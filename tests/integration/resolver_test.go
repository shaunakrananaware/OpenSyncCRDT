package integration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/automerge/automerge-go"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
	"github.com/shaunakrananaware/OpenSyncCRDT/pkg/engine"
	"github.com/shaunakrananaware/OpenSyncCRDT/pkg/protocol"
)

// resolverSentinel is the value the mock resolver forces onto the document. It
// differs from any value the concurrent client changes carry, so seeing it in
// the committed state proves the resolver's result won — not Automerge's
// automatic merge.
const resolverSentinel = int64(999)

// TestCustomResolverCalledAndCommitted is the §18 integration requirement: a
// mock conflict-resolver endpoint is called when concurrent history diverges,
// and the resolved state it returns is committed to the document.
func TestCustomResolverCalledAndCommitted(t *testing.T) {
	const secret = "resolver-secret"

	var (
		called    atomic.Int32
		badSig    atomic.Bool
		lastDocID atomic.Value // string
	)

	// Mock resolver: verify the HMAC signature, then return a document state
	// with the sentinel value so the test can tell the resolver's result apart
	// from Automerge's automatic merge.
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("X-OpenSyncCRDT-Signature"); got != want {
			badSig.Store(true)
		}

		var req struct {
			DocID        string `json:"doc_id"`
			ChangeA      string `json:"change_a"`
			ChangeB      string `json:"change_b"`
			CurrentState string `json:"current_state"`
			Timestamp    string `json:"timestamp"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		lastDocID.Store(req.DocID)

		// Build the resolved state from the base state the server sent us, so it
		// is a valid continuation of the document's history.
		base, err := base64.StdEncoding.DecodeString(req.CurrentState)
		if err != nil {
			http.Error(w, "bad current_state", http.StatusBadRequest)
			return
		}
		doc, err := automerge.Load(base)
		if err != nil {
			http.Error(w, "load current_state", http.StatusBadRequest)
			return
		}
		if err := doc.RootMap().Set("k", resolverSentinel); err != nil {
			http.Error(w, "set", http.StatusInternalServerError)
			return
		}
		if _, err := doc.Commit("resolved"); err != nil {
			http.Error(w, "commit", http.StatusInternalServerError)
			return
		}

		called.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"resolved_state": base64.StdEncoding.EncodeToString(doc.Save()),
		})
	}))
	t.Cleanup(resolver.Close)

	// Engine wired to the mock resolver.
	full := config.Default()
	full.Storage.Backend = config.StorageSQLite
	full.Storage.DataDir = t.TempDir()
	full.Auth.Mode = config.AuthModeNone
	full.Conflict.ResolverURL = resolver.URL
	full.Conflict.ResolverSecret = secret
	full.Conflict.Timeout = 5 * time.Second

	eng, err := engine.New(engine.Config{Full: &full})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	ts := httptest.NewServer(eng.Handler())
	t.Cleanup(func() {
		ts.Close()
		eng.Close()
	})

	const docID = "conflict-doc"
	a := dial(t, ts)
	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSubscribe, DocID: docID, SessionID: "sess-a"})
	_ = readUntil(t, a, protocol.TypeReplay) // terminal empty batch for the new doc

	// c1: the base op. Linear history, no divergence yet.
	c1 := makeChange(t, nil, func(d *automerge.Doc) { _ = d.RootMap().Set("k", int64(1)) })
	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSync, DocID: docID, SessionID: "sess-a", Payload: c1})
	_ = readUntil(t, a, protocol.TypeAck)

	// Two changes made concurrently from the state after c1: neither descends
	// from the other, so applying both leaves the document with two heads.
	base := automerge.New()
	if err := base.LoadIncremental(c1); err != nil {
		t.Fatalf("load c1: %v", err)
	}
	stateAfterC1 := base.Save()
	cX := makeChange(t, stateAfterC1, func(d *automerge.Doc) { _ = d.RootMap().Set("k", int64(2)) })
	cY := makeChange(t, stateAfterC1, func(d *automerge.Doc) { _ = d.RootMap().Set("k", int64(3)) })

	// cX extends c1 linearly (still one head) — resolver not consulted.
	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSync, DocID: docID, SessionID: "sess-a", Payload: cX})
	_ = readUntil(t, a, protocol.TypeAck)

	// cY is concurrent with cX; applying it produces divergent history, which
	// triggers the resolver.
	writeJSON(t, a, protocol.Inbound{Type: protocol.TypeSync, DocID: docID, SessionID: "sess-a", Payload: cY})
	_ = readUntil(t, a, protocol.TypeAck)

	if called.Load() == 0 {
		t.Fatal("conflict resolver was never called on divergent history")
	}
	if badSig.Load() {
		t.Error("resolver received an invalid HMAC signature")
	}
	if got, _ := lastDocID.Load().(string); got != docID {
		t.Errorf("resolver doc_id = %q, want %q", got, docID)
	}

	// The committed state must be the resolver's result (sentinel), not
	// Automerge's automatic merge (which would leave k as 2 or 3).
	resp, err := ts.Client().Get(ts.URL + "/api/v1/docs/" + docID + "/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want 200", resp.StatusCode)
	}
	var exported struct {
		DocID string `json:"doc_id"`
		State []byte `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&exported); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	doc, err := automerge.Load(exported.State)
	if err != nil {
		t.Fatalf("load exported state: %v", err)
	}
	v, err := doc.RootMap().Get("k")
	if err != nil {
		t.Fatalf("get k: %v", err)
	}
	if v.Int64() != resolverSentinel {
		t.Fatalf("committed k = %v, want resolver sentinel %d (resolver result was not committed)", v, resolverSentinel)
	}
}
