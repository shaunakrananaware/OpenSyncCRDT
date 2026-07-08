package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONDecodeSync(t *testing.T) {
	c := JSONCodec()
	// payload "hello" base64 == "aGVsbG8="
	raw := []byte(`{"type":"sync","doc_id":"d1","session_id":"s1","payload":"aGVsbG8="}`)
	in, err := c.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if in.Type != TypeSync || in.DocID != "d1" || in.SessionID != "s1" {
		t.Fatalf("decoded = %+v", in)
	}
	if !bytes.Equal(in.Payload, []byte("hello")) {
		t.Fatalf("payload = %q, want hello", in.Payload)
	}
}

func TestJSONDecodeSubscribe(t *testing.T) {
	in, err := JSONCodec().Decode([]byte(`{"type":"subscribe","doc_id":"d","session_id":"s","last_seq":42}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if in.Type != TypeSubscribe || in.LastSeq != 42 {
		t.Fatalf("decoded = %+v", in)
	}
}

func TestJSONDecodeMissingType(t *testing.T) {
	if _, err := JSONCodec().Decode([]byte(`{"doc_id":"d"}`)); err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestEncodeServerSyncRoundTrip(t *testing.T) {
	c := JSONCodec()
	msg := NewServerSync("d1", "s2", 7, []byte("world"))
	b, err := c.Encode(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["type"] != "sync" || got["from_session"] != "s2" || got["seq"].(float64) != 7 {
		t.Fatalf("encoded = %s", b)
	}
	if got["payload"] != "d29ybGQ=" { // base64("world")
		t.Fatalf("payload = %v", got["payload"])
	}
}

func TestEncodeAckErrorPong(t *testing.T) {
	c := JSONCodec()
	for _, tc := range []struct {
		msg  any
		want string
	}{
		{NewAck(9), `"type":"ack"`},
		{NewError(CodeApplyFailed, "boom"), `"code":"apply_failed"`},
		{NewPong(), `"type":"pong"`},
	} {
		b, err := c.Encode(tc.msg)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if !bytes.Contains(b, []byte(tc.want)) {
			t.Errorf("encoded %s, want contains %s", b, tc.want)
		}
	}
}

func TestSelectCodec(t *testing.T) {
	if _, echo := SelectCodec([]string{SubprotocolJSON}); echo != SubprotocolJSON {
		t.Errorf("json offer echo = %q", echo)
	}
	if c, echo := SelectCodec([]string{SubprotocolMsgpack}); c == nil || echo != "" {
		t.Errorf("msgpack-only falls back to json with no echo, got echo=%q", echo)
	}
	if c, _ := SelectCodec(nil); c == nil {
		t.Error("nil offer should still yield a codec")
	}
}
