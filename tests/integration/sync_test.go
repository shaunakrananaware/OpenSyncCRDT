// Package integration exercises the full stack end to end: the embedded engine
// behind a real HTTP server, spoken to over real WebSocket connections.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/automerge/automerge-go"
	"github.com/coder/websocket"

	"github.com/shaunakrananaware/OpenSyncCRDT/pkg/engine"
	"github.com/shaunakrananaware/OpenSyncCRDT/pkg/protocol"
)

func newServer(t *testing.T) (*httptest.Server, *engine.Engine) {
	t.Helper()
	eng, err := engine.New(engine.Config{
		Storage: engine.SQLiteStorage(t.TempDir()),
		Auth:    engine.NoAuth(),
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	ts := httptest.NewServer(eng.Handler())
	t.Cleanup(func() {
		ts.Close()
		eng.Close()
	})
	return ts, eng
}

func wsURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/sync"
}

func dial(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts), &websocket.DialOptions{
		Subprotocols: []string{protocol.SubprotocolJSON},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close(websocket.StatusNormalClosure, "") })
	return c
}

func writeJSON(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, _ := json.Marshal(v)
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readUntil reads messages until one with the wanted type arrives (or timeout),
// returning the raw JSON. Other message types are skipped.
func readUntil(t *testing.T, c *websocket.Conn, want protocol.Type) map[string]json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read (want %s): %v", want, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		var typ protocol.Type
		_ = json.Unmarshal(m["type"], &typ)
		if typ == want {
			return m
		}
	}
}

// makeChange produces the incremental Automerge change bytes a client would send
// after mutating a document that started from base (nil = empty).
func makeChange(t *testing.T, base []byte, mutate func(*automerge.Doc)) []byte {
	t.Helper()
	var d *automerge.Doc
	if len(base) > 0 {
		var err error
		if d, err = automerge.Load(base); err != nil {
			t.Fatalf("load base: %v", err)
		}
	} else {
		d = automerge.New()
	}
	mutate(d)
	if _, err := d.Commit("test change"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return d.SaveIncremental()
}

// TestTwoClientsSync is success criterion #2: two clients connect, one sends an
// operation, the other receives it in real time.
func TestTwoClientsSync(t *testing.T) {
	ts, _ := newServer(t)

	a := dial(t, ts)
	b := dial(t, ts)

	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSubscribe, DocID: "doc1", SessionID: "sess-a"})
	writeJSON(t, b, protocol.Inbound{Type: protocol.TypeSubscribe, DocID: "doc1", SessionID: "sess-b"})

	// Both get a terminal (empty) replay batch since the doc is new.
	_ = readUntil(t, a, protocol.TypeReplay)
	_ = readUntil(t, b, protocol.TypeReplay)

	change := makeChange(t, nil, func(d *automerge.Doc) { _ = d.RootMap().Set("title", "hello") })
	writeJSON(t, a, protocol.Inbound{
		Type:      protocol.TypeSync,
		DocID:     "doc1",
		SessionID: "sess-a",
		Payload:   change,
	})

	// A is acked at seq 1.
	ackMsg := readUntil(t, a, protocol.TypeAck)
	var seq int64
	_ = json.Unmarshal(ackMsg["seq"], &seq)
	if seq != 1 {
		t.Fatalf("ack seq = %d, want 1", seq)
	}

	// B receives the fanned-out change and can materialize the value.
	syncMsg := readUntil(t, b, protocol.TypeSync)
	var payload []byte
	_ = json.Unmarshal(syncMsg["payload"], &payload)
	var fromSession string
	_ = json.Unmarshal(syncMsg["from_session"], &fromSession)
	if fromSession != "sess-a" {
		t.Errorf("from_session = %q, want sess-a", fromSession)
	}

	doc := automerge.New()
	if err := doc.LoadIncremental(payload); err != nil {
		t.Fatalf("apply broadcast payload: %v", err)
	}
	v, _ := doc.RootMap().Get("title")
	if v.Str() != "hello" {
		t.Fatalf("title = %v, want hello", v)
	}
}

// TestReconnectReplay is success criterion #3: a client that was offline while
// changes happened catches up on reconnect with no data loss.
func TestReconnectReplay(t *testing.T) {
	ts, _ := newServer(t)

	// A writes two ops while nobody else is listening.
	a := dial(t, ts)
	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSubscribe, DocID: "doc2", SessionID: "sess-a"})
	_ = readUntil(t, a, protocol.TypeReplay)

	c1 := makeChange(t, nil, func(d *automerge.Doc) { _ = d.RootMap().Set("k", int64(1)) })
	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSync, DocID: "doc2", SessionID: "sess-a", Payload: c1})
	_ = readUntil(t, a, protocol.TypeAck)

	// Second change based on the first.
	base := automerge.New()
	_ = base.LoadIncremental(c1)
	c2 := makeChange(t, base.Save(), func(d *automerge.Doc) { _ = d.RootMap().Set("k", int64(2)) })
	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSync, DocID: "doc2", SessionID: "sess-a", Payload: c2})
	_ = readUntil(t, a, protocol.TypeAck)

	// A late client subscribes from scratch and must receive both ops in order.
	late := dial(t, ts)
	writeJSON(t, late, protocol.Inbound{Type: protocol.TypeSubscribe, DocID: "doc2", SessionID: "sess-late", LastSeq: 0})

	replay := readUntil(t, late, protocol.TypeReplay)
	var ops []protocol.ReplayOp
	_ = json.Unmarshal(replay["ops"], &ops)
	if len(ops) != 2 {
		t.Fatalf("replay ops = %d, want 2", len(ops))
	}
	if ops[0].Seq != 1 || ops[1].Seq != 2 {
		t.Fatalf("replay seqs = %d,%d want 1,2", ops[0].Seq, ops[1].Seq)
	}

	// Applying the replayed ops in order reconstructs the final value.
	doc := automerge.New()
	for _, op := range ops {
		if err := doc.LoadIncremental(op.Payload); err != nil {
			t.Fatalf("apply replay op %d: %v", op.Seq, err)
		}
	}
	v, _ := doc.RootMap().Get("k")
	if v.Int64() != 2 {
		t.Fatalf("replayed k = %v, want 2", v)
	}
}

// TestRESTLifecycle exercises the management API.
func TestRESTLifecycle(t *testing.T) {
	ts, _ := newServer(t)
	client := ts.Client()

	// Health and readiness.
	for _, path := range []string{"/health", "/ready"} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}

	// Create a document.
	resp, err := client.Post(ts.URL+"/api/v1/docs", "application/json",
		strings.NewReader(`{"doc_id":"api-doc","metadata":{"owner":"alice"}}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}

	// List documents.
	resp2, err := client.Get(ts.URL + "/api/v1/docs")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp2.Body.Close()
	var listing struct {
		Documents []struct {
			ID       string            `json:"id"`
			Metadata map[string]string `json:"metadata"`
		} `json:"documents"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&listing); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listing.Documents) != 1 || listing.Documents[0].ID != "api-doc" {
		t.Fatalf("listing = %+v", listing.Documents)
	}
	if listing.Documents[0].Metadata["owner"] != "alice" {
		t.Errorf("metadata not returned: %+v", listing.Documents[0].Metadata)
	}

	// Metrics endpoint returns Prometheus text.
	resp3, err := client.Get(ts.URL + "/api/v1/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("metrics status = %d", resp3.StatusCode)
	}

	// Delete the document.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/docs/api-doc", nil)
	resp4, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("delete status = %d, want 200", resp4.StatusCode)
	}

	// Now missing.
	resp5, _ := client.Get(ts.URL + "/api/v1/docs/api-doc")
	resp5.Body.Close()
	if resp5.StatusCode != http.StatusNotFound {
		t.Errorf("get deleted status = %d, want 404", resp5.StatusCode)
	}
}
