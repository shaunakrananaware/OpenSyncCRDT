package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/opensynccrdt/opensynccrdt/pkg/protocol"
)

// captureBroadcaster is a stand-in for the server hub: it records every
// operation delivered to a node's local connections.
type captureBroadcaster struct {
	ch chan protocol.ServerSync
}

func newCapture() *captureBroadcaster {
	return &captureBroadcaster{ch: make(chan protocol.ServerSync, 16)}
}

func (c *captureBroadcaster) Broadcast(_, _ string, msg protocol.ServerSync) {
	c.ch <- msg
}

func uniqueID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

func newTestNode(t *testing.T, url, nodeID string, local *captureBroadcaster) *Node {
	t.Helper()
	n, err := New(Options{
		Ctx:      context.Background(),
		RedisURL: url,
		NodeID:   nodeID,
		Addr:     "127.0.0.1:0",
		Local:    local,
	})
	if err != nil {
		t.Fatalf("join cluster (%s): %v", nodeID, err)
	}
	t.Cleanup(func() { _ = n.Close() })
	return n
}

// TestClusterCrossNodeBroadcast is success criterion #9: an operation committed
// on one node reaches clients connected to a different node. It is skipped
// unless TEST_REDIS_URL is set, so local `go test ./...` stays hermetic.
func TestClusterCrossNodeBroadcast(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("set TEST_REDIS_URL to run cluster tests")
	}

	localA, localB := newCapture(), newCapture()
	a := newTestNode(t, url, uniqueID("node-a"), localA)
	b := newTestNode(t, url, uniqueID("node-b"), localB)

	doc := uniqueID("doc")
	// Both nodes serve the document, so both subscribe to its channel.
	a.OnDocActive(doc)
	b.OnDocActive(doc)
	// Let the SUBSCRIBE commands take effect before publishing.
	time.Sleep(250 * time.Millisecond)

	msg := protocol.NewServerSync(doc, "sess-1", 7, []byte("change-set"))
	a.Broadcast(doc, "sess-1", msg)

	// Node A delivers to its own local connections directly (not via Redis).
	select {
	case got := <-localA.ch:
		if got.Seq != 7 || string(got.Payload) != "change-set" {
			t.Fatalf("node A local delivery = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("node A did not deliver locally")
	}

	// Node B receives the same operation across the cluster.
	select {
	case got := <-localB.ch:
		if got.DocID != doc || got.Seq != 7 || got.FromSession != "sess-1" ||
			string(got.Payload) != "change-set" {
			t.Fatalf("node B cross-node delivery = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("node B did not receive cross-node broadcast")
	}

	// Node A must not receive the echo of its own publish.
	select {
	case got := <-localA.ch:
		t.Fatalf("node A received echo of its own publish: %+v", got)
	case <-time.After(400 * time.Millisecond):
	}
}

// TestClusterNodeRegistry verifies nodes register themselves and are listable.
func TestClusterNodeRegistry(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("set TEST_REDIS_URL to run cluster tests")
	}

	idA, idB := uniqueID("node-a"), uniqueID("node-b")
	a := newTestNode(t, url, idA, newCapture())
	newTestNode(t, url, idB, newCapture())

	infos, err := a.Nodes(context.Background())
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	seen := map[string]bool{}
	for _, n := range infos {
		seen[n.ID] = true
	}
	if !seen[idA] || !seen[idB] {
		t.Fatalf("registry missing nodes; got %v, want %s and %s present", seen, idA, idB)
	}
}

// TestClusterUnsubscribeStopsDelivery verifies a node stops receiving a
// document's operations once its last local subscriber leaves.
func TestClusterUnsubscribeStopsDelivery(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("set TEST_REDIS_URL to run cluster tests")
	}

	localA, localB := newCapture(), newCapture()
	a := newTestNode(t, url, uniqueID("node-a"), localA)
	b := newTestNode(t, url, uniqueID("node-b"), localB)

	doc := uniqueID("doc")
	a.OnDocActive(doc)
	b.OnDocActive(doc)
	time.Sleep(250 * time.Millisecond)

	// B's last local subscriber for the document leaves.
	b.OnDocInactive(doc)
	time.Sleep(250 * time.Millisecond)

	a.Broadcast(doc, "sess-1", protocol.NewServerSync(doc, "sess-1", 1, []byte("x")))

	select {
	case got := <-localB.ch:
		t.Fatalf("node B still received after unsubscribe: %+v", got)
	case <-time.After(1 * time.Second):
	}
}
