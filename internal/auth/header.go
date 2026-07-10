package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
)

// headerAuth trusts an identity header injected by a trusted upstream (an API
// gateway, reverse proxy, or the developer's own server). OpenSyncCRDT trusts
// this header completely and never validates its value; it is the developer's
// responsibility to ensure only their trusted upstream can set it.
type headerAuth struct {
	headerName string
}

func (h headerAuth) Authenticate(_ context.Context, r *http.Request) (Identity, error) {
	v := strings.TrimSpace(r.Header.Get(h.headerName))
	if v == "" {
		// Absent header: reject with 401.
		return Identity{}, ErrUnauthorized
	}
	return Identity{UserID: v}, nil
}

func (headerAuth) Mode() config.AuthMode { return config.AuthModeHeader }
