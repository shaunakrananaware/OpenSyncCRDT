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

	// observer, if set, is notified when a document gains its first local
	// subscriber or loses its last. In cluster mode it drives per-document
	// Redis channel subscription so a node only receives cross-node traffic
	// for documents it actually serves. Notifications fire outside the hub
	// lock so the observer can perform I/O without stalling the hub.
	observer SubscriptionObserver
}

// SubscriptionObserver is notified as documents become locally active or
// inactive. Implemented by the cluster node.
type SubscriptionObserver interface {
	OnDocActive(docID string)
	OnDocInactive(docID string)
}

func newHub(logger *slog.Logger) *hub {
	return &hub{
		logger: logger,
		docs:   make(map[string]map[*conn]struct{}),
	}
}

// setObserver installs the subscription observer. It must be called during
// wiring, before any connection is served.
func (h *hub) setObserver(o SubscriptionObserver) { h.observer = o }

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
// Documents left with no local subscribers are reported to the observer after
// the lock is released.
func (h *hub) deregister(c *conn) {
	var emptied []string
	h.mu.Lock()
	for docID := range c.subscriptions() {
		if set, ok := h.docs[docID]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.docs, docID)
				emptied = append(emptied, docID)
			}
		}
	}
	if h.active > 0 {
		h.active--
	}
	h.mu.Unlock()

	if h.observer != nil {
		for _, docID := range emptied {
			h.observer.OnDocInactive(docID)
		}
	}
}

// subscribe adds a connection to a document's subscriber set. When it is the
// first local subscriber for that document, the observer is notified after the
// lock is released.
func (h *hub) subscribe(docID string, c *conn) {
	h.mu.Lock()
	set, ok := h.docs[docID]
	if !ok {
		set = make(map[*conn]struct{})
		h.docs[docID] = set
	}
	set[c] = struct{}{}
	first := !ok
	h.mu.Unlock()

	if first && h.observer != nil {
		h.observer.OnDocActive(docID)
	}
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
