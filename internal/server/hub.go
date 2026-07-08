package server

import (
	"log/slog"
	stdsync "sync"

	"github.com/opensynccrdt/opensynccrdt/pkg/protocol"
)

// hub tracks live connections and routes broadcasts to the sessions subscribed
// to each document. It implements sync.Broadcaster.
//
// Broadcast never blocks on a slow client: each connection has a bounded send
// queue, and a client whose queue is full is disconnected rather than allowed
// to stall the engine's write path.
type hub struct {
	logger *slog.Logger

	mu     stdsync.RWMutex
	docs   map[string]map[*conn]struct{} // docID -> subscribed connections
	active int                           // current connection count
}

func newHub(logger *slog.Logger) *hub {
	return &hub{
		logger: logger,
		docs:   make(map[string]map[*conn]struct{}),
	}
}

// tryRegister reserves a connection slot if the active count is below max (0 =
// unlimited), incrementing the count. It reports whether a slot was granted.
func (h *hub) tryRegister(max int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if max > 0 && h.active >= max {
		return false
	}
	h.active++
	return true
}

// release returns a reserved slot without any document cleanup. Used when a
// connection fails between reservation and serving.
func (h *hub) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active > 0 {
		h.active--
	}
}

// deregister removes a connection from all documents and decrements the count.
func (h *hub) deregister(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for docID := range c.subscriptions() {
		if set, ok := h.docs[docID]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.docs, docID)
			}
		}
	}
	if h.active > 0 {
		h.active--
	}
}

// subscribe adds a connection to a document's subscriber set.
func (h *hub) subscribe(docID string, c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.docs[docID]
	if !ok {
		set = make(map[*conn]struct{})
		h.docs[docID] = set
	}
	set[c] = struct{}{}
}

// Broadcast fans a committed change out to every session subscribed to docID
// except the originating session.
func (h *hub) Broadcast(docID, exceptSession string, msg protocol.ServerSync) {
	h.mu.RLock()
	set := h.docs[docID]
	targets := make([]*conn, 0, len(set))
	for c := range set {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		if c.sessionFor(docID) == exceptSession {
			continue // don't echo a change back to its author
		}
		c.enqueue(msg)
	}
}
