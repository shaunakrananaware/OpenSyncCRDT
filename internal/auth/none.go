package auth

import (
	"context"
	"net/http"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
)

// noneAuth accepts every connection. Default mode: local development, trusted
// internal networks, or when auth is enforced at the network level.
type noneAuth struct{}

func (noneAuth) Authenticate(context.Context, *http.Request) (Identity, error) {
	return Identity{}, nil
}

func (noneAuth) Mode() config.AuthMode { return config.AuthModeNone }
