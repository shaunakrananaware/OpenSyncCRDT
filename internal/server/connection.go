package server

import (
	"context"
	"log/slog"
	stdsync "sync"
	"time"

	"github.com/coder/websocket"

	"github.com/opensynccrdt/opensynccrdt/internal/auth"
	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/metrics"
	syncengine "github.com/opensynccrdt/opensynccrdt/internal/sync"
	"github.com/opensynccrdt/opensynccrdt/pkg/protocol"
)

// sendQueueSize bounds a connection's outbound buffer. A client that cannot
// keep up is disconnected rather than allowed to stall the engine.
const sendQueueSize = 256

// conn is one WebSocket client connection.
type conn struct {
	ws       *websocket.Conn
	codec    protocol.Codec
	hub      *hub
	engine   *syncengine.Engine
	emitter  syncengine.Emitter
	metrics  *metrics.Registry
	logger   *slog.Logger
	limits   config.LimitsConfig
	identity auth.Identity

	remoteAddr  string
	connectedAt time.Time

	ctx       context.Context
	cancel    context.CancelFunc
	send      chan []byte
	done      chan struct{}
	closeOnce stdsync.Once

	subMu stdsync.Mutex
	subs  map[string]string // docID -> sessionID
}

func newConn(base context.Context, ws *websocket.Conn, codec protocol.Codec, deps serverDeps, identity auth.Identity, remoteAddr string) *conn {
	ctx, cancel := context.WithCancel(base)
	return &conn{
		ws:          ws,
		codec:       codec,
		hub:         deps.hub,
		engine:      deps.engine,
		emitter:     deps.emitter,
		metrics:     deps.metrics,
		logger:      deps.logger,
		limits:      deps.limits,
		identity:    identity,
		remoteAddr:  remoteAddr,
		connectedAt: time.Now(),
		ctx:         ctx,
		cancel:      cancel,
		send:        make(chan []byte, sendQueueSize),
		done:        make(chan struct{}),
		subs:        make(map[string]string),
	}
}

// serve runs the connection until it closes. It blocks in the read loop; the
// write and keepalive loops run in their own goroutines.
func (c *conn) serve() {
	if c.metrics != nil {
		c.metrics.ConnectionOpened()
		defer c.metrics.ConnectionClosed()
	}
	if lim := c.limits.MaxMessageSizeBytes; lim > 0 {
		c.ws.SetReadLimit(lim)
	}

	go c.writeLoop()
	go c.keepAlive()

	c.readLoop()

	// Read loop returned: the connection is finished. Tear down and fire the
	// disconnected event for every document this connection was subscribed to.
	c.close(websocket.StatusNormalClosure, "")
	c.hub.deregister(c)
	c.fireDisconnected()
}

func (c *conn) readLoop() {
	for {
		typ, data, err := c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		_ = typ
		in, derr := c.codec.Decode(data)
		if derr != nil {
			c.enqueue(protocol.NewError(protocol.CodeBadMessage, derr.Error()))
			continue
		}
		c.handle(in)
	}
}

func (c *conn) handle(in protocol.Inbound) {
	switch in.Type {
	case protocol.TypeSubscribe:
		c.handleSubscribe(in)
	case protocol.TypeSync:
		c.handleSync(in)
	case protocol.TypePing:
		c.enqueue(protocol.NewPong())
	default:
		c.enqueue(protocol.NewError(protocol.CodeBadMessage, "unknown message type"))
	}
}

func (c *conn) handleSubscribe(in protocol.Inbound) {
	if in.DocID == "" {
		c.enqueue(protocol.NewError(protocol.CodeBadMessage, "subscribe requires doc_id"))
		return
	}
	c.subMu.Lock()
	c.subs[in.DocID] = in.SessionID
	c.subMu.Unlock()

	c.hub.subscribe(in.DocID, c)
	c.fireConnected(in.DocID, in.SessionID)

	// Deliver everything the subscriber has not yet seen.
	err := c.engine.Replay(in.DocID, in.LastSeq, func(batch protocol.Replay) error {
		if c.metrics != nil {
			c.metrics.ReplayOps(len(batch.Ops))
		}
		return c.enqueueErr(batch)
	})
	if err != nil {
		c.logger.Warn("replay failed", "doc_id", in.DocID, "error", err)
		c.enqueue(protocol.NewError(protocol.CodeInternal, "replay failed"))
	}
}

func (c *conn) handleSync(in protocol.Inbound) {
	if in.DocID == "" {
		c.enqueue(protocol.NewError(protocol.CodeBadMessage, "sync requires doc_id"))
		return
	}
	// Ensure echo-suppression works even if the client syncs without an explicit
	// subscribe first.
	c.subMu.Lock()
	if _, ok := c.subs[in.DocID]; !ok {
		c.subs[in.DocID] = in.SessionID
	}
	c.subMu.Unlock()

	seq, err := c.engine.Submit(c.ctx, in.DocID, in.SessionID, c.identity.UserID, in.Payload)
	if err != nil {
		if c.metrics != nil {
			c.metrics.OperationError()
		}
		c.logger.Warn("submit failed", "doc_id", in.DocID, "session_id", in.SessionID, "error", err)
		c.enqueue(protocol.NewError(protocol.CodeApplyFailed, err.Error()))
		c.emit("on_sync_error", map[string]any{
			"doc_id":        in.DocID,
			"session_id":    in.SessionID,
			"error_code":    protocol.CodeApplyFailed,
			"error_message": err.Error(),
		})
		return
	}
	if c.metrics != nil {
		c.metrics.Operation(in.DocID)
	}
	c.enqueue(protocol.NewAck(seq))
}

func (c *conn) writeLoop() {
	msgType := websocket.MessageText
	if c.codec.Binary() {
		msgType = websocket.MessageBinary
	}
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.send:
			ctx := c.ctx
			var cancel context.CancelFunc
			if c.limits.WriteTimeout > 0 {
				ctx, cancel = context.WithTimeout(c.ctx, c.limits.WriteTimeout)
			}
			err := c.ws.Write(ctx, msgType, frame)
			if cancel != nil {
				cancel()
			}
			if err != nil {
				c.close(websocket.StatusInternalError, "write error")
				return
			}
		}
	}
}

// keepAlive sends periodic control pings; a missed pong (within PongTimeout)
// closes the connection.
func (c *conn) keepAlive() {
	interval := c.limits.PingInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			pctx := c.ctx
			var cancel context.CancelFunc
			if c.limits.PongTimeout > 0 {
				pctx, cancel = context.WithTimeout(c.ctx, c.limits.PongTimeout)
			}
			err := c.ws.Ping(pctx)
			if cancel != nil {
				cancel()
			}
			if err != nil {
				c.close(websocket.StatusPolicyViolation, "ping timeout")
				return
			}
		}
	}
}

// enqueue encodes and queues an outbound message, disconnecting a client whose
// send queue is full (it cannot keep up).
func (c *conn) enqueue(msg any) {
	_ = c.enqueueErr(msg)
}

func (c *conn) enqueueErr(msg any) error {
	frame, err := c.codec.Encode(msg)
	if err != nil {
		c.logger.Error("encode outbound", "error", err)
		return err
	}
	select {
	case <-c.done:
		return nil
	case c.send <- frame:
		return nil
	default:
		c.logger.Warn("slow client: send queue full, closing", "remote_addr", c.remoteAddr)
		c.close(websocket.StatusPolicyViolation, "send queue full")
		return context.Canceled
	}
}

func (c *conn) close(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		c.cancel()
		_ = c.ws.Close(code, reason)
	})
}

func (c *conn) sessionFor(docID string) string {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	return c.subs[docID]
}

func (c *conn) subscriptions() map[string]string {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	out := make(map[string]string, len(c.subs))
	for k, v := range c.subs {
		out[k] = v
	}
	return out
}

func (c *conn) fireConnected(docID, sessionID string) {
	c.emit("on_client_connected", map[string]any{
		"doc_id":      docID,
		"session_id":  sessionID,
		"user_id":     nullable(c.identity.UserID),
		"remote_addr": c.remoteAddr,
	})
}

func (c *conn) fireDisconnected() {
	dur := int64(time.Since(c.connectedAt).Seconds())
	for docID, sessionID := range c.subscriptions() {
		c.emit("on_client_disconnected", map[string]any{
			"doc_id":           docID,
			"session_id":       sessionID,
			"user_id":          nullable(c.identity.UserID),
			"duration_seconds": dur,
		})
	}
}

func (c *conn) emit(event string, fields map[string]any) {
	if c.emitter != nil {
		c.emitter.Emit(event, fields)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
