package api

import "net/http"

// handleHealth is the liveness probe: the process is up and serving.
func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady is the readiness probe: dependencies (storage) are reachable.
func (a *API) handleReady(w http.ResponseWriter, _ *http.Request) {
	if err := a.store.HealthCheck(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unavailable",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
