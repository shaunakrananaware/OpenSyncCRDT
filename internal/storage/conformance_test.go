package storage

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// storeFactory returns a fresh, empty Store. It is invoked once per conformance
// subtest so each scenario starts from a clean slate. For the on-disk backends
// (Postgres, MySQL) the factory truncates the schema; for SQLite each call gets
// a new temp-dir database.
type storeFactory func(t *testing.T) Store

// testStoreConformance runs the full storage contract against a backend. Every
// backend — SQLite, Postgres, MySQL — passes this identical suite, which is the
// specification's requirement that all backends behave the same.
func testStoreConformance(t *testing.T, newStore storeFactory) {
	t.Run("CreateGetDeleteDocument", func(t *testing.T) {
		s := newStore(t)

		if err := s.CreateDocument("doc1", map[string]string{"owner": "alice"}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := s.CreateDocument("doc1", nil); !errors.Is(err, ErrDocumentExists) {
			t.Fatalf("duplicate create err = %v, want ErrDocumentExists", err)
		}

		doc, err := s.GetDocument("doc1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if doc.Metadata["owner"] != "alice" {
			t.Errorf("metadata = %v", doc.Metadata)
		}
		if doc.LatestSeq != 0 {
			t.Errorf("latest seq = %d, want 0", doc.LatestSeq)
		}

		if err := s.DeleteDocument("doc1"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.GetDocument("doc1"); !errors.Is(err, ErrDocumentNotFound) {
			t.Fatalf("get after delete err = %v, want ErrDocumentNotFound", err)
		}
		if err := s.DeleteDocument("doc1"); !errors.Is(err, ErrDocumentNotFound) {
			t.Fatalf("delete missing err = %v, want ErrDocumentNotFound", err)
		}
	})

	t.Run("AppendOpSequence", func(t *testing.T) {
		s := newStore(t)

		// First op auto-creates the document and gets seq 1.
		seq, err := s.AppendOp("doc", []byte("a"), "sess-1")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if seq != 1 {
			t.Fatalf("first seq = %d, want 1", seq)
		}
		seq, err = s.AppendOp("doc", []byte("b"), "sess-1")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if seq != 2 {
			t.Fatalf("second seq = %d, want 2", seq)
		}

		if latest, _ := s.GetLatestSeq("doc"); latest != 2 {
			t.Errorf("latest = %d, want 2", latest)
		}
		if latest, _ := s.GetLatestSeq("missing"); latest != 0 {
			t.Errorf("latest of missing = %d, want 0", latest)
		}
	})

	t.Run("GetOpsSinceAndRange", func(t *testing.T) {
		s := newStore(t)
		for i := 0; i < 5; i++ {
			if _, err := s.AppendOp("doc", []byte{byte('a' + i)}, "s"); err != nil {
				t.Fatal(err)
			}
		}

		ops, err := s.GetOpsSince("doc", 2)
		if err != nil {
			t.Fatalf("since: %v", err)
		}
		if len(ops) != 3 || ops[0].Seq != 3 || ops[2].Seq != 5 {
			t.Fatalf("since(2) = %+v", ops)
		}
		// Payload round-trips intact.
		if string(ops[0].Payload) != "c" {
			t.Errorf("payload = %q, want c", ops[0].Payload)
		}

		rng, err := s.GetOpsInRange("doc", 2, 4)
		if err != nil {
			t.Fatalf("range: %v", err)
		}
		if len(rng) != 3 || rng[0].Seq != 2 || rng[2].Seq != 4 {
			t.Fatalf("range(2,4) = %+v", rng)
		}

		// Nonexistent document yields empty, not error.
		empty, err := s.GetOpsSince("missing", 0)
		if err != nil || len(empty) != 0 {
			t.Fatalf("since missing = %v, %v", empty, err)
		}
	})

	t.Run("Snapshots", func(t *testing.T) {
		s := newStore(t)
		if _, _, err := s.GetSnapshot("doc"); !errors.Is(err, ErrNoSnapshot) {
			t.Fatalf("no snapshot err = %v, want ErrNoSnapshot", err)
		}

		if _, err := s.AppendOp("doc", []byte("a"), "s"); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveSnapshot("doc", []byte("state-1"), 1); err != nil {
			t.Fatalf("save snapshot: %v", err)
		}
		if _, err := s.AppendOp("doc", []byte("b"), "s"); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveSnapshot("doc", []byte("state-2"), 2); err != nil {
			t.Fatalf("save snapshot: %v", err)
		}
		// Re-saving at the same seq replaces the prior snapshot.
		if err := s.SaveSnapshot("doc", []byte("state-2b"), 2); err != nil {
			t.Fatalf("re-save snapshot: %v", err)
		}

		state, atSeq, err := s.GetSnapshot("doc")
		if err != nil {
			t.Fatalf("get snapshot: %v", err)
		}
		if atSeq != 2 || string(state) != "state-2b" {
			t.Fatalf("snapshot = %q @ %d, want state-2b @ 2", state, atSeq)
		}
	})

	t.Run("DeleteCascade", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.AppendOp("doc", []byte("a"), "s"); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveSnapshot("doc", []byte("state"), 1); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteDocument("doc"); err != nil {
			t.Fatal(err)
		}
		if ops, _ := s.GetOpsSince("doc", 0); len(ops) != 0 {
			t.Errorf("ops after delete = %d, want 0", len(ops))
		}
		if _, _, err := s.GetSnapshot("doc"); !errors.Is(err, ErrNoSnapshot) {
			t.Errorf("snapshot after delete err = %v, want ErrNoSnapshot", err)
		}
	})

	t.Run("ListDocuments", func(t *testing.T) {
		s := newStore(t)
		for _, id := range []string{"a", "b", "c"} {
			if _, err := s.AppendOp(id, []byte("x"), "s"); err != nil {
				t.Fatal(err)
			}
		}
		docs, err := s.ListDocuments(DocumentFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(docs) != 3 {
			t.Fatalf("list len = %d, want 3", len(docs))
		}
		got := map[string]bool{}
		for _, d := range docs {
			got[d.ID] = true
		}
		for _, id := range []string{"a", "b", "c"} {
			if !got[id] {
				t.Errorf("missing doc %q in list", id)
			}
		}

		limited, err := s.ListDocuments(DocumentFilter{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(limited) != 2 {
			t.Errorf("limited len = %d, want 2", len(limited))
		}
	})

	// ConcurrentAppendNoGaps verifies sequence assignment is gap-free and
	// unique under concurrent writers — the property AppendOp's row-locking
	// transaction guarantees on every backend.
	t.Run("ConcurrentAppendNoGaps", func(t *testing.T) {
		s := newStore(t)
		const (
			writers  = 8
			perWrite = 25
			total    = writers * perWrite
		)

		var wg sync.WaitGroup
		seqs := make(chan int64, total)
		errs := make(chan error, total)
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < perWrite; i++ {
					seq, err := s.AppendOp("doc", []byte(fmt.Sprintf("%d-%d", w, i)), fmt.Sprintf("s%d", w))
					if err != nil {
						errs <- err
						return
					}
					seqs <- seq
				}
			}(w)
		}
		wg.Wait()
		close(seqs)
		close(errs)

		for err := range errs {
			t.Fatalf("concurrent append: %v", err)
		}

		seen := make(map[int64]bool, total)
		for seq := range seqs {
			if seen[seq] {
				t.Fatalf("duplicate seq %d", seq)
			}
			seen[seq] = true
		}
		for i := int64(1); i <= total; i++ {
			if !seen[i] {
				t.Fatalf("missing seq %d (gap)", i)
			}
		}
	})
}
