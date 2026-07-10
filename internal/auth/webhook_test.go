package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
)

func mustRequest(t *testing.T, token, docID string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/sync?doc_id="+docID, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestWebhookAuthCachesAllow(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"allowed": true, "user_id": "alice"})
	}))
	defer srv.Close()

	a, err := New(config.AuthConfig{
		Mode:            config.AuthModeWebhook,
		WebhookURL:      srv.URL,
		WebhookSecret:   "s",
		WebhookTimeout:  2 * time.Second,
		WebhookCacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		id, err := a.Authenticate(context.Background(), mustRequest(t, "tok", "doc1"))
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		if id.UserID != "alice" {
			t.Fatalf("user id = %q, want alice", id.UserID)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("webhook called %d times, want 1 (cached)", got)
	}

	// A different doc_id is a distinct cache key and calls again.
	if _, err := a.Authenticate(context.Background(), mustRequest(t, "tok", "doc2")); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("webhook called %d times after new doc, want 2", got)
	}
}

func TestWebhookAuthDenies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	a, _ := New(config.AuthConfig{
		Mode:            config.AuthModeWebhook,
		WebhookURL:      srv.URL,
		WebhookCacheTTL: time.Minute,
	})
	if _, err := a.Authenticate(context.Background(), mustRequest(t, "tok", "doc1")); err != ErrUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestHeaderAuth(t *testing.T) {
	a, _ := New(config.AuthConfig{Mode: config.AuthModeHeader, HeaderName: "X-User-ID"})

	r := httptest.NewRequest(http.MethodGet, "/sync", nil)
	r.Header.Set("X-User-ID", "bob")
	id, err := a.Authenticate(context.Background(), r)
	if err != nil || id.UserID != "bob" {
		t.Fatalf("header auth = %q, %v", id.UserID, err)
	}

	// Missing header is rejected.
	if _, err := a.Authenticate(context.Background(), httptest.NewRequest(http.MethodGet, "/sync", nil)); err != ErrUnauthorized {
		t.Fatalf("missing header err = %v, want ErrUnauthorized", err)
	}
}

func TestNoneAuthAcceptsAll(t *testing.T) {
	a, _ := New(config.AuthConfig{Mode: config.AuthModeNone})
	id, err := a.Authenticate(context.Background(), httptest.NewRequest(http.MethodGet, "/sync", nil))
	if err != nil || id.UserID != "" {
		t.Fatalf("none auth = %q, %v", id.UserID, err)
	}
}
