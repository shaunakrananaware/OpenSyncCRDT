// Package api implements the REST management surface and the Kubernetes health
// and readiness probes. All endpoints accept and return JSON (except the
// Prometheus metrics endpoint, which returns text exposition format).
//
// API versioning lives in the URL path (/api/v1/). v1 is designed to be
// extended without a breaking v2.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/opensynccrdt/opensynccrdt/internal/cluster"
	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/metrics"
	"github.com/opensynccrdt/opensynccrdt/internal/storage"
	syncengine "github.com/opensynccrdt/opensynccrdt/internal/sync"
)

// API holds the dependencies for the REST surface.
type API struct {
	engine    *syncengine.Engine
	store     storage.Store
	metrics   *metrics.Registry
	emitter   syncengine.Emitter
	cfg       config.Config
	logger    *slog.Logger
	startedAt time.Time
	nodeID    string
	cluster   *cluster.Node
}

// Options configures an API.
type Options struct {
	Engine  *syncengine.Engine
	Metrics *metrics.Registry
	Emitter syncengine.Emitter
	Config  config.Config
	Logger  *slog.Logger
	NodeID  string
	// Cluster, when non-nil, backs /api/v1/nodes with the live Redis node
	// registry. Nil in single-node mode.
	Cluster *cluster.Node
}

// New builds an API.
func New(opts Options) *API {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &API{
		engine:    opts.Engine,
		store:     opts.Engine.Store(),
		metrics:   opts.Metrics,
		emitter:   opts.Emitter,
		cfg:       opts.Config,
		logger:    logger,
		startedAt: time.Now(),
		nodeID:    opts.NodeID,
		cluster:   opts.Cluster,
	}
}

// Register mounts every REST route (and /health, /ready) onto mux. The
// management routes under /api/v1/ are gated by MANAGEMENT_API_ENABLED and, when
// set, the MANAGEMENT_API_KEY bearer token.
func (a *API) Register(mux *http.ServeMux) {
	// Probes are always available and unauthenticated.
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /ready", a.handleReady)

	if !a.cfg.Management.Enabled {
		return
	}

	m := a.requireKey // management-key middleware
	mux.Handle("POST /api/v1/docs", m(http.HandlerFunc(a.handleCreateDoc)))
	mux.Handle("GET /api/v1/docs", m(http.HandlerFunc(a.handleListDocs)))
	mux.Handle("GET /api/v1/docs/{id}", m(http.HandlerFunc(a.handleGetDoc)))
	mux.Handle("DELETE /api/v1/docs/{id}", m(http.HandlerFunc(a.handleDeleteDoc)))
	mux.Handle("GET /api/v1/docs/{id}/history", m(http.HandlerFunc(a.handleHistory)))
	mux.Handle("POST /api/v1/docs/{id}/snapshot", m(http.HandlerFunc(a.handleSnapshot)))
	mux.Handle("GET /api/v1/docs/{id}/export", m(http.HandlerFunc(a.handleExport)))
	mux.Handle("GET /api/v1/metrics", m(http.HandlerFunc(a.handleMetrics)))
	mux.Handle("GET /api/v1/nodes", m(http.HandlerFunc(a.handleNodes)))
}

// requireKey enforces the optional management API bearer token.
func (a *API) requireKey(next http.Handler) http.Handler {
	key := a.cfg.Management.Key
	if key == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			writeError(w, http.StatusUnauthorized, "missing or invalid management API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
