// Package sync is the transport-agnostic core of OpenSyncCRDT. It applies
// incoming Automerge change sets to per-document server-side CRDT state,
// persists them to the op log, snapshots periodically, and fans committed
// changes out to other connected sessions.
//
// The package knows nothing about WebSockets or HTTP. Connection management
// lives in internal/server, which drives the Engine and implements Broadcaster.
package sync

import (
	"context"
	"fmt"
	"log/slog"
	stdsync "sync"
	"time"

	"github.com/opensynccrdt/opensynccrdt/internal/crdt"
	"github.com/opensynccrdt/opensynccrdt/internal/storage"
	"github.com/opensynccrdt/opensynccrdt/pkg/protocol"
)

// Emitter dispatches an outbound webhook event with the given event-specific
// fields. Implemented by the webhook dispatcher; may be nil (events disabled).
type Emitter interface {
	Emit(event string, fields map[string]any)
}

// Options configures an Engine.
type Options struct {
	SnapshotInterval int
	Resolver         *crdt.Resolver
	ResolverTimeout  time.Duration
	// ReplayBatchSize caps how many ops are packed into a single replay batch.
	ReplayBatchSize int
	Emitter         Emitter
	Logger          *slog.Logger
}

// Engine coordinates all document state.
type Engine struct {
	store           storage.Store
	broadcaster     Broadcaster
	emitter         Emitter
	resolver        *crdt.Resolver
	resolverTimeout time.Duration
	snapshotEvery   int
	replayBatch     int
	logger          *slog.Logger

	mu   stdsync.Mutex
	docs map[string]*docState
}

// docState is the cached in-memory CRDT for one document plus a lock that
// serializes writes to it, so sequence assignment and broadcast order agree.
type docState struct {
	mu  stdsync.Mutex
	doc *crdt.Doc
}

const (
	defaultReplayBatchSize = 500
	eventDocumentCreated   = "on_document_created"
	eventDocumentUpdated   = "on_document_updated"
	eventSyncError         = "on_sync_error"
)

// NewEngine constructs an Engine. broadcaster must not be nil; emitter and
// resolver may be nil.
func NewEngine(store storage.Store, broadcaster Broadcaster, opts Options) *Engine {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	batch := opts.ReplayBatchSize
	if batch <= 0 {
		batch = defaultReplayBatchSize
	}
	return &Engine{
		store:           store,
		broadcaster:     broadcaster,
		emitter:         opts.Emitter,
		resolver:        opts.Resolver,
		resolverTimeout: opts.ResolverTimeout,
		snapshotEvery:   opts.SnapshotInterval,
		replayBatch:     batch,
		logger:          logger,
		docs:            make(map[string]*docState),
	}
}

// Submit applies a client's change set to a document, commits it to the op log,
// snapshots if due, fires the relevant webhooks, and broadcasts the change to
// other subscribers. It returns the committed sequence number for the client's
// ack.
func (e *Engine) Submit(ctx context.Context, docID, sessionID, userID string, payload []byte) (int64, error) {
	if docID == "" {
		return 0, fmt.Errorf("submit: empty doc_id")
	}
	if len(payload) == 0 {
		return 0, fmt.Errorf("submit: empty payload")
	}

	st, err := e.state(docID)
	if err != nil {
		return 0, err
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if err := e.applyChange(ctx, docID, st, payload); err != nil {
		return 0, err
	}

	seq, err := e.store.AppendOp(docID, payload, sessionID)
	if err != nil {
		return 0, fmt.Errorf("append op: %w", err)
	}

	// on_document_created fires exactly once, on the first committed op.
	if seq == 1 {
		e.emit(eventDocumentCreated, map[string]any{
			"doc_id":  docID,
			"user_id": nullable(userID),
		})
	}
	e.emit(eventDocumentUpdated, map[string]any{
		"doc_id":     docID,
		"seq":        seq,
		"user_id":    nullable(userID),
		"session_id": sessionID,
	})

	// Periodic snapshot so restart does not replay the entire op log.
	if e.snapshotEvery > 0 && seq%int64(e.snapshotEvery) == 0 {
		if err := e.store.SaveSnapshot(docID, st.doc.Save(), seq); err != nil {
			e.logger.Warn("save snapshot failed", "doc_id", docID, "seq", seq, "error", err)
		}
	}

	e.broadcaster.Broadcast(docID, sessionID, protocol.NewServerSync(docID, sessionID, seq, payload))
	return seq, nil
}

// applyChange merges payload into the document. With no resolver configured
// (the default), Automerge resolves any concurrency automatically. When a
// resolver is configured and the change introduces divergent history, the
// developer's endpoint is consulted; on any failure we keep Automerge's
// automatic result and log a warning.
func (e *Engine) applyChange(ctx context.Context, docID string, st *docState, payload []byte) error {
	if e.resolver == nil {
		return st.doc.Apply(payload)
	}

	stateBefore := st.doc.Save()
	headsBefore := st.doc.Heads()
	if err := st.doc.Apply(payload); err != nil {
		return err
	}
	// More than one head means the incoming change was concurrent with existing
	// history rather than a linear extension.
	if len(headsBefore) == 0 || len(st.doc.Heads()) <= 1 {
		return nil
	}

	rctx := ctx
	if e.resolverTimeout > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, e.resolverTimeout)
		defer cancel()
	}
	// change_a: the state prior to this change; change_b: the incoming change.
	resolved, err := e.resolver.Resolve(rctx, docID, stateBefore, payload, stateBefore)
	if err != nil {
		e.logger.Warn("conflict resolver failed; using automatic merge",
			"doc_id", docID, "error", err)
		e.emit(eventSyncError, map[string]any{
			"doc_id":        docID,
			"session_id":    "",
			"error_code":    "resolver_failed",
			"error_message": err.Error(),
		})
		return nil
	}
	newDoc, err := crdt.Load(resolved)
	if err != nil {
		e.logger.Warn("resolver returned unloadable state; using automatic merge",
			"doc_id", docID, "error", err)
		return nil
	}
	st.doc = newDoc
	return nil
}

// state returns the cached docState for a document, loading it from storage
// (latest snapshot plus newer ops) on first access.
func (e *Engine) state(docID string) (*docState, error) {
	e.mu.Lock()
	if st, ok := e.docs[docID]; ok {
		e.mu.Unlock()
		return st, nil
	}
	e.mu.Unlock()

	doc, err := e.loadDoc(docID)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	// Another goroutine may have loaded it while we were reading storage.
	if st, ok := e.docs[docID]; ok {
		return st, nil
	}
	st := &docState{doc: doc}
	e.docs[docID] = st
	return st, nil
}

// loadDoc reconstructs a document's CRDT state from its latest snapshot and any
// ops committed after it.
func (e *Engine) loadDoc(docID string) (*crdt.Doc, error) {
	snapshot, atSeq, err := e.store.GetSnapshot(docID)
	if err != nil {
		if err == storage.ErrNoSnapshot {
			snapshot, atSeq = nil, 0
		} else {
			return nil, fmt.Errorf("load snapshot: %w", err)
		}
	}
	ops, err := e.store.GetOpsSince(docID, atSeq)
	if err != nil {
		return nil, fmt.Errorf("load ops: %w", err)
	}
	payloads := make([][]byte, len(ops))
	for i, op := range ops {
		payloads[i] = op.Payload
	}
	return crdt.Rehydrate(snapshot, payloads)
}

// Snapshot forces a full-state snapshot of a document at its current latest
// sequence and returns that sequence. Used by the manual-snapshot API.
func (e *Engine) Snapshot(docID string) (int64, error) {
	st, err := e.state(docID)
	if err != nil {
		return 0, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	seq, err := e.store.GetLatestSeq(docID)
	if err != nil {
		return 0, err
	}
	if err := e.store.SaveSnapshot(docID, st.doc.Save(), seq); err != nil {
		return 0, fmt.Errorf("save snapshot: %w", err)
	}
	return seq, nil
}

// Export returns the full Automerge state of a document for the export API.
func (e *Engine) Export(docID string) ([]byte, error) {
	st, err := e.state(docID)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.doc.Save(), nil
}

// Forget drops a document's cached CRDT state (e.g. after deletion) so a later
// access reloads from storage.
func (e *Engine) Forget(docID string) {
	e.mu.Lock()
	delete(e.docs, docID)
	e.mu.Unlock()
}

// Store exposes the underlying storage for the management API and health checks.
func (e *Engine) Store() storage.Store { return e.store }

func (e *Engine) emit(event string, fields map[string]any) {
	if e.emitter == nil {
		return
	}
	e.emitter.Emit(event, fields)
}

// nullable renders an empty identity as a JSON null (per the spec's
// "string or null" payload fields).
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
