package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
)

// mysqlStore is the MySQL backend, built on the standard database/sql interface
// with the pure-Go go-sql-driver/mysql driver. database/sql reconnects
// automatically on connection loss.
//
// Like the other backends, timestamps and sequences are stored as BIGINT (unix
// milliseconds) and payloads as LONGBLOB, so the shared op/document scanning
// logic and the integration suite behave identically.
type mysqlStore struct {
	db *sql.DB
}

var _ Store = (*mysqlStore)(nil)

// openMySQL converts the STORAGE_URL into a go-sql-driver DSN, connects,
// applies pool sizing, and runs migrations.
func openMySQL(ctx context.Context, cfg config.StorageConfig) (*mysqlStore, error) {
	dsn, err := mysqlDSN(cfg.URL)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	if cfg.MySQLMaxConns > 0 {
		db.SetMaxOpenConns(cfg.MySQLMaxConns)
	}
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	s := &mysqlStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate mysql schema: %w", err)
	}
	return s, nil
}

// mysqlDSN converts a mysql://user:pass@host:3306/dbname URL (as specified by
// STORAGE_URL) into the user:pass@tcp(host:port)/dbname DSN the driver expects.
// Query parameters from the URL are preserved.
func mysqlDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse mysql url: %w", err)
	}
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	host := u.Host
	if u.Port() == "" {
		host = u.Hostname() + ":3306"
	}
	cfg.Addr = host
	cfg.DBName = u.Path
	if len(cfg.DBName) > 0 && cfg.DBName[0] == '/' {
		cfg.DBName = cfg.DBName[1:]
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			cfg.Passwd = pw
		}
	}
	// Merge any URL query parameters (e.g. tls, collation) into driver params.
	for k, vs := range u.Query() {
		if len(vs) > 0 {
			if cfg.Params == nil {
				cfg.Params = map[string]string{}
			}
			cfg.Params[k] = vs[0]
		}
	}
	return cfg.FormatDSN(), nil
}

// --- migrations -------------------------------------------------------------

// mysqlMigrations is the ordered list of schema versions. Each entry is a set
// of statements applied atomically and recorded in schema_migrations. MySQL
// DDL cannot be rolled back, but the version guard keeps each migration
// idempotent across restarts.
var mysqlMigrations = [][]string{
	{
		`CREATE TABLE IF NOT EXISTS documents (
			id          VARCHAR(255) NOT NULL,
			metadata    TEXT         NOT NULL,
			latest_seq  BIGINT       NOT NULL DEFAULT 0,
			created_at  BIGINT       NOT NULL,
			updated_at  BIGINT       NOT NULL,
			PRIMARY KEY (id),
			INDEX idx_documents_updated_at (updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS operations (
			doc_id      VARCHAR(255) NOT NULL,
			seq         BIGINT       NOT NULL,
			session_id  VARCHAR(255) NOT NULL DEFAULT '',
			payload     LONGBLOB     NOT NULL,
			created_at  BIGINT       NOT NULL,
			PRIMARY KEY (doc_id, seq),
			CONSTRAINT fk_operations_doc FOREIGN KEY (doc_id)
				REFERENCES documents(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS snapshots (
			doc_id      VARCHAR(255) NOT NULL,
			seq         BIGINT       NOT NULL,
			state       LONGBLOB     NOT NULL,
			created_at  BIGINT       NOT NULL,
			PRIMARY KEY (doc_id, seq),
			CONSTRAINT fk_snapshots_doc FOREIGN KEY (doc_id)
				REFERENCES documents(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	},
}

func (s *mysqlStore) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    BIGINT NOT NULL,
		applied_at BIGINT NOT NULL,
		PRIMARY KEY (version)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i, stmts := range mysqlMigrations {
		version := int64(i + 1)
		if version <= current {
			continue
		}
		for _, stmt := range stmts {
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply migration %d: %w", version, err)
			}
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().UTC().UnixMilli()); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
	}
	return nil
}

// Close releases the underlying database handle.
func (s *mysqlStore) Close() error { return s.db.Close() }

func (s *mysqlStore) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.db.PingContext(ctx)
}

// --- documents --------------------------------------------------------------

func (s *mysqlStore) CreateDocument(id string, metadata map[string]string) error {
	meta, err := marshalMetadata(metadata)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	_, err = s.db.Exec(
		`INSERT INTO documents (id, metadata, latest_seq, created_at, updated_at)
		 VALUES (?, ?, 0, ?, ?)`,
		id, meta, now, now,
	)
	if err != nil {
		if isMySQLDuplicate(err) {
			return ErrDocumentExists
		}
		return fmt.Errorf("create document: %w", err)
	}
	return nil
}

func (s *mysqlStore) GetDocument(id string) (*Document, error) {
	return scanDocument(s.db.QueryRow(
		`SELECT id, metadata, latest_seq, created_at, updated_at
		 FROM documents WHERE id = ?`, id))
}

func (s *mysqlStore) ListDocuments(f DocumentFilter) ([]Document, error) {
	query := `SELECT id, metadata, latest_seq, created_at, updated_at FROM documents`
	var args []any
	if !f.UpdatedSince.IsZero() {
		query += " WHERE updated_at >= ?"
		args = append(args, f.UpdatedSince.UTC().UnixMilli())
	}
	query += " ORDER BY updated_at DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
		if f.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, f.Offset)
		}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	docs := []Document{}
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, *doc)
	}
	return docs, rows.Err()
}

func (s *mysqlStore) DeleteDocument(id string) error {
	// ON DELETE CASCADE removes operations and snapshots along with the row.
	res, err := s.db.Exec(`DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete document rows affected: %w", err)
	}
	if n == 0 {
		return ErrDocumentNotFound
	}
	return nil
}

// --- operations -------------------------------------------------------------

func (s *mysqlStore) AppendOp(docID string, change []byte, sessionID string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin append tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	now := time.Now().UTC().UnixMilli()

	// Ensure the document row exists, then take a row lock (SELECT ... FOR
	// UPDATE) so concurrent appenders serialize on sequence assignment.
	if _, err := tx.Exec(
		`INSERT INTO documents (id, metadata, latest_seq, created_at, updated_at)
		 VALUES (?, '{}', 0, ?, ?) ON DUPLICATE KEY UPDATE id = id`,
		docID, now, now,
	); err != nil {
		return 0, fmt.Errorf("ensure document on first write: %w", err)
	}

	var latest int64
	if err := tx.QueryRow(
		`SELECT latest_seq FROM documents WHERE id = ? FOR UPDATE`, docID).Scan(&latest); err != nil {
		return 0, fmt.Errorf("read latest seq: %w", err)
	}

	seq := latest + 1
	if _, err := tx.Exec(
		`INSERT INTO operations (doc_id, seq, session_id, payload, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		docID, seq, sessionID, change, now,
	); err != nil {
		return 0, fmt.Errorf("insert operation: %w", err)
	}

	if _, err := tx.Exec(
		`UPDATE documents SET latest_seq = ?, updated_at = ? WHERE id = ?`,
		seq, now, docID,
	); err != nil {
		return 0, fmt.Errorf("advance latest seq: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit append tx: %w", err)
	}
	return seq, nil
}

func (s *mysqlStore) GetOpsSince(docID string, afterSeq int64) ([]StoredOp, error) {
	return s.queryOps(
		`SELECT doc_id, seq, session_id, payload, created_at
		 FROM operations WHERE doc_id = ? AND seq > ? ORDER BY seq ASC`,
		docID, afterSeq,
	)
}

func (s *mysqlStore) GetOpsInRange(docID string, fromSeq, toSeq int64) ([]StoredOp, error) {
	return s.queryOps(
		`SELECT doc_id, seq, session_id, payload, created_at
		 FROM operations WHERE doc_id = ? AND seq >= ? AND seq <= ? ORDER BY seq ASC`,
		docID, fromSeq, toSeq,
	)
}

func (s *mysqlStore) queryOps(query string, args ...any) ([]StoredOp, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ops: %w", err)
	}
	defer rows.Close()
	return scanOpRows(rows)
}

func (s *mysqlStore) GetLatestSeq(docID string) (int64, error) {
	var latest int64
	err := s.db.QueryRow(`SELECT latest_seq FROM documents WHERE id = ?`, docID).Scan(&latest)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get latest seq: %w", err)
	}
	return latest, nil
}

// --- snapshots --------------------------------------------------------------

func (s *mysqlStore) SaveSnapshot(docID string, state []byte, atSeq int64) error {
	now := time.Now().UTC().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO snapshots (doc_id, seq, state, created_at)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE state = VALUES(state), created_at = VALUES(created_at)`,
		docID, atSeq, state, now,
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (s *mysqlStore) GetSnapshot(docID string) ([]byte, int64, error) {
	var (
		state []byte
		atSeq int64
	)
	err := s.db.QueryRow(
		`SELECT state, seq FROM snapshots WHERE doc_id = ? ORDER BY seq DESC LIMIT 1`, docID,
	).Scan(&state, &atSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNoSnapshot
	}
	if err != nil {
		return nil, 0, fmt.Errorf("get snapshot: %w", err)
	}
	return state, atSeq, nil
}

// --- helpers ----------------------------------------------------------------

func isMySQLDuplicate(err error) bool {
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number == 1062 // ER_DUP_ENTRY
	}
	return false
}
