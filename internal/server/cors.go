package server

import (
	"net/http"
	"strings"
)

// corsMiddleware is a minimal CORS implementation tailored to API + git smart
// HTTP. We don't pull in go-chi/cors because we want CORS to be off by default
// for /info/refs and friends (the Git CLI doesn't need preflight) and on for
// /api/v1.
func corsMiddleware(allowed []string) func(http.Handler) http.Handler {
	allowAny := false
	allowSet := map[string]struct{}{}
	for _, o := range allowed {
		o = strings.TrimSpace(o)
		if o == "*" {
			allowAny = true
		} else if o != "" {
			allowSet[o] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (allowAny || originAllowed(origin, allowSet)) {
				if allowAny {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					// Append "Origin" to Vary instead of overwriting it —
					// other middleware (compression, content negotiation)
					// may have already set Vary, and Set would clobber that
					// and break downstream caches.
					addVary(w, "Origin")
				}
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(origin string, allow map[string]struct{}) bool {
	_, ok := allow[origin]
	return ok
}

// addVary appends a token to the Vary header without clobbering existing
// values. Vary tokens are case-insensitive and comma-separated.
func addVary(w http.ResponseWriter, token string) {
	existing := w.Header().Values("Vary")
	for _, v := range existing {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return
			}
		}
	}
	w.Header().Add("Vary", token)
}
