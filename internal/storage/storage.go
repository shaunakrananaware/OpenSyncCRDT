// Package storage defines the persistence contract for OpenSyncCRDT and the
// factory that constructs a concrete backend from configuration.
//
// The store persists three things per document:
//
//   - document metadata (identity, ownership, latest sequence number)
//   - an ordered op log of raw Automerge binary change sets
//   - periodic full-state snapshots of the Automerge document
//
// The store treats Automerge change sets and snapshots as opaque binary blobs.
// It never inspects or interprets their contents; the CRDT math happens inside
// the Automerge library. The store's only job is to store, order, and hand
// back those blobs reliably.
package storage

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by store implementations. Callers should compare
// with errors.Is.
var (
	// ErrDocumentNotFound is returned when a document does not exist.
	ErrDocumentNotFound = errors.New("document not found")
	// ErrDocumentExists is returned by CreateDocument when the id is taken.
	ErrDocumentExists = errors.New("document already exists")
	// ErrNoSnapshot is returned when a document has no snapshot yet.
	ErrNoSnapshot = errors.New("no snapshot for document")
)

// Document is the metadata OpenSyncCRDT tracks for a synced document. The
// actual CRDT contents live in the op log and snapshots, not here.
type Document struct {
	// ID is the caller-supplied document identifier.
	ID string
	// CreatedBy is the user identity that first created the document, or empty
	// when unknown (e.g. auth mode none).
	CreatedBy string
	// LatestSeq is the highest sequence number committed for this document. 0
	// means no operations have been committed yet.
	LatestSeq int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Operation is a single committed Automerge change set within a document's
// ordered op log.
type Operation struct {
	DocID     string
	Seq       int64
	SessionID string
	// UserID is the identity that produced the change, or empty when unknown.
	UserID string
	// Payload is the raw Automerge binary change set. Opaque to the store.
	Payload   []byte
	CreatedAt time.Time
}

// Snapshot is a full Automerge document state captured up to and including a
// given sequence number, so that startup can begin from the snapshot instead
// of replaying the entire op log.
type Snapshot struct {
	DocID string
	// Seq is the highest op sequence number reflected in State.
	Seq int64
	// State is the raw Automerge binary document state. Opaque to the store.
	State     []byte
	CreatedAt time.Time
}

// DocumentFilter narrows the results of ListDocuments. Zero-valued fields are
// ignored.
type DocumentFilter struct {
	// CreatedBy, when non-empty, restricts results to documents created by the
	// given user identity.
	CreatedBy string
	// UpdatedSince, when non-zero, restricts results to documents updated at or
	// after the given time.
	UpdatedSince time.Time
	// Limit caps the number of returned documents. 0 means the store default.
	Limit int
	// Offset skips the given number of documents for pagination.
	Offset int
}

// AppendResult reports the outcome of committing an operation.
type AppendResult struct {
	// Seq is the sequence number assigned to the committed operation.
	Seq int64
	// Created is true when this operation implicitly created the document,
	// i.e. it was the first write and no metadata row previously existed. This
	// lets the sync layer fire on_document_created exactly once.
	Created bool
}

// Store is the persistence contract every backend implements. All methods are
// safe for concurrent use.
type Store interface {
	// CreateDocument explicitly creates document metadata. Returns
	// ErrDocumentExists if the id is already in use.
	CreateDocument(ctx context.Context, id, createdBy string) (*Document, error)

	// GetDocument returns document metadata, or ErrDocumentNotFound.
	GetDocument(ctx context.Context, id string) (*Document, error)

	// ListDocuments returns documents matching the filter, ordered by most
	// recently updated first.
	ListDocuments(ctx context.Context, filter DocumentFilter) ([]*Document, error)

	// DeleteDocument removes a document and all of its operations and
	// snapshots. Returns ErrDocumentNotFound if it does not exist.
	DeleteDocument(ctx context.Context, id string) error

	// AppendOp atomically assigns the next sequence number for the document,
	// persists the operation, and advances the document's LatestSeq and
	// UpdatedAt. The document is created on first write if it does not exist.
	AppendOp(ctx context.Context, op Operation) (AppendResult, error)

	// ListOps returns operations with Seq strictly greater than afterSeq, in
	// ascending sequence order, up to limit entries (0 = no limit). Use
	// afterSeq 0 to read from the beginning.
	ListOps(ctx context.Context, docID string, afterSeq int64, limit int) ([]*Operation, error)

	// SaveSnapshot persists a full-state snapshot for a document. If a snapshot
	// with the same sequence number exists it is replaced.
	SaveSnapshot(ctx context.Context, snap Snapshot) error

	// LatestSnapshot returns the highest-sequence snapshot for a document, or
	// ErrNoSnapshot if none exists.
	LatestSnapshot(ctx context.Context, docID string) (*Snapshot, error)

	// Ping verifies the store is reachable and healthy (for readiness checks).
	Ping(ctx context.Context) error

	// Close releases all resources held by the store.
	Close() error
}
