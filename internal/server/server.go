// Package server implements the WebSocket transport: connection upgrade, the
// per-connection read/write loops, and the hub that routes broadcasts. All
// real-time sync flows through a single endpoint, /sync.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"

	"github.com/opensynccrdt/opensynccrdt/internal/auth"
	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/metrics"
	syncengine "github.com/opensynccrdt/opensynccrdt/internal/sync"
	"github.com/opensynccrdt/opensynccrdt/pkg/protocol"
)

// serverDeps groups the dependencies each connection needs.
type serverDeps struct {
	hub     *hub
	engine  *syncengine.Engine
	emitter syncengine.Emitter
	metrics *metrics.Registry
	logger  *slog.Logger
	limits  config.LimitsConfig
}

// Server upgrades HTTP requests to WebSocket connections on /sync and owns the
// hub. It implements sync.Broadcaster (via the hub) so the engine can fan out
// committed changes.
type Server struct {
	auth    auth.Authenticator
	hub     *hub
	deps    serverDeps
	cors    config.CORSConfig
	logger  *slog.Logger
	baseCtx context.Context
}

// Options configures a Server.
type Options struct {
	Auth    auth.Authenticator
	Engine  *syncengine.Engine
	Emitter syncengine.Emitter
	Metrics *metrics.Registry
	Limits  config.LimitsConfig
	CORS    config.CORSConfig
	Logger  *slog.Logger
	BaseCtx context.Context
}

// New builds a Server. The returned Server's Broadcaster (Hub) should be passed
// to the engine so broadcasts reach connections.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	base := opts.BaseCtx
	if base == nil {
		base = context.Background()
	}
	h := newHub(logger)
	return &Server{
		auth: opts.Auth,
		hub:  h,
		deps: serverDeps{
			hub:     h,
			engine:  opts.Engine,
			emitter: opts.Emitter,
			metrics: opts.Metrics,
			logger:  logger,
			limits:  opts.Limits,
		},
		cors:    opts.CORS,
		logger:  logger,
		baseCtx: base,
	}
}

// Hub returns the broadcaster to wire into the engine.
func (s *Server) Hub() syncengine.Broadcaster { return s.hub }

// SetSubscriptionObserver installs an observer notified when a document gains
// its first local subscriber or loses its last. In cluster mode the cluster
// node is the observer, so it subscribes to a document's Redis channel only
// while the node serves that document. Must be called during wiring.
func (s *Server) SetSubscriptionObserver(o SubscriptionObserver) { s.hub.setObserver(o) }

// SetEngine injects the sync engine. It must be called before the first
// connection is served (the engine and server have a mutual dependency: the
// engine needs the hub, the connections need the engine).
func (s *Server) SetEngine(e *syncengine.Engine) { s.deps.engine = e }

// HandleSync is the http.HandlerFunc for the /sync WebSocket endpoint.
func (s *Server) HandleSync(w http.ResponseWriter, r *http.Request) {
	// Reserve a connection slot up front; the reservation is released either by
	// the deferred release (on an early failure) or by serve()'s final
	// deregister (on a served connection).
	if !s.hub.tryRegister(s.deps.limits.MaxConnections) {
		http.Error(w, "connection limit reached", http.StatusServiceUnavailable)
		return
	}
	served := false
	defer func() {
		if !served {
			s.hub.release()
		}
	}()

	// Authenticate before upgrading so a rejection is a clean HTTP status.
	identity, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			s.logger.Error("auth error", "error", err)
			http.Error(w, "authorization failed", http.StatusInternalServerError)
		}
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:   []string{protocol.SubprotocolJSON},
		OriginPatterns: s.originPatterns(),
	})
	if err != nil {
		// Accept already wrote a response.
		s.logger.Debug("websocket accept failed", "error", err)
		return
	}

	codec, _ := protocol.SelectCodec(subprotocols(ws.Subprotocol()))

	served = true // serve()'s deregister now owns the slot release
	c := newConn(s.baseCtx, ws, codec, s.deps, identity, r.RemoteAddr)
	c.serve()
}

// originPatterns maps CORS_ALLOWED_ORIGINS onto the websocket library's origin
// check. "*" (the default) allows any origin.
func (s *Server) originPatterns() []string {
	for _, o := range s.cors.AllowedOrigins {
		if o == "*" {
			return []string{"*"}
		}
	}
	// The library matches host patterns; strip scheme for matching.
	pats := make([]string, 0, len(s.cors.AllowedOrigins))
	for _, o := range s.cors.AllowedOrigins {
		pats = append(pats, stripScheme(o))
	}
	return pats
}

func subprotocols(negotiated string) []string {
	if negotiated == "" {
		return nil
	}
	return []string{negotiated}
}

func stripScheme(o string) string {
	for _, p := range []string{"https://", "http://", "wss://", "ws://"} {
		if len(o) >= len(p) && o[:len(p)] == p {
			return o[len(p):]
		}
	}
	return o
}
