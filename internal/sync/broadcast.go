package sync

import "github.com/shaunakrananaware/OpenSyncCRDT/pkg/protocol"

// Broadcaster fans a committed change set out to the sessions subscribed to a
// document, skipping the session that produced it. It is implemented by the
// server's hub; the engine calls it after every committed op.
//
// Implementations must be non-blocking: a slow or stuck client must never stall
// the engine's write path. The hub achieves this with per-connection buffered
// send queues.
type Broadcaster interface {
	Broadcast(docID, exceptSession string, msg protocol.ServerSync)
}
