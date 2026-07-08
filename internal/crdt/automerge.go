// Package crdt wraps the official automerge-go library. The rest of the engine
// treats Automerge change sets and document state as opaque bytes; this package
// is the only place that understands them.
//
// A Doc is the server-side materialisation of one document's Automerge state.
// It is rebuilt on demand from the latest snapshot plus any newer ops, kept up
// to date as new change sets arrive, and periodically re-snapshotted.
package crdt

import (
	"fmt"
	"sync"

	"github.com/automerge/automerge-go"
)

// Doc is a concurrency-safe wrapper around an Automerge document. All access is
// serialized by an internal mutex, since the underlying automerge-go document
// is not safe for concurrent use.
type Doc struct {
	mu  sync.Mutex
	doc *automerge.Doc
}

// New returns an empty document.
func New() *Doc {
	return &Doc{doc: automerge.New()}
}

// Load reconstructs a document from a full-state snapshot (as produced by Save).
func Load(state []byte) (*Doc, error) {
	d, err := automerge.Load(state)
	if err != nil {
		return nil, fmt.Errorf("load automerge state: %w", err)
	}
	return &Doc{doc: d}, nil
}

// Rehydrate rebuilds a document from an optional snapshot followed by an ordered
// list of subsequent change-set payloads (the op log tail). A nil/empty
// snapshot starts from an empty document.
func Rehydrate(snapshot []byte, ops [][]byte) (*Doc, error) {
	var d *Doc
	if len(snapshot) > 0 {
		var err error
		if d, err = Load(snapshot); err != nil {
			return nil, err
		}
	} else {
		d = New()
	}
	for i, op := range ops {
		if err := d.Apply(op); err != nil {
			return nil, fmt.Errorf("replay op %d: %w", i, err)
		}
	}
	return d, nil
}

// Apply merges an incoming Automerge change-set payload into the document. The
// payload is the raw binary a client produced with SaveIncremental (or an
// equivalent). Applying an already-known change is a no-op.
func (d *Doc) Apply(payload []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.doc.LoadIncremental(payload); err != nil {
		return fmt.Errorf("apply change set: %w", err)
	}
	return nil
}

// Save returns a full-state snapshot of the document suitable for storage and
// for reconstructing the document later via Load.
func (d *Doc) Save() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.doc.Save()
}

// Heads returns the current head change hashes as hex strings. Divergent
// (concurrent) history shows up as more than one head.
func (d *Doc) Heads() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	heads := d.doc.Heads()
	out := make([]string, len(heads))
	for i, h := range heads {
		out[i] = h.String()
	}
	return out
}
