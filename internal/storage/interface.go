// Package storage defines the persistence contract for OpenSyncCRDT and the
// factory that constructs the configured backend.
//
// Every backend implements the same Store interface. The active backend is
// selected at startup from configuration — no code changes or recompilation is
// needed to switch backends. All sync logic, conflict resolution, WebSocket
// handling, and webhook dispatch are identical regardless of backend; the
// storage backend is the only thing that differs.
//
// The store persists three things per document:
//
//   - document metadata (identity, developer-supplied metadata, latest seq)
//   - an ordered op log of raw Automerge binary change sets
//   - periodic full-state snapshots of the Automerge document
//
// The store treats Automerge change sets and snapshots as opaque binary blobs.
// It never inspects or interprets their contents; the CRDT math happens inside
// the Automerge library. The store's only job is to store, order, and hand
// back those blobs reliably.
package storage

import (
	"errors"
	"time"
)

// Sentinel errors returned by store implementations. Callers compare with
// errors.Is.
var (
	// ErrDocumentNotFound is returned when a document does not exist.
	ErrDocumentNotFound = errors.New("document not found")
	// ErrDocumentExists is returned by CreateDocument when the id is taken.
	ErrDocumentExists = errors.New("document already exists")
	// ErrNoSnapshot is returned by GetSnapshot when a document has no snapshot.
	ErrNoSnapshot = errors.New("no snapshot for document")
)

// StoredOp is a single committed Automerge change set within a document's
// ordered op log, as read back from storage.
type StoredOp struct {
	Seq       int64
	DocID     string
	SessionID string
	// Payload is the raw Automerge binary change set. Opaque to the store.
	Payload   []byte
	CreatedAt time.Time
}

// Document is the metadata OpenSyncCRDT tracks for a synced document. The
// actual CRDT contents live in the op log and snapshots, not here.
type Document struct {
	ID string
	// Metadata is developer-supplied key/value metadata attached at creation.
	Metadata map[string]string
	// LatestSeq is the highest sequence number committed for this document. 0
	// means no operations have been committed yet.
	LatestSeq int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DocumentFilter narrows the results of ListDocuments. Zero-valued fields are
// ignored.
type DocumentFilter struct {
	// UpdatedSince, when non-zero, restricts results to documents updated at or
	// after the given time.
	UpdatedSince time.Time
	// Limit caps the number of returned documents. 0 means no limit.
	Limit int
	// Offset skips the given number of documents for pagination.
	Offset int
}

// Store is the persistence contract every backend implements. All methods are
// safe for concurrent use.
//
// The signatures match the specification exactly so that alternative backends
// (Postgres, MySQL) are drop-in and pass the same integration suite.
type Store interface {
	// AppendOp atomically assigns the next sequence number for the document,
	// persists the change set, and advances the document's latest seq. The
	// document metadata row is created on first write if it does not exist. The
	// first committed op on a document has seq 1.
	AppendOp(docID string, change []byte, sessionID string) (seq int64, err error)

	// GetOpsSince returns operations with seq strictly greater than afterSeq, in
	// ascending sequence order. Use afterSeq 0 to read from the beginning. A
	// document with no matching ops (or no such document) yields an empty slice.
	GetOpsSince(docID string, afterSeq int64) ([]StoredOp, error)

	// GetOpsInRange returns operations with fromSeq <= seq <= toSeq in ascending
	// sequence order.
	GetOpsInRange(docID string, fromSeq, toSeq int64) ([]StoredOp, error)

	// SaveSnapshot persists a full-state snapshot reflecting all ops up to and
	// including atSeq. Re-saving at the same seq replaces the prior snapshot.
	SaveSnapshot(docID string, state []byte, atSeq int64) error

	// GetSnapshot returns the highest-seq snapshot for a document, or
	// ErrNoSnapshot if none exists.
	GetSnapshot(docID string) (state []byte, atSeq int64, err error)

	// GetLatestSeq returns the document's highest committed sequence number, or
	// 0 if the document does not exist or has no ops.
	GetLatestSeq(docID string) (int64, error)

	// CreateDocument explicitly creates document metadata. Returns
	// ErrDocumentExists if the id is already in use.
	CreateDocument(docID string, metadata map[string]string) error

	// GetDocument returns document metadata, or ErrDocumentNotFound.
	GetDocument(docID string) (*Document, error)

	// ListDocuments returns documents matching the filter, most recently updated
	// first.
	ListDocuments(filter DocumentFilter) ([]Document, error)

	// DeleteDocument removes a document and all of its operations and snapshots.
	// Returns ErrDocumentNotFound if it does not exist.
	DeleteDocument(docID string) error

	// HealthCheck verifies the backend is reachable and healthy.
	HealthCheck() error

	// Close releases all resources held by the store. Not part of the
	// specification's interface listing, but required for a clean shutdown.
	Close() error
}
