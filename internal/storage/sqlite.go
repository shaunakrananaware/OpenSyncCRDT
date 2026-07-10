package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
)

// sqliteStore is the default backend: an embedded single-file SQLite database
// (github.com/mattn/go-sqlite3) in WAL mode. It is the one place in
// OpenSyncCRDT that requires CGO.
type sqliteStore struct {
	db   *sql.DB
	path string
}

// compile-time assertion that sqliteStore satisfies the contract.
var _ Store = (*sqliteStore)(nil)

// openSQLite opens (creating if necessary) the SQLite database under
// cfg.DataDir, applies pragmas and the schema, and returns a ready store.
func openSQLite(ctx context.Context, cfg config.StorageConfig) (*sqliteStore, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir %q: %w", cfg.DataDir, err)
	}
	path := filepath.Join(cfg.DataDir, "opensynccrdt.db")

	// WAL gives concurrent readers alongside a single writer; foreign keys must
	// be enabled per-connection for ON DELETE CASCADE to fire; busy_timeout makes
	// writers wait for a held lock instead of failing immediately; _txlock=
	// immediate takes the write lock at BEGIN so AppendOp's read-then-write
	// transaction cannot deadlock on a lock upgrade when the pool holds more than
	// one connection.
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=%d&_synchronous=NORMAL&_txlock=immediate",
		path, cfg.BusyTimeout.Milliseconds(),
	)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	// SQLite serializes writes; a bounded pool avoids "database is locked"
	// storms while still allowing concurrent WAL readers.
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}

	s := &sqliteStore{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}
	return s, nil
}

// schema is applied idempotently on every open. Versioned migrations can be
// layered on later without changing the contract.
const sqliteSchema = `
CREATE TABLE IF NOT EXISTS documents (
	id          TEXT    PRIMARY KEY,
	metadata    TEXT    NOT NULL DEFAULT '{}',
	latest_seq  INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS operations (
	doc_id      TEXT    NOT NULL,
	seq         INTEGER NOT NULL,
	session_id  TEXT    NOT NULL DEFAULT '',
	payload     BLOB    NOT NULL,
	created_at  INTEGER NOT NULL,
	PRIMARY KEY (doc_id, seq),
	FOREIGN KEY (doc_id) REFERENCES documents(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE IF NOT EXISTS snapshots (
	doc_id      TEXT    NOT NULL,
	seq         INTEGER NOT NULL,
	state       BLOB    NOT NULL,
	created_at  INTEGER NOT NULL,
	PRIMARY KEY (doc_id, seq),
	FOREIGN KEY (doc_id) REFERENCES documents(id) ON DELETE CASCADE
) STRICT;

CREATE INDEX IF NOT EXISTS idx_documents_updated_at ON documents(updated_at DESC);
`

func (s *sqliteStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, sqliteSchema)
	return err
}

// Close releases the underlying database handle.
func (s *sqliteStore) Close() error { return s.db.Close() }

// HealthCheck verifies the database is reachable.
func (s *sqliteStore) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.db.PingContext(ctx)
}

// --- documents --------------------------------------------------------------

func (s *sqliteStore) CreateDocument(id string, metadata map[string]string) error {
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
		if isUniqueViolation(err) {
			return ErrDocumentExists
		}
		return fmt.Errorf("create document: %w", err)
	}
	return nil
}

func (s *sqliteStore) GetDocument(id string) (*Document, error) {
	return scanDocument(s.db.QueryRow(
		`SELECT id, metadata, latest_seq, created_at, updated_at
		 FROM documents WHERE id = ?`, id))
}

func (s *sqliteStore) ListDocuments(f DocumentFilter) ([]Document, error) {
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

func (s *sqliteStore) DeleteDocument(id string) error {
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

func (s *sqliteStore) AppendOp(docID string, change []byte, sessionID string) (int64, error) {
	// A single IMMEDIATE transaction serializes sequence assignment: read the
	// current latest_seq, insert at seq+1, and advance the document row.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin append tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	now := time.Now().UTC().UnixMilli()

	var latest int64
	err = tx.QueryRow(`SELECT latest_seq FROM documents WHERE id = ?`, docID).Scan(&latest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// First write creates the document metadata.
		if _, err := tx.Exec(
			`INSERT INTO documents (id, metadata, latest_seq, created_at, updated_at)
			 VALUES (?, '{}', 0, ?, ?)`,
			docID, now, now,
		); err != nil {
			return 0, fmt.Errorf("create document on first write: %w", err)
		}
		latest = 0
	case err != nil:
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

func (s *sqliteStore) GetOpsSince(docID string, afterSeq int64) ([]StoredOp, error) {
	return s.queryOps(
		`SELECT doc_id, seq, session_id, payload, created_at
		 FROM operations WHERE doc_id = ? AND seq > ? ORDER BY seq ASC`,
		docID, afterSeq,
	)
}

func (s *sqliteStore) GetOpsInRange(docID string, fromSeq, toSeq int64) ([]StoredOp, error) {
	return s.queryOps(
		`SELECT doc_id, seq, session_id, payload, created_at
		 FROM operations WHERE doc_id = ? AND seq >= ? AND seq <= ? ORDER BY seq ASC`,
		docID, fromSeq, toSeq,
	)
}

func (s *sqliteStore) queryOps(query string, args ...any) ([]StoredOp, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ops: %w", err)
	}
	defer rows.Close()
	return scanOpRows(rows)
}

func (s *sqliteStore) GetLatestSeq(docID string) (int64, error) {
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

func (s *sqliteStore) SaveSnapshot(docID string, state []byte, atSeq int64) error {
	now := time.Now().UTC().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO snapshots (doc_id, seq, state, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(doc_id, seq) DO UPDATE SET state = excluded.state, created_at = excluded.created_at`,
		docID, atSeq, state, now,
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (s *sqliteStore) GetSnapshot(docID string) ([]byte, int64, error) {
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

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDocument(row rowScanner) (*Document, error) {
	var (
		doc       Document
		meta      string
		createdMs int64
		updatedMs int64
	)
	err := row.Scan(&doc.ID, &meta, &doc.LatestSeq, &createdMs, &updatedMs)
	if errors.Is(err, sql.ErrNoRows) {
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

func marshalMetadata(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}
	return string(b), nil
}

func unmarshalMetadata(s string) (map[string]string, error) {
	if s == "" || s == "{}" {
		return map[string]string{}, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return m, nil
}

func isUniqueViolation(err error) bool {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code == sqlite3.ErrConstraint &&
			sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey
	}
	return false
}
