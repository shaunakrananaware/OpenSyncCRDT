// Package cluster implements optional multi-node clustering over Redis. When
// CLUSTER_MODE=true, operations committed on one node are published to a Redis
// pub/sub channel per document and re-broadcast to local connections on every
// other node that serves that document, so devices connected to different nodes
// stay in sync. Nodes also register themselves in Redis with a heartbeat so
// /api/v1/nodes can list the live cluster.
//
// The package plugs into the sync engine as a sync.Broadcaster: the engine
// calls Broadcast after every committed op, which delivers locally and then
// fans the op out across the cluster. Incoming cluster messages are delivered
// straight to the local hub, never re-published, so there are no loops.
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	syncengine "github.com/opensynccrdt/opensynccrdt/internal/sync"
	"github.com/opensynccrdt/opensynccrdt/pkg/protocol"
)

// Node-registry heartbeat parameters are fixed by the specification: each node
// refreshes its key every heartbeatInterval, and the key expires after nodeTTL
// if a node dies without refreshing.
const (
	nodeTTL           = 30 * time.Second
	heartbeatInterval = 10 * time.Second
	// opTimeout bounds individual Redis commands issued off the hot path so a
	// stalled Redis never blocks a connection or the engine indefinitely.
	opTimeout = 5 * time.Second
)

func ttlDuration(ttlSeconds int) time.Duration {
	return time.Duration(ttlSeconds) * time.Second
}

// NodeInfo describes a live cluster node as stored in the Redis registry.
type NodeInfo struct {
	ID        string    `json:"id"`
	Addr      string    `json:"addr"`
	StartedAt time.Time `json:"started_at"`
}

// wireMsg is the cross-node encoding of a committed operation. Origin lets a
// node ignore the echo of its own publishes (a publisher subscribed to the same
// channel receives its own messages).
type wireMsg struct {
	Origin      string `json:"origin"`
	DocID       string `json:"doc_id"`
	FromSession string `json:"from_session"`
	Seq         int64  `json:"seq"`
	Payload     []byte `json:"payload"`
}

// Options configures a Node.
type Options struct {
	Ctx      context.Context
	RedisURL string
	NodeID   string
	Addr     string
	// Local delivers cluster-originated operations to this node's own
	// connections. It is the server hub.
	Local  syncengine.Broadcaster
	Logger *slog.Logger
}

// Node is a single OpenSyncCRDT instance's view of the cluster. It implements
// syncengine.Broadcaster (the engine's fan-out target) and the server's
// subscription observer (first/last local subscriber per document).
type Node struct {
	bus       *redisBus
	local     syncengine.Broadcaster
	id        string
	addr      string
	startedAt time.Time
	logger    *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
}

// New connects to Redis, registers this node, and starts the heartbeat and the
// pub/sub receive loop. Call Close to leave the cluster.
func New(opts Options) (*Node, error) {
	base := opts.Ctx
	if base == nil {
		base = context.Background()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	dialCtx, cancelDial := context.WithTimeout(base, opTimeout)
	bus, err := dialRedis(dialCtx, opts.RedisURL)
	cancelDial()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(base)
	n := &Node{
		bus:       bus,
		local:     opts.Local,
		id:        opts.NodeID,
		addr:      opts.Addr,
		startedAt: time.Now().UTC(),
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
	}

	// Register immediately so the node appears in /api/v1/nodes without waiting
	// for the first heartbeat tick.
	if err := n.register(ctx); err != nil {
		cancel()
		_ = bus.close()
		return nil, fmt.Errorf("register node: %w", err)
	}

	go n.heartbeatLoop()
	go n.receiveLoop()

	logger.Info("cluster node joined", "node_id", n.id, "addr", n.addr)
	return n, nil
}

// Broadcast delivers a committed op to local subscribers and publishes it to the
// document's cluster channel so other nodes can deliver it to theirs. It is the
// engine's Broadcaster in cluster mode.
func (n *Node) Broadcast(docID, exceptSession string, msg protocol.ServerSync) {
	// Local delivery first: same behaviour as single-node.
	n.local.Broadcast(docID, exceptSession, msg)

	data, err := json.Marshal(wireMsg{
		Origin:      n.id,
		DocID:       docID,
		FromSession: msg.FromSession,
		Seq:         msg.Seq,
		Payload:     msg.Payload,
	})
	if err != nil {
		n.logger.Error("cluster: marshal op", "doc_id", docID, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, opTimeout)
	defer cancel()
	if err := n.bus.publish(ctx, docChannel(docID), data); err != nil {
		n.logger.Warn("cluster: publish op", "doc_id", docID, "error", err)
	}
}

// OnDocActive subscribes to a document's cluster channel when the first local
// connection for that document appears.
func (n *Node) OnDocActive(docID string) {
	ctx, cancel := context.WithTimeout(n.ctx, opTimeout)
	defer cancel()
	if err := n.bus.subscribe(ctx, docChannel(docID)); err != nil {
		n.logger.Warn("cluster: subscribe", "doc_id", docID, "error", err)
	}
}

// OnDocInactive unsubscribes from a document's cluster channel when the last
// local connection for that document goes away.
func (n *Node) OnDocInactive(docID string) {
	ctx, cancel := context.WithTimeout(n.ctx, opTimeout)
	defer cancel()
	if err := n.bus.unsubscribe(ctx, docChannel(docID)); err != nil {
		n.logger.Warn("cluster: unsubscribe", "doc_id", docID, "error", err)
	}
}

// Nodes returns every currently-alive node from the Redis registry.
func (n *Node) Nodes(ctx context.Context) ([]NodeInfo, error) {
	values, err := n.bus.listNodeValues(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]NodeInfo, 0, len(values))
	for _, v := range values {
		var info NodeInfo
		if err := json.Unmarshal(v, &info); err != nil {
			n.logger.Warn("cluster: bad node registry entry", "error", err)
			continue
		}
		nodes = append(nodes, info)
	}
	return nodes, nil
}

// Close deregisters the node and tears down the Redis connection.
func (n *Node) Close() error {
	n.cancel()
	// Best-effort deregister with a fresh short-lived context, since n.ctx is
	// now cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	if err := n.bus.deleteNode(ctx, nodeKey(n.id)); err != nil {
		n.logger.Warn("cluster: deregister", "node_id", n.id, "error", err)
	}
	cancel()
	return n.bus.close()
}

// register writes this node's registry key with the node TTL.
func (n *Node) register(ctx context.Context) error {
	value, err := json.Marshal(NodeInfo{
		ID:        n.id,
		Addr:      n.addr,
		StartedAt: n.startedAt,
	})
	if err != nil {
		return err
	}
	return n.bus.setNode(ctx, nodeKey(n.id), value, int(nodeTTL/time.Second))
}

// heartbeatLoop refreshes the node registry key before it expires.
func (n *Node) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(n.ctx, opTimeout)
			if err := n.register(ctx); err != nil {
				n.logger.Warn("cluster: heartbeat", "node_id", n.id, "error", err)
			}
			cancel()
		}
	}
}

// receiveLoop delivers cluster-originated operations to local connections. A
// node's own publishes (Origin == n.id) are ignored, since they were already
// delivered locally in Broadcast.
func (n *Node) receiveLoop() {
	messages := n.bus.messages()
	for {
		select {
		case <-n.ctx.Done():
			return
		case m, ok := <-messages:
			if !ok {
				return
			}
			var wm wireMsg
			if err := json.Unmarshal([]byte(m.Payload), &wm); err != nil {
				n.logger.Warn("cluster: bad message", "channel", m.Channel, "error", err)
				continue
			}
			if wm.Origin == n.id {
				continue // echo of our own publish
			}
			n.local.Broadcast(
				wm.DocID,
				wm.FromSession,
				protocol.NewServerSync(wm.DocID, wm.FromSession, wm.Seq, wm.Payload),
			)
		}
	}
}
