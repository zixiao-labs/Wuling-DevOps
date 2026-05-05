// Package server constructs the HTTP server (router + handlers + middleware).
// Kept separate from cmd/wuling-api so tests can spin up a server without
// going through the program's main().
package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/authhttp"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/githttp"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/orghttp"
	"github.com/zixiao-labs/wuling-devops/internal/repohttp"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Deps bundles everything the HTTP server needs.
type Deps struct {
	Cfg    *config.Config
	Log    *slog.Logger
	Pool   *db.Pool
	Store  *userstore.Store
	Layout *repostore.Layout
}

// New returns a router fully wired with all current Stage-1 domains.
func New(d Deps) http.Handler {
	verifier := auth.NewVerifier(d.Cfg.JWT)
	issuer := auth.NewIssuer(d.Cfg.JWT)

	r := chi.NewRouter()
	r.Use(httpapi.RequestIDMiddleware)
	r.Use(httpapi.LoggingMiddleware(d.Log))
	r.Use(httpapi.RecoverMiddleware(d.Log))
	r.Use(corsMiddleware(d.Cfg.HTTP.CORSOrigins))

	// Health endpoints are unauthenticated.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*1e9)
		defer cancel()
		if err := d.Pool.Ping(ctx); err != nil {
			// Log full driver error server-side, but only return a generic
			// "db down" to clients — leaking err.Error() can expose DSNs,
			// connection-string bits, or internal hostnames.
			d.Log.Error("healthz db ping failed", "err", err)
			httpapi.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "db down"})
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"name": "wuling-api", "stage": 1})
	})

	// JSON API at /api/v1.
	r.Route("/api/v1", func(api chi.Router) {
		(&authhttp.Handler{
			Store: d.Store, Issuer: issuer, Verifier: verifier,
		}).Mount(chiSubrouter(api, "/auth"))

		(&orghttp.Handler{
			Store: d.Store, Verifier: verifier,
		}).Mount(api)

		(&repohttp.Handler{
			Store: d.Store, Layout: d.Layout, Verifier: verifier,
		}).Mount(api)
	})

	// Git smart HTTP at the root, GitHub-style:
	//   /<org>/<proj>/<repo>.git/...
	(&githttp.Handler{
		Store:    d.Store,
		Layout:   d.Layout,
		Logger:   d.Log,
		PWReslv:  &authhttp.PasswordResolver{Store: d.Store},
		PATReslv: &authhttp.PATResolver{Store: d.Store},
	}).Mount(r)

	return r
}

// chiSubrouter mounts a sub-router at prefix and returns it. We use this
// helper because chi's r.Route() takes a func, but our domain handlers want a
// chi.Router they can attach to.
func chiSubrouter(parent chi.Router, prefix string) chi.Router {
	sub := chi.NewRouter()
	parent.Mount(prefix, sub)
	return sub
}
