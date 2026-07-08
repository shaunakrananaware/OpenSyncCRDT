package api

import (
	"net/http"
	"strings"
)

// Handler builds the complete HTTP handler: the REST routes, the probes, and
// the WebSocket sync endpoint, wrapped in CORS and panic-recovery middleware.
// syncHandler is the server's /sync upgrade handler.
func (a *API) Handler(syncHandler http.HandlerFunc) http.Handler {
	mux := http.NewServeMux()
	a.Register(mux)
	mux.HandleFunc("/sync", syncHandler)
	return a.recover(a.cors(mux))
}

// cors applies CORS headers according to CORS_ALLOWED_ORIGINS and answers
// preflight requests.
func (a *API) cors(next http.Handler) http.Handler {
	allowed := a.cfg.CORS.AllowedOrigins
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allow := matchOrigin(allowed, origin); allow != "" {
			w.Header().Set("Access-Control-Allow-Origin", allow)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recover turns a panic in any handler into a 500 instead of crashing the
// server.
func (a *API) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.logger.Error("panic in handler", "error", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// matchOrigin returns the value to echo in Access-Control-Allow-Origin, or ""
// if the origin is not allowed.
func matchOrigin(allowed []string, origin string) string {
	for _, a := range allowed {
		if a == "*" {
			return "*"
		}
		if origin != "" && strings.EqualFold(a, origin) {
			return origin
		}
	}
	return ""
}
