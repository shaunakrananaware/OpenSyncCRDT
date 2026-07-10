// Package auth authorizes incoming WebSocket connections. Auth is the
// developer's responsibility: OpenSyncCRDT does not manage users, sessions, or
// tokens. It offers three modes selected purely by configuration — none,
// header, and webhook — and switching between them requires only changing
// AUTH_MODE and restarting. No code changes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
)

// ErrUnauthorized indicates a connection must be rejected. The server maps it
// to HTTP 401.
var ErrUnauthorized = errors.New("unauthorized")

// Identity is the authenticated principal for a connection. In none mode it is
// empty; in header/webhook modes UserID carries the developer's user identity,
// used in logging and webhook payloads. Metadata is developer-supplied context
// (webhook mode only) passed through to webhook events.
type Identity struct {
	UserID   string
	Metadata map[string]any
}

// Authenticator authorizes a new connection from its HTTP upgrade request.
type Authenticator interface {
	// Authenticate returns the connection's identity or ErrUnauthorized. Other
	// errors indicate an internal failure (e.g. the auth webhook is unreachable)
	// and also reject the connection.
	Authenticate(ctx context.Context, r *http.Request) (Identity, error)
	// Mode reports the configured auth mode, for logging.
	Mode() config.AuthMode
}

// New builds the Authenticator for the configured mode.
func New(cfg config.AuthConfig) (Authenticator, error) {
	switch cfg.Mode {
	case config.AuthModeNone:
		return noneAuth{}, nil
	case config.AuthModeHeader:
		return headerAuth{headerName: cfg.HeaderName}, nil
	case config.AuthModeWebhook:
		return newWebhookAuth(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", cfg.Mode)
	}
}
