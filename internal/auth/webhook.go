package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	stdsync "sync"
	"time"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/webhook"
)

// webhookAuth calls the developer's endpoint to authorize every new connection.
// Responses are cached per token per doc_id for a configurable TTL so reconnect
// storms do not hammer the developer's auth server.
type webhookAuth struct {
	url      string
	secret   string
	cacheTTL time.Duration
	client   *http.Client

	mu    stdsync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	identity Identity
	allowed  bool
	expires  time.Time
}

func newWebhookAuth(cfg config.AuthConfig) *webhookAuth {
	timeout := cfg.WebhookTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &webhookAuth{
		url:      cfg.WebhookURL,
		secret:   cfg.WebhookSecret,
		cacheTTL: cfg.WebhookCacheTTL,
		client:   &http.Client{Timeout: timeout},
		cache:    make(map[string]cacheEntry),
	}
}

// verifyRequest is the signed JSON body posted to the developer's endpoint.
type verifyRequest struct {
	Token     string `json:"token"`
	DocID     string `json:"doc_id"`
	Action    string `json:"action"`
	Timestamp string `json:"timestamp"`
}

// verifyResponse is the expected reply. A 200 status means allow; any other
// status (or a transport error/timeout) means deny.
type verifyResponse struct {
	Allowed  bool           `json:"allowed"`
	UserID   string         `json:"user_id"`
	Metadata map[string]any `json:"metadata"`
}

func (w *webhookAuth) Authenticate(ctx context.Context, r *http.Request) (Identity, error) {
	token := extractToken(r)
	docID := r.URL.Query().Get("doc_id")
	key := token + "\x00" + docID

	if id, ok, found := w.lookup(key); found {
		if !ok {
			return Identity{}, ErrUnauthorized
		}
		return id, nil
	}

	body, err := json.Marshal(verifyRequest{
		Token:     token,
		DocID:     docID,
		Action:    "connect",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return Identity{}, fmt.Errorf("marshal verify request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return Identity{}, fmt.Errorf("build verify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenSyncCRDT-Signature", webhook.Sign(w.secret, body))

	resp, err := w.client.Do(req)
	if err != nil {
		// Timeout or transport error: deny.
		return Identity{}, fmt.Errorf("%w: auth webhook: %v", ErrUnauthorized, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.store(key, Identity{}, false)
		return Identity{}, ErrUnauthorized
	}

	var vr verifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return Identity{}, fmt.Errorf("decode verify response: %w", err)
	}
	if !vr.Allowed {
		w.store(key, Identity{}, false)
		return Identity{}, ErrUnauthorized
	}

	id := Identity{UserID: vr.UserID, Metadata: vr.Metadata}
	w.store(key, id, true)
	return id, nil
}

func (*webhookAuth) Mode() config.AuthMode { return config.AuthModeWebhook }

func (w *webhookAuth) lookup(key string) (Identity, bool, bool) {
	if w.cacheTTL <= 0 {
		return Identity{}, false, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	e, ok := w.cache[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(w.cache, key)
		}
		return Identity{}, false, false
	}
	return e.identity, e.allowed, true
}

func (w *webhookAuth) store(key string, id Identity, allowed bool) {
	if w.cacheTTL <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cache[key] = cacheEntry{identity: id, allowed: allowed, expires: time.Now().Add(w.cacheTTL)}
}

// extractToken reads the caller's token from the Authorization header (bearer
// or raw) or, failing that, the "token" query parameter.
func extractToken(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
		return h
	}
	return r.URL.Query().Get("token")
}
