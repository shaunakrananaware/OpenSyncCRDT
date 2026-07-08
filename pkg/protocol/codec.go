package protocol

import (
	"encoding/json"
	"fmt"
)

// WebSocket subprotocols negotiated via the Sec-WebSocket-Protocol header.
const (
	// SubprotocolJSON is the default, human-debuggable codec.
	SubprotocolJSON = "opensync-json"
	// SubprotocolMsgpack is the optional binary codec (not yet implemented;
	// reserved so clients can request it and negotiation can fall back).
	SubprotocolMsgpack = "opensync-msgpack"
)

// Codec encodes outbound envelopes and decodes inbound frames. The wire format
// is negotiated once at connection time via the WebSocket subprotocol.
type Codec interface {
	// Subprotocol reports the negotiated Sec-WebSocket-Protocol value.
	Subprotocol() string
	// Binary reports whether frames are binary (true) or text (false).
	Binary() bool
	// Decode parses an inbound client frame.
	Decode(data []byte) (Inbound, error)
	// Encode serializes an outbound envelope (ServerSync, Replay, Ack, ...).
	Encode(msg any) ([]byte, error)
}

// jsonCodec implements Codec with newline-free JSON text frames. []byte payloads
// are base64-encoded by encoding/json automatically.
type jsonCodec struct{}

// JSONCodec is the default codec.
func JSONCodec() Codec { return jsonCodec{} }

func (jsonCodec) Subprotocol() string { return SubprotocolJSON }
func (jsonCodec) Binary() bool        { return false }

func (jsonCodec) Decode(data []byte) (Inbound, error) {
	var in Inbound
	if err := json.Unmarshal(data, &in); err != nil {
		return Inbound{}, fmt.Errorf("decode inbound: %w", err)
	}
	if in.Type == "" {
		return Inbound{}, fmt.Errorf("decode inbound: missing type")
	}
	return in, nil
}

func (jsonCodec) Encode(msg any) ([]byte, error) {
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode outbound: %w", err)
	}
	return b, nil
}

// SelectCodec negotiates a codec from the client's offered subprotocols. The
// server only implements JSON today; a client that offers msgpack (or offers
// nothing) still gets a working JSON connection. The returned string is the
// subprotocol the server should echo back in the handshake, or "" to omit it.
func SelectCodec(offered []string) (Codec, string) {
	for _, p := range offered {
		if p == SubprotocolJSON {
			return JSONCodec(), SubprotocolJSON
		}
	}
	// Default: JSON. Echo the subprotocol only if the client explicitly asked
	// for opensync-json (handled above); otherwise omit it for compatibility
	// with bare clients.
	return JSONCodec(), ""
}
