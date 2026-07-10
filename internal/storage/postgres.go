package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
)

// postgresStore is the PostgreSQL backend, built on a native pgx connection
// pool (github.com/jackc/pgx/v5). It is pure Go — no CGO. The pool reconnects
// automatically on connection loss.
//
// Timestamps and sequences are stored as BIGINT (unix milliseconds) and
// payloads as BYTEA, mirroring the SQLite schema so the shared document/op
// scanning logic and the integration suite behave identically across backends.
type postgresStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*postgresStore)(nil)

// openPostgres parses cfg.URL, applies the connection-pool sizing, connects,
// and runs migrations. The pool keeps a minimum of 2 connections warm as the
// specification requires, with a configurable maximum.
func openPostgres(ctx context.Context, cfg config.StorageConfig) (*postgresStore, error) {
	connString, err := pgConnString(cfg)
	if err != nil {
		return nil, err
	}

	poolCfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}

	poolCfg.MinConns = 2
	maxConns := int32(cfg.PostgresMaxConns)
	if maxConns < poolCfg.MinConns {
		maxConns = poolCfg.MinConns
	}
	poolCfg.MaxConns = maxConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	s := &postgresStore{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate postgres schema: %w", err)
	}
	return s, nil
}

// pgConnString returns the connection string, ensuring the configured sslmode
// is applied when the URL does not already specify one.
func pgConnString(cfg config.StorageConfig) (string, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return "", fmt.Errorf("parse postgres url: %w", err)
	}
	q := u.Query()
	if q.Get("sslmode") == "" && cfg.PostgresSSLMode != "" {
		q.Set("sslmode", cfg.PostgresSSLMode)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// --- migrations -------------------------------------------------------------

// pgMigrations is the ordered list of schema versions. Each is applied exactly
// once; applied versions are recorded in schema_migrations.
var pgMigrations = []string{
	`CREATE TABLE IF NOT EXISTS documents (
		id          TEXT   PRIMARY KEY,
		metadata    TEXT   NOT NULL DEFAULT '{}',
		latest_seq  BIGINT NOT NULL DEFAULT 0,
		created_at  BIGINT NOT NULL,
		updated_at  BIGINT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS operations (
		doc_id      TEXT   NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		seq         BIGINT NOT NULL,
		session_id  TEXT   NOT NULL DEFAULT '',
		payload     BYTEA  NOT NULL,
		created_at  BIGINT NOT NULL,
		PRIMARY KEY (doc_id, seq)
	);
	CREATE TABLE IF NOT EXISTS snapshots (
		doc_id      TEXT   NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		seq         BIGINT NOT NULL,
		state       BYTEA  NOT NULL,
		created_at  BIGINT NOT NULL,
		PRIMARY KEY (doc_id, seq)
	);
	CREATE INDEX IF NOT EXISTS idx_documents_updated_at ON documents(updated_at DESC);`,
}

func (s *postgresStore) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    BIGINT PRIMARY KEY,
		applied_at BIGINT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int64
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i, stmt := range pgMigrations {
		version := int64(i + 1)
		if version <= current {
			continue
		}
		if err := s.applyMigration(ctx, version, stmt); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
	}
	return nil
}

func (s *postgresStore) applyMigration(ctx context.Context, version int64, stmt string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	if _, err := tx.Exec(ctx, stmt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`,
		version, time.Now().UTC().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Close releases the connection pool.
func (s *postgresStore) Close() error {
	s.pool.Close()
	return nil
}

func (s *postgresStore) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.pool.Ping(ctx)
}

// --- documents --------------------------------------------------------------

func (s *postgresStore) CreateDocument(id string, metadata map[string]string) error {
	meta, err := marshalMetadata(metadata)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	_, err = s.pool.Exec(context.Background(),
		`INSERT INTO documents (id, metadata, latest_seq, created_at, updated_at)
		 VALUES ($1, $2, 0, $3, $3)`,
		id, meta, now,
	)
	if err != nil {
		if isPGUniqueViolation(err) {
			return ErrDocumentExists
		}
		return fmt.Errorf("create document: %w", err)
	}
	return nil
}

func (s *postgresStore) GetDocument(id string) (*Document, error) {
	row := s.pool.QueryRow(context.Background(),
		`SELECT id, metadata, latest_seq, created_at, updated_at
		 FROM documents WHERE id = $1`, id)
	return pgScanDocument(row)
}

func (s *postgresStore) ListDocuments(f DocumentFilter) ([]Document, error) {
	query := `SELECT id, metadata, latest_seq, created_at, updated_at FROM documents`
	var args []any
	n := 0
	if !f.UpdatedSince.IsZero() {
		n++
		query += fmt.Sprintf(" WHERE updated_at >= $%d", n)
		args = append(args, f.UpdatedSince.UTC().UnixMilli())
	}
	query += " ORDER BY updated_at DESC"
	if f.Limit > 0 {
		n++
		query += fmt.Sprintf(" LIMIT $%d", n)
		args = append(args, f.Limit)
		if f.Offset > 0 {
			n++
			query += fmt.Sprintf(" OFFSET $%d", n)
			args = append(args, f.Offset)
		}
	}

	rows, err := s.pool.Query(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	docs := []Document{}
	for rows.Next() {
		doc, err := pgScanDocument(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, *doc)
	}
	return docs, rows.Err()
}

func (s *postgresStore) DeleteDocument(id string) error {
	// ON DELETE CASCADE removes operations and snapshots along with the row.
	tag, err := s.pool.Exec(context.Background(), `DELETE FROM documents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDocumentNotFound
	}
	return nil
}

// --- operations -------------------------------------------------------------

func (s *postgresStore) AppendOp(docID string, change []byte, sessionID string) (int64, error) {
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin append tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	now := time.Now().UTC().UnixMilli()

	// Ensure the document row exists, then take a row lock so concurrent
	// appenders serialize on sequence assignment (gap-free, unique seqs).
	if _, err := tx.Exec(ctx,
		`INSERT INTO documents (id, metadata, latest_seq, created_at, updated_at)
		 VALUES ($1, '{}', 0, $2, $2) ON CONFLICT (id) DO NOTHING`,
		docID, now,
	); err != nil {
		return 0, fmt.Errorf("ensure document on first write: %w", err)
	}

	var latest int64
	if err := tx.QueryRow(ctx,
		`SELECT latest_seq FROM documents WHERE id = $1 FOR UPDATE`, docID).Scan(&latest); err != nil {
		return 0, fmt.Errorf("read latest seq: %w", err)
	}

	seq := latest + 1
	if _, err := tx.Exec(ctx,
		`INSERT INTO operations (doc_id, seq, session_id, payload, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		docID, seq, sessionID, change, now,
	); err != nil {
		return 0, fmt.Errorf("insert operation: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE documents SET latest_seq = $1, updated_at = $2 WHERE id = $3`,
		seq, now, docID,
	); err != nil {
		return 0, fmt.Errorf("advance latest seq: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit append tx: %w", err)
	}
	return seq, nil
}

func (s *postgresStore) GetOpsSince(docID string, afterSeq int64) ([]StoredOp, error) {
	return s.queryOps(
		`SELECT doc_id, seq, session_id, payload, created_at
		 FROM operations WHERE doc_id = $1 AND seq > $2 ORDER BY seq ASC`,
		docID, afterSeq,
	)
}

func (s *postgresStore) GetOpsInRange(docID string, fromSeq, toSeq int64) ([]StoredOp, error) {
	return s.queryOps(
		`SELECT doc_id, seq, session_id, payload, created_at
		 FROM operations WHERE doc_id = $1 AND seq >= $2 AND seq <= $3 ORDER BY seq ASC`,
		docID, fromSeq, toSeq,
	)
}

func (s *postgresStore) queryOps(query string, args ...any) ([]StoredOp, error) {
	rows, err := s.pool.Query(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ops: %w", err)
	}
	defer rows.Close()

	ops := []StoredOp{}
	for rows.Next() {
		var (
			op        StoredOp
			createdMs int64
		)
		if err := rows.Scan(&op.DocID, &op.Seq, &op.SessionID, &op.Payload, &createdMs); err != nil {
			return nil, fmt.Errorf("scan op: %w", err)
		}
		op.CreatedAt = time.UnixMilli(createdMs).UTC()
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

func (s *postgresStore) GetLatestSeq(docID string) (int64, error) {
	var latest int64
	err := s.pool.QueryRow(context.Background(),
		`SELECT latest_seq FROM documents WHERE id = $1`, docID).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get latest seq: %w", err)
	}
	return latest, nil
}

// --- snapshots --------------------------------------------------------------

func (s *postgresStore) SaveSnapshot(docID string, state []byte, atSeq int64) error {
	now := time.Now().UTC().UnixMilli()
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO snapshots (doc_id, seq, state, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (doc_id, seq) DO UPDATE SET state = EXCLUDED.state, created_at = EXCLUDED.created_at`,
		docID, atSeq, state, now,
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (s *postgresStore) GetSnapshot(docID string) ([]byte, int64, error) {
	var (
		state []byte
		atSeq int64
	)
	err := s.pool.QueryRow(context.Background(),
		`SELECT state, seq FROM snapshots WHERE doc_id = $1 ORDER BY seq DESC LIMIT 1`, docID,
	).Scan(&state, &atSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNoSnapshot
	}
	if err != nil {
		return nil, 0, fmt.Errorf("get snapshot: %w", err)
	}
	return state, atSeq, nil
}

// --- helpers ----------------------------------------------------------------

// pgRow is satisfied by both pgx.Row (single-row query) and pgx.Rows.
type pgRow interface {
	Scan(dest ...any) error
}

func pgScanDocument(row pgRow) (*Document, error) {
	var (
		doc       Document
		meta      string
		createdMs int64
		updatedMs int64
	)
	err := row.Scan(&doc.ID, &meta, &doc.LatestSeq, &createdMs, &updatedMs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan document: %w", err)
	}
	if doc.Metadata, err = unmarshalMetadata(meta); err != nil {
		return nil, err
	}
	doc.CreatedAt = time.UnixMilli(createdMs).UTC()
	doc.UpdatedAt = time.UnixMilli(updatedMs).UTC()
	return &doc, nil
}

func isPGUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" // unique_violation
	}
	return false
}
