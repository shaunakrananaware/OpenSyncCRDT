package api

import (
	"net/http"
	"time"
)

// GET /api/v1/metrics — Prometheus text exposition format.
func (a *API) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if a.metrics != nil {
		_, _ = w.Write([]byte(a.metrics.Render()))
	}
}

// GET /api/v1/nodes — cluster node listing. In single-node mode this reports
// just this node.
func (a *API) handleNodes(w http.ResponseWriter, _ *http.Request) {
	node := map[string]any{
		"id":         a.nodeID,
		"addr":       a.cfg.Addr(),
		"started_at": a.startedAt.UTC().Format(time.RFC3339),
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster_mode": a.cfg.Cluster.Mode,
		"nodes":        []map[string]any{node},
	})
}
