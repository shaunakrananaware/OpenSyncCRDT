package crdt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/opensynccrdt/opensynccrdt/internal/webhook"
)

// Resolver calls a developer-configured HTTP endpoint to resolve a merge
// between two concurrent change sets, instead of letting Automerge merge them
// automatically. It is optional: when no resolver URL is configured the engine
// uses Automerge's automatic resolution and never constructs a Resolver.
type Resolver struct {
	url    string
	secret string
	client *http.Client
}

// NewResolver returns a Resolver, or nil if url is empty (meaning: use
// Automerge's automatic resolution).
func NewResolver(url, secret string, timeout time.Duration) *Resolver {
	if url == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Resolver{
		url:    url,
		secret: secret,
		client: &http.Client{Timeout: timeout},
	}
}

// resolveRequest is the JSON body posted to the resolver endpoint.
type resolveRequest struct {
	DocID        string `json:"doc_id"`
	ChangeA      string `json:"change_a"`
	ChangeB      string `json:"change_b"`
	CurrentState string `json:"current_state"`
	Timestamp    string `json:"timestamp"`
}

// resolveResponse is the expected JSON reply.
type resolveResponse struct {
	ResolvedState string `json:"resolved_state"`
}

// Resolve posts both concurrent change sets and the current document state to
// the developer's endpoint and returns the resolved full-state bytes. On any
// error (including timeout or a non-2xx status) it returns an error so the
// caller can fall back to Automerge's automatic resolution and log a warning.
func (r *Resolver) Resolve(ctx context.Context, docID string, changeA, changeB, currentState []byte) ([]byte, error) {
	body, err := json.Marshal(resolveRequest{
		DocID:        docID,
		ChangeA:      base64.StdEncoding.EncodeToString(changeA),
		ChangeB:      base64.StdEncoding.EncodeToString(changeB),
		CurrentState: base64.StdEncoding.EncodeToString(currentState),
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal resolve request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build resolve request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenSyncCRDT-Signature", webhook.Sign(r.secret, body))

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call resolver: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("resolver returned status %d", resp.StatusCode)
	}

	var out resolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode resolver response: %w", err)
	}
	state, err := base64.StdEncoding.DecodeString(out.ResolvedState)
	if err != nil {
		return nil, fmt.Errorf("decode resolved_state: %w", err)
	}
	if len(state) == 0 {
		return nil, fmt.Errorf("resolver returned empty resolved_state")
	}
	return state, nil
}
