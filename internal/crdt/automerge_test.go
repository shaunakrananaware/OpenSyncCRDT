package crdt

import (
	"testing"

	"github.com/automerge/automerge-go"
)

// clientChange simulates a client: it loads the shared state (if any), mutates
// the doc, commits, and returns the incremental change-set bytes a client would
// send over the wire.
func clientChange(t *testing.T, base []byte, mutate func(*automerge.Doc)) []byte {
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
	if _, err := d.Commit("change"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return d.SaveIncremental()
}

func TestApplyAndSave(t *testing.T) {
	doc := New()
	change := clientChange(t, nil, func(d *automerge.Doc) {
		_ = d.RootMap().Set("title", "hello")
	})
	if err := doc.Apply(change); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Reload the saved snapshot and confirm the value survived.
	reloaded, err := automerge.Load(doc.Save())
	if err != nil {
		t.Fatalf("load save: %v", err)
	}
	v, _ := reloaded.RootMap().Get("title")
	if v.Str() != "hello" {
		t.Fatalf("title = %v, want hello", v)
	}
}

func TestConcurrentMergeNoDataLoss(t *testing.T) {
	doc := New()
	base := doc.Save()

	// Two clients fork from the same base and change different keys.
	a := clientChange(t, base, func(d *automerge.Doc) { _ = d.RootMap().Set("a", int64(1)) })
	b := clientChange(t, base, func(d *automerge.Doc) { _ = d.RootMap().Set("b", int64(2)) })

	if err := doc.Apply(a); err != nil {
		t.Fatalf("apply a: %v", err)
	}
	if err := doc.Apply(b); err != nil {
		t.Fatalf("apply b: %v", err)
	}

	reloaded, _ := automerge.Load(doc.Save())
	va, _ := reloaded.RootMap().Get("a")
	vb, _ := reloaded.RootMap().Get("b")
	if va.Int64() != 1 || vb.Int64() != 2 {
		t.Fatalf("merged a=%v b=%v, want 1 and 2 (no data loss)", va, vb)
	}
}

func TestRehydrateFromSnapshotAndOps(t *testing.T) {
	// Build initial state, snapshot it, then apply two more ops on top.
	doc := New()
	c1 := clientChange(t, nil, func(d *automerge.Doc) { _ = d.RootMap().Set("k", int64(1)) })
	if err := doc.Apply(c1); err != nil {
		t.Fatal(err)
	}
	snapshot := doc.Save()

	c2 := clientChange(t, snapshot, func(d *automerge.Doc) { _ = d.RootMap().Set("k", int64(2)) })
	base2, _ := automerge.Load(snapshot)
	_ = base2.LoadIncremental(c2)
	c3 := clientChange(t, base2.Save(), func(d *automerge.Doc) { _ = d.RootMap().Set("m", int64(9)) })

	rebuilt, err := Rehydrate(snapshot, [][]byte{c2, c3})
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	reloaded, _ := automerge.Load(rebuilt.Save())
	k, _ := reloaded.RootMap().Get("k")
	m, _ := reloaded.RootMap().Get("m")
	if k.Int64() != 2 || m.Int64() != 9 {
		t.Fatalf("rehydrated k=%v m=%v, want 2 and 9", k, m)
	}
}
