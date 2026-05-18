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
	"github.com/zixiao-labs/wuling-devops/internal/insighthttp"
	"github.com/zixiao-labs/wuling-devops/internal/insightstore"
	"github.com/zixiao-labs/wuling-devops/internal/issuehttp"
	"github.com/zixiao-labs/wuling-devops/internal/issuestore"
	"github.com/zixiao-labs/wuling-devops/internal/mrhttp"
	"github.com/zixiao-labs/wuling-devops/internal/mrstore"
	"github.com/zixiao-labs/wuling-devops/internal/orghttp"
	"github.com/zixiao-labs/wuling-devops/internal/repohttp"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
	"github.com/zixiao-labs/wuling-devops/internal/wikihttp"
	"github.com/zixiao-labs/wuling-devops/internal/wikistore"
)

// Deps bundles everything the HTTP server needs.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Pool     *db.Pool
	Store    *userstore.Store
	Issues   *issuestore.Store
	MRs      *mrstore.Store
	Wikis    *wikistore.Store
	Insights *insightstore.Store
	Layout   *repostore.Layout
}

// New returns a router fully wired with all current Stage-1 domains.
func New(d Deps) http.Handler {
	// Fail-fast nil checks for critical dependencies
	if d.Issues == nil {
		panic("server.Deps.Issues cannot be nil")
	}
	if d.MRs == nil {
		panic("server.Deps.MRs cannot be nil")
	}
	if d.Layout == nil {
		panic("server.Deps.Layout cannot be nil")
	}

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
		authSub := chiSubrouter(api, "/auth")
		(&authhttp.Handler{
			Store: d.Store, Issuer: issuer, Verifier: verifier,
			Signup: struct{ RequireApproval bool }{RequireApproval: d.Cfg.Signup.RequireApproval},
		}).Mount(authSub)
		(&authhttp.SSHKeyHandler{
			Store: d.Store, Verifier: verifier,
		}).Mount(authSub)
		(&authhttp.OAuthHandler{
			Store: d.Store, Issuer: issuer,
			Cfg:    d.Cfg.OAuth,
			Signup: d.Cfg.Signup,
			JWT:    d.Cfg.JWT,
		}).Mount(authSub)
		(&authhttp.AdminHandler{
			Store: d.Store, Verifier: verifier,
		}).Mount(api)

		(&orghttp.Handler{
			Store: d.Store, Verifier: verifier,
		}).Mount(api)

		(&repohttp.Handler{
			Store: d.Store, Layout: d.Layout, Verifier: verifier,
		}).Mount(api)

		(&issuehttp.Handler{
			Users: d.Store, Issues: d.Issues, Verifier: verifier,
		}).Mount(api)

		(&mrhttp.Handler{
			Users: d.Store, MRs: d.MRs, Layout: d.Layout, Verifier: verifier,
		}).Mount(api)

		if d.Wikis != nil {
			(&wikihttp.Handler{
				Users: d.Store, Wikis: d.Wikis, Verifier: verifier,
			}).Mount(api)
		}

		if d.Insights != nil {
			(&insighthttp.Handler{
				Users: d.Store, Insights: d.Insights,
				Layout: d.Layout, Verifier: verifier,
			}).Mount(api)
		}
	})

	// Git smart HTTP at the root, GitHub-style:
	//   /<org>/<proj>/<repo>.git/...
	gh := &githttp.Handler{
		Store:    d.Store,
		Layout:   d.Layout,
		Logger:   d.Log,
		PWReslv:  &authhttp.PasswordResolver{Store: d.Store},
		PATReslv: &authhttp.PATResolver{Store: d.Store},
	}
	// Assigning `d.Insights` (a *insightstore.Store) directly to an interface
	// field would produce a typed-nil even when d.Insights is nil, and the
	// nil-check inside Indexer's caller would falsely succeed. Branch here.
	if d.Insights != nil {
		gh.Indexer = d.Insights
	}
	gh.Mount(r)

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
