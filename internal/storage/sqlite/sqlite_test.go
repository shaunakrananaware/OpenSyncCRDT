package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/storage"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	cfg := config.Default().Storage
	cfg.SQLitePath = filepath.Join(t.TempDir(), "test.db")
	cfg.MaxOpenConns = 4 // exercise concurrent access
	s, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateGetDeleteDocument(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	doc, err := s.CreateDocument(ctx, "doc1", "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if doc.ID != "doc1" || doc.CreatedBy != "alice" || doc.LatestSeq != 0 {
		t.Errorf("unexpected doc: %+v", doc)
	}

	// Duplicate create rejected.
	if _, err := s.CreateDocument(ctx, "doc1", "alice"); !errors.Is(err, storage.ErrDocumentExists) {
		t.Errorf("duplicate create err = %v, want ErrDocumentExists", err)
	}

	got, err := s.GetDocument(ctx, "doc1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "doc1" {
		t.Errorf("get id = %q", got.ID)
	}

	if err := s.DeleteDocument(ctx, "doc1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetDocument(ctx, "doc1"); !errors.Is(err, storage.ErrDocumentNotFound) {
		t.Errorf("get after delete err = %v, want ErrDocumentNotFound", err)
	}
	if err := s.DeleteDocument(ctx, "doc1"); !errors.Is(err, storage.ErrDocumentNotFound) {
		t.Errorf("delete missing err = %v, want ErrDocumentNotFound", err)
	}
}

func TestAppendOpAssignsSequenceAndCreates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// First write creates the document implicitly.
	res, err := s.AppendOp(ctx, storage.Operation{
		DocID: "d", SessionID: "s1", UserID: "bob", Payload: []byte("change-1"),
	})
	if err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if res.Seq != 1 || !res.Created {
		t.Errorf("first append = %+v, want seq 1 created true", res)
	}

	res2, err := s.AppendOp(ctx, storage.Operation{
		DocID: "d", SessionID: "s1", Payload: []byte("change-2"),
	})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if res2.Seq != 2 || res2.Created {
		t.Errorf("second append = %+v, want seq 2 created false", res2)
	}

	doc, err := s.GetDocument(ctx, "d")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if doc.LatestSeq != 2 {
		t.Errorf("latest seq = %d, want 2", doc.LatestSeq)
	}
	if doc.CreatedBy != "bob" {
		t.Errorf("created_by = %q, want bob (from first op)", doc.CreatedBy)
	}
}

func TestListOps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := s.AppendOp(ctx, storage.Operation{
			DocID: "d", Payload: []byte{byte(i)},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// afterSeq 0 returns everything in order.
	ops, err := s.ListOps(ctx, "d", 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ops) != 5 {
		t.Fatalf("len = %d, want 5", len(ops))
	}
	for i, op := range ops {
		if op.Seq != int64(i+1) {
			t.Errorf("ops[%d].Seq = %d, want %d", i, op.Seq, i+1)
		}
	}

	// afterSeq + limit slices the log for catch-up replay.
	ops, err = s.ListOps(ctx, "d", 2, 2)
	if err != nil {
		t.Fatalf("list slice: %v", err)
	}
	if len(ops) != 2 || ops[0].Seq != 3 || ops[1].Seq != 4 {
		t.Errorf("slice = %+v, want seq 3,4", seqs(ops))
	}
}

func TestSnapshots(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateDocument(ctx, "d", ""); err != nil {
		t.Fatalf("create doc: %v", err)
	}

	if _, err := s.LatestSnapshot(ctx, "d"); !errors.Is(err, storage.ErrNoSnapshot) {
		t.Errorf("empty snapshot err = %v, want ErrNoSnapshot", err)
	}

	if err := s.SaveSnapshot(ctx, storage.Snapshot{DocID: "d", Seq: 10, State: []byte("state-10")}); err != nil {
		t.Fatalf("save 10: %v", err)
	}
	if err := s.SaveSnapshot(ctx, storage.Snapshot{DocID: "d", Seq: 20, State: []byte("state-20")}); err != nil {
		t.Fatalf("save 20: %v", err)
	}

	snap, err := s.LatestSnapshot(ctx, "d")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if snap.Seq != 20 || string(snap.State) != "state-20" {
		t.Errorf("latest = seq %d state %q, want 20/state-20", snap.Seq, snap.State)
	}

	// Re-saving the same seq replaces the state.
	if err := s.SaveSnapshot(ctx, storage.Snapshot{DocID: "d", Seq: 20, State: []byte("state-20b")}); err != nil {
		t.Fatalf("resave 20: %v", err)
	}
	snap, _ = s.LatestSnapshot(ctx, "d")
	if string(snap.State) != "state-20b" {
		t.Errorf("resaved state = %q, want state-20b", snap.State)
	}
}

func TestDeleteCascades(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.AppendOp(ctx, storage.Operation{DocID: "d", Payload: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot(ctx, storage.Snapshot{DocID: "d", Seq: 1, State: []byte("s")}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDocument(ctx, "d"); err != nil {
		t.Fatal(err)
	}

	ops, err := s.ListOps(ctx, "d", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("ops after delete = %d, want 0 (cascade)", len(ops))
	}
	if _, err := s.LatestSnapshot(ctx, "d"); !errors.Is(err, storage.ErrNoSnapshot) {
		t.Errorf("snapshot after delete err = %v, want ErrNoSnapshot (cascade)", err)
	}
}

func TestListDocumentsFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateDocument(ctx, "a", "alice"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := s.CreateDocument(ctx, "b", "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDocument(ctx, "c", "alice"); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListDocuments(ctx, storage.DocumentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("all = %d, want 3", len(all))
	}

	alices, err := s.ListDocuments(ctx, storage.DocumentFilter{CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alices) != 2 {
		t.Errorf("alice docs = %d, want 2", len(alices))
	}

	limited, err := s.ListDocuments(ctx, storage.DocumentFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Errorf("limited = %d, want 1", len(limited))
	}
}

// TestConcurrentAppendNoGaps verifies sequence assignment stays gap-free and
// unique under concurrent writers to the same document.
func TestConcurrentAppendNoGaps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const writers = 8
	const perWriter = 25
	var wg sync.WaitGroup
	seqCh := make(chan int64, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				res, err := s.AppendOp(ctx, storage.Operation{DocID: "hot", Payload: []byte("x")})
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				seqCh <- res.Seq
			}
		}()
	}
	wg.Wait()
	close(seqCh)

	seen := make(map[int64]bool)
	for seq := range seqCh {
		if seen[seq] {
			t.Errorf("duplicate seq %d", seq)
		}
		seen[seq] = true
	}
	total := writers * perWriter
	for i := int64(1); i <= int64(total); i++ {
		if !seen[i] {
			t.Errorf("missing seq %d", i)
		}
	}

	doc, err := s.GetDocument(ctx, "hot")
	if err != nil {
		t.Fatal(err)
	}
	if doc.LatestSeq != int64(total) {
		t.Errorf("latest seq = %d, want %d", doc.LatestSeq, total)
	}
}

func seqs(ops []*storage.Operation) []int64 {
	out := make([]int64, len(ops))
	for i, op := range ops {
		out[i] = op.Seq
	}
	return out
}
