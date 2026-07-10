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

// GET /api/v1/nodes — cluster node listing. In cluster mode this lists every
// currently-alive node from the Redis registry; in single-node mode it reports
// just this node.
func (a *API) handleNodes(w http.ResponseWriter, r *http.Request) {
	if a.cluster != nil {
		infos, err := a.cluster.Nodes(r.Context())
		if err != nil {
			a.logger.Error("list cluster nodes", "error", err)
			writeError(w, http.StatusServiceUnavailable, "cluster registry unavailable")
			return
		}
		nodes := make([]map[string]any, 0, len(infos))
		for _, n := range infos {
			nodes = append(nodes, map[string]any{
				"id":         n.ID,
				"addr":       n.Addr,
				"started_at": n.StartedAt.UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"cluster_mode": true,
			"nodes":        nodes,
		})
		return
	}

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
