// Package protocol defines the WebSocket message envelopes exchanged between
// OpenSyncCRDT clients and the server, together with the codec that (de)encodes
// them on the wire.
//
// All real-time sync happens over a single WebSocket endpoint (/sync). Every
// message is a typed envelope discriminated by its "type" field. Automerge
// change sets and document state travel inside the Payload field as opaque
// bytes — in the JSON codec they are base64-encoded (Go marshals []byte as a
// base64 string automatically).
package protocol

// Type is the discriminant carried in every envelope's "type" field.
type Type string

const (
	// Client -> server.
	TypeSync      Type = "sync"      // a change set produced by a client
	TypeSubscribe Type = "subscribe" // join a document and request catch-up
	TypePing      Type = "ping"      // application-level keepalive

	// Server -> client.
	TypeReplay Type = "replay" // batched catch-up of missed ops
	TypeAck    Type = "ack"    // acknowledges a committed client sync
	TypeError  Type = "error"  // a problem processing a client message
	TypePong   Type = "pong"   // reply to a client ping
	// TypeSync is also used server -> client to fan out a peer's change.
)

// Error codes reported in an Error envelope's "code" field and surfaced in the
// on_sync_error webhook.
const (
	CodeBadMessage    = "bad_message"    // malformed or unexpected envelope
	CodeUnauthorized  = "unauthorized"   // auth rejected the action
	CodeNotSubscribed = "not_subscribed" // sync before subscribe
	CodeApplyFailed   = "apply_failed"   // change set could not be applied/stored
	CodeInternal      = "internal"       // unexpected server-side failure
)

// Inbound is the union of all client-to-server message shapes. The server reads
// each frame into an Inbound and dispatches on Type; fields not relevant to a
// given Type are left zero.
type Inbound struct {
	Type      Type   `json:"type"`
	DocID     string `json:"doc_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// Payload is a raw Automerge binary change set (base64 in JSON).
	Payload []byte `json:"payload,omitempty"`
	// LastSeq is the highest sequence number a subscriber already has; the
	// server replays everything after it.
	LastSeq int64 `json:"last_seq,omitempty"`
}

// ServerSync fans a peer's committed change set out to other subscribers, and
// mirrors the client's own change back is avoided by the hub (the originating
// session is skipped).
type ServerSync struct {
	Type        Type   `json:"type"`
	DocID       string `json:"doc_id"`
	FromSession string `json:"from_session"`
	Seq         int64  `json:"seq"`
	Payload     []byte `json:"payload"`
}

// ReplayOp is a single operation inside a Replay batch.
type ReplayOp struct {
	Seq         int64  `json:"seq"`
	FromSession string `json:"from_session"`
	Payload     []byte `json:"payload"`
}

// Replay delivers a batch of missed operations to a catching-up subscriber.
// Large histories are split across multiple batches; Done is true on the last.
type Replay struct {
	Type         Type       `json:"type"`
	DocID        string     `json:"doc_id"`
	Ops          []ReplayOp `json:"ops"`
	BatchSeq     int        `json:"batch_seq"`
	TotalBatches int        `json:"total_batches"`
	Done         bool       `json:"done"`
}

// Ack confirms a client's sync message was committed at the given sequence.
type Ack struct {
	Type Type  `json:"type"`
	Seq  int64 `json:"seq"`
}

// Error reports a problem processing a client message.
type Error struct {
	Type    Type   `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Pong replies to a client Ping.
type Pong struct {
	Type Type `json:"type"`
}

// Constructors keep the Type field correct at every call site.

func NewServerSync(docID, fromSession string, seq int64, payload []byte) ServerSync {
	return ServerSync{Type: TypeSync, DocID: docID, FromSession: fromSession, Seq: seq, Payload: payload}
}

func NewReplay(docID string, ops []ReplayOp, batchSeq, totalBatches int, done bool) Replay {
	return Replay{Type: TypeReplay, DocID: docID, Ops: ops, BatchSeq: batchSeq, TotalBatches: totalBatches, Done: done}
}

func NewAck(seq int64) Ack { return Ack{Type: TypeAck, Seq: seq} }

func NewError(code, message string) Error {
	return Error{Type: TypeError, Code: code, Message: message}
}

func NewPong() Pong { return Pong{Type: TypePong} }
