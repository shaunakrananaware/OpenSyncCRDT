// Package engine is the public embedding API for OpenSyncCRDT. It lets another
// Go program run the sync engine in-process and mount it on an existing HTTP
// mux, instead of running the standalone binary.
//
//	eng, err := engine.New(engine.Config{
//	    Storage: engine.SQLiteStorage("./data"),
//	    Auth:    engine.NoAuth(),
//	})
//	if err != nil { log.Fatal(err) }
//	defer eng.Close()
//
//	mux.Handle("/", eng.Handler())            // full REST + /sync
//	http.HandleFunc("/my-sync", eng.WebSocketHandler()) // just the WebSocket
//
// The internal/ packages are implementation details; this is the only supported
// public surface for embedding.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/opensynccrdt/opensynccrdt/internal/api"
	"github.com/opensynccrdt/opensynccrdt/internal/auth"
	"github.com/opensynccrdt/opensynccrdt/internal/cluster"
	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/crdt"
	"github.com/opensynccrdt/opensynccrdt/internal/metrics"
	"github.com/opensynccrdt/opensynccrdt/internal/server"
	"github.com/opensynccrdt/opensynccrdt/internal/storage"
	syncengine "github.com/opensynccrdt/opensynccrdt/internal/sync"
	"github.com/opensynccrdt/opensynccrdt/internal/webhook"
)

// Configurator mutates a resolved config.Config. The Storage and Auth helpers
// below return Configurators.
type Configurator func(*config.Config)

// Config configures an embedded engine. Storage and Auth are convenience
// shortcuts; Full, if set, supplies a complete configuration that the shortcuts
// are then applied on top of.
type Config struct {
	Storage Configurator
	Auth    Configurator
	// Full overrides the built-in defaults with a complete configuration.
	Full *config.Config
	// NodeID identifies this instance in /api/v1/nodes; generated if empty.
	NodeID string
}

// SQLiteStorage selects the SQLite backend rooted at dataDir.
func SQLiteStorage(dataDir string) Configurator {
	return func(c *config.Config) {
		c.Storage.Backend = config.StorageSQLite
		c.Storage.DataDir = dataDir
	}
}

// NoAuth accepts all connections (AUTH_MODE=none).
func NoAuth() Configurator {
	return func(c *config.Config) { c.Auth.Mode = config.AuthModeNone }
}

// HeaderAuth trusts an upstream-injected identity header.
func HeaderAuth(headerName string) Configurator {
	return func(c *config.Config) {
		c.Auth.Mode = config.AuthModeHeader
		if headerName != "" {
			c.Auth.HeaderName = headerName
		}
	}
}

// Engine is a running, embeddable sync engine.
type Engine struct {
	cfg     config.Config
	store   storage.Store
	api     *api.API
	srv     *server.Server
	cluster *cluster.Node
	cancel  context.CancelFunc
}

// New assembles an embedded engine from cfg.
func New(cfg Config) (*Engine, error) {
	resolved := config.Default()
	if cfg.Full != nil {
		resolved = *cfg.Full
	}
	if cfg.Storage != nil {
		cfg.Storage(&resolved)
	}
	if cfg.Auth != nil {
		cfg.Auth(&resolved)
	}
	if err := resolved.Validate(); err != nil {
		return nil, fmt.Errorf("invalid engine config: %w", err)
	}
	return newFromConfig(resolved, cfg.NodeID)
}

// newFromConfig builds the full object graph. Shared by New and the standalone
// binary.
func newFromConfig(cfg config.Config, nodeID string) (*Engine, error) {
	ctx, cancel := context.WithCancel(context.Background())

	store, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open storage: %w", err)
	}

	authenticator, err := auth.New(cfg.Auth)
	if err != nil {
		cancel()
		_ = store.Close()
		return nil, fmt.Errorf("configure auth: %w", err)
	}

	reg := metrics.New()
	dispatcher := webhook.NewDispatcher(webhook.Config{
		Events:     cfg.Webhooks.Events,
		Secret:     cfg.Webhooks.Secret,
		Timeout:    cfg.Webhooks.Timeout,
		MaxRetries: cfg.Webhooks.MaxRetries,
		Metrics:    reg,
	})
	resolver := crdt.NewResolver(cfg.Conflict.ResolverURL, cfg.Conflict.ResolverSecret, cfg.Conflict.Timeout)

	srv := server.New(server.Options{
		Auth:    authenticator,
		Emitter: dispatcher,
		Metrics: reg,
		Limits:  cfg.Limits,
		CORS:    cfg.CORS,
		BaseCtx: ctx,
	})

	if nodeID == "" {
		nodeID = generateNodeID()
	}

	// In cluster mode the engine broadcasts through the cluster node, which
	// delivers locally (via the hub) and fans the op out to other nodes over
	// Redis. In single-node mode the hub is the broadcaster directly.
	var (
		broadcaster syncengine.Broadcaster = srv.Hub()
		clusterNode *cluster.Node
	)
	if cfg.Cluster.Mode {
		node, err := cluster.New(cluster.Options{
			Ctx:      ctx,
			RedisURL: cfg.Cluster.RedisURL,
			NodeID:   nodeID,
			Addr:     cfg.Addr(),
			Local:    srv.Hub(),
			Logger:   slog.Default(),
		})
		if err != nil {
			cancel()
			_ = store.Close()
			return nil, fmt.Errorf("join cluster: %w", err)
		}
		broadcaster = node
		srv.SetSubscriptionObserver(node)
		clusterNode = node
	}

	syncEng := syncengine.NewEngine(store, broadcaster, syncengine.Options{
		SnapshotInterval: cfg.Storage.SnapshotInterval,
		Resolver:         resolver,
		ResolverTimeout:  cfg.Conflict.Timeout,
		Emitter:          dispatcher,
	})
	srv.SetEngine(syncEng)

	restAPI := api.New(api.Options{
		Engine:  syncEng,
		Metrics: reg,
		Emitter: dispatcher,
		Config:  cfg,
		NodeID:  nodeID,
		Cluster: clusterNode,
	})

	return &Engine{
		cfg:     cfg,
		store:   store,
		api:     restAPI,
		srv:     srv,
		cluster: clusterNode,
		cancel:  cancel,
	}, nil
}

// Handler returns the complete HTTP handler: REST management API, health/ready
// probes, and the /sync WebSocket endpoint.
func (e *Engine) Handler() http.Handler {
	return e.api.Handler(e.srv.HandleSync)
}

// WebSocketHandler returns just the /sync WebSocket upgrade handler, for mounting
// at a custom path.
func (e *Engine) WebSocketHandler() http.HandlerFunc {
	return e.srv.HandleSync
}

// Config returns the resolved configuration.
func (e *Engine) Config() config.Config { return e.cfg }

// Close cancels live connections, leaves the cluster, and releases storage.
func (e *Engine) Close() error {
	e.cancel()
	if e.cluster != nil {
		if err := e.cluster.Close(); err != nil {
			e.store.Close()
			return fmt.Errorf("leave cluster: %w", err)
		}
	}
	return e.store.Close()
}
