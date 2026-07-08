// Package sqlite implements the storage.Store contract on top of an embedded
// SQLite database (github.com/mattn/go-sqlite3). It is the default backend: a
// single file, no external services, WAL-mode for concurrent readers.
//
// This is the one place in OpenSyncCRDT that requires CGO.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/storage"
)

// Store is the SQLite-backed implementation of storage.Store.
type Store struct {
	db *sql.DB
}

// compile-time assertion that Store satisfies the contract.
var _ storage.Store = (*Store)(nil)

// Open opens (creating if necessary) the SQLite database at cfg.SQLitePath,
// applies pragmas and the schema, and returns a ready Store.
func Open(ctx context.Context, cfg config.StorageConfig) (*Store, error) {
	// WAL gives us concurrent readers alongside a single writer; foreign keys
	// must be enabled per-connection for ON DELETE CASCADE to fire; busy_timeout
	// makes writers wait for a held lock instead of failing immediately;
	// _txlock=immediate takes the write lock at BEGIN so AppendOp's
	// read-then-write transaction cannot deadlock on a lock upgrade when more
	// than one connection is in the pool.
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=%d&_synchronous=NORMAL&_txlock=immediate",
		cfg.SQLitePath, cfg.BusyTimeout.Milliseconds(),
	)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", cfg.SQLitePath, err)
	}

	// SQLite serializes writes; a bounded pool avoids "database is locked"
	// storms while still allowing concurrent WAL readers.
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", cfg.SQLitePath, err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}
	return s, nil
}

// schema is applied idempotently on every Open. It is intentionally simple;
// versioned migrations can be layered on later without changing the contract.
const schema = `
CREATE TABLE IF NOT EXISTS documents (
	id          TEXT    PRIMARY KEY,
	created_by  TEXT    NOT NULL DEFAULT '',
	latest_seq  INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS operations (
	doc_id      TEXT    NOT NULL,
	seq         INTEGER NOT NULL,
	session_id  TEXT    NOT NULL DEFAULT '',
	user_id     TEXT    NOT NULL DEFAULT '',
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

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// --- documents --------------------------------------------------------------

func (s *Store) CreateDocument(ctx context.Context, id, createdBy string) (*storage.Document, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO documents (id, created_by, latest_seq, created_at, updated_at)
		 VALUES (?, ?, 0, ?, ?)`,
		id, createdBy, now.UnixMilli(), now.UnixMilli(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, storage.ErrDocumentExists
		}
		return nil, fmt.Errorf("create document: %w", err)
	}
	return &storage.Document{
		ID:        id,
		CreatedBy: createdBy,
		LatestSeq: 0,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *Store) GetDocument(ctx context.Context, id string) (*storage.Document, error) {
	return scanDocument(s.db.QueryRowContext(ctx,
		`SELECT id, created_by, latest_seq, created_at, updated_at
		 FROM documents WHERE id = ?`, id))
}

func (s *Store) ListDocuments(ctx context.Context, f storage.DocumentFilter) ([]*storage.Document, error) {
	query := `SELECT id, created_by, latest_seq, created_at, updated_at FROM documents`
	var conds []string
	var args []any
	if f.CreatedBy != "" {
		conds = append(conds, "created_by = ?")
		args = append(args, f.CreatedBy)
	}
	if !f.UpdatedSince.IsZero() {
		conds = append(conds, "updated_at >= ?")
		args = append(args, f.UpdatedSince.UTC().UnixMilli())
	}
	for i, c := range conds {
		if i == 0 {
			query += " WHERE "
		} else {
			query += " AND "
		}
		query += c
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

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	var docs []*storage.Document
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func (s *Store) DeleteDocument(ctx context.Context, id string) error {
	// ON DELETE CASCADE removes operations and snapshots along with the row.
	res, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete document rows affected: %w", err)
	}
	if n == 0 {
		return storage.ErrDocumentNotFound
	}
	return nil
}

// --- operations -------------------------------------------------------------

func (s *Store) AppendOp(ctx context.Context, op storage.Operation) (storage.AppendResult, error) {
	// A single IMMEDIATE transaction serializes sequence assignment: read the
	// current latest_seq, insert at seq+1, and advance the document row.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return storage.AppendResult{}, fmt.Errorf("begin append tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	now := time.Now().UTC()
	created := false

	var latest int64
	err = tx.QueryRowContext(ctx,
		`SELECT latest_seq FROM documents WHERE id = ?`, op.DocID).Scan(&latest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// First write creates the document metadata.
		created = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO documents (id, created_by, latest_seq, created_at, updated_at)
			 VALUES (?, ?, 0, ?, ?)`,
			op.DocID, op.UserID, now.UnixMilli(), now.UnixMilli(),
		); err != nil {
			return storage.AppendResult{}, fmt.Errorf("create document on first write: %w", err)
		}
		latest = 0
	case err != nil:
		return storage.AppendResult{}, fmt.Errorf("read latest seq: %w", err)
	}

	seq := latest + 1
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO operations (doc_id, seq, session_id, user_id, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		op.DocID, seq, op.SessionID, op.UserID, op.Payload, now.UnixMilli(),
	); err != nil {
		return storage.AppendResult{}, fmt.Errorf("insert operation: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE documents SET latest_seq = ?, updated_at = ? WHERE id = ?`,
		seq, now.UnixMilli(), op.DocID,
	); err != nil {
		return storage.AppendResult{}, fmt.Errorf("advance latest seq: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return storage.AppendResult{}, fmt.Errorf("commit append tx: %w", err)
	}
	return storage.AppendResult{Seq: seq, Created: created}, nil
}

func (s *Store) ListOps(ctx context.Context, docID string, afterSeq int64, limit int) ([]*storage.Operation, error) {
	query := `SELECT doc_id, seq, session_id, user_id, payload, created_at
	          FROM operations WHERE doc_id = ? AND seq > ? ORDER BY seq ASC`
	args := []any{docID, afterSeq}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ops: %w", err)
	}
	defer rows.Close()

	var ops []*storage.Operation
	for rows.Next() {
		var (
			op        storage.Operation
			createdMs int64
		)
		if err := rows.Scan(&op.DocID, &op.Seq, &op.SessionID, &op.UserID, &op.Payload, &createdMs); err != nil {
			return nil, fmt.Errorf("scan op: %w", err)
		}
		op.CreatedAt = time.UnixMilli(createdMs).UTC()
		ops = append(ops, &op)
	}
	return ops, rows.Err()
}

// --- snapshots --------------------------------------------------------------

func (s *Store) SaveSnapshot(ctx context.Context, snap storage.Snapshot) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO snapshots (doc_id, seq, state, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(doc_id, seq) DO UPDATE SET state = excluded.state, created_at = excluded.created_at`,
		snap.DocID, snap.Seq, snap.State, now.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (s *Store) LatestSnapshot(ctx context.Context, docID string) (*storage.Snapshot, error) {
	var (
		snap      storage.Snapshot
		createdMs int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT doc_id, seq, state, created_at FROM snapshots
		 WHERE doc_id = ? ORDER BY seq DESC LIMIT 1`, docID,
	).Scan(&snap.DocID, &snap.Seq, &snap.State, &createdMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNoSnapshot
	}
	if err != nil {
		return nil, fmt.Errorf("latest snapshot: %w", err)
	}
	snap.CreatedAt = time.UnixMilli(createdMs).UTC()
	return &snap, nil
}

// --- helpers ----------------------------------------------------------------

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDocument(row rowScanner) (*storage.Document, error) {
	var (
		doc       storage.Document
		createdMs int64
		updatedMs int64
	)
	err := row.Scan(&doc.ID, &doc.CreatedBy, &doc.LatestSeq, &createdMs, &updatedMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan document: %w", err)
	}
	doc.CreatedAt = time.UnixMilli(createdMs).UTC()
	doc.UpdatedAt = time.UnixMilli(updatedMs).UTC()
	return &doc, nil
}

func isUniqueViolation(err error) bool {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code == sqlite3.ErrConstraint &&
			sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey
	}
	return false
}
