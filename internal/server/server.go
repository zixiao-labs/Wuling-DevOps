// Package server constructs the HTTP server (router + handlers + middleware).
// Kept separate from cmd/wuling-api so tests can spin up a server without
// going through the program's main().
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
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
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/mrhttp"
	"github.com/zixiao-labs/wuling-devops/internal/mrstore"
	"github.com/zixiao-labs/wuling-devops/internal/oauthhttp"
	"github.com/zixiao-labs/wuling-devops/internal/oauthstore"
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

	// OAuth provider wiring. The HMAC secret keys every minted token; in dev
	// we auto-mint a random one on boot so curl/`wuling-api` from a fresh
	// checkout works without manual env setup, while config validation
	// rejects an empty secret in production.
	oauthSecret := d.Cfg.OAuth.ProviderHMACSecret
	if oauthSecret == "" {
		oauthSecret = mustRandomHex(32)
		d.Log.Warn("oauth: generated ephemeral HMAC secret (set WULING_OAUTH_HMAC_SECRET to make stable across restarts)")
	}
	oauthStore := oauthstore.New(d.Pool)
	hasher := auth.NewHMACHasher(oauthSecret)
	oauthH := oauthhttp.New(oauthhttp.Handler{
		OAuth:  oauthStore,
		Users:  d.Store,
		Issuer: issuer,
		Hasher: hasher,
		Cfg:    d.Cfg.OAuth,
	})
	seedFirstPartyClient(context.Background(), d.Log, oauthStore, d.Cfg.OAuth.DesktopClientID)

	bearerResolver := auth.BearerResolver{JWT: verifier, OAT: oauthH}

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
			d.Log.Error("healthz db ping failed", "err", err)
			httpapi.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "db down"})
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"name": "wuling-api", "stage": 1})
	})

	// /.well-known/wuling-clients is the IdP discovery doc. Lives at the
	// root because clients look there before they know any /api paths.
	r.Get("/.well-known/wuling-clients", oauthH.WellKnownHandler())

	// JSON API at /api/v1.
	r.Route("/api/v1", func(api chi.Router) {
		authSub := chiSubrouter(api, "/auth")
		(&authhttp.Handler{
			Store: d.Store, Issuer: issuer, Verifier: verifier,
			OAT:    oauthH,
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

		// /admin subtree carries every admin-only endpoint (user management
		// from authhttp + OAuth client management from oauthhttp). Mount once,
		// share the JWT + admin guard.
		api.Route("/admin", func(adm chi.Router) {
			adm.Use(auth.Middleware(verifier, false))
			adm.Use(requireAdmin(d.Store))
			(&authhttp.AdminHandler{Store: d.Store, Verifier: verifier}).MountInner(adm)
			oauthH.MountAdmin(adm)
		})

		// OAuth Provider role. The token / authorize / device endpoints are
		// public (the protocol authenticates clients via client_secret + PKCE
		// + code/refresh tokens). The /apps and /authorizations endpoints
		// require a logged-in user, so they sit behind the Bearer middleware
		// and accept either a JWT (web UI) or an OAT (API client managing
		// its own grants).
		api.Route("/oauth", func(or chi.Router) {
			oauthH.Mount(or)
			or.Group(func(p chi.Router) {
				p.Use(auth.MiddlewareBearer(bearerResolver, false))
				oauthH.MountAuthed(p)
			})
		})

		(&orghttp.Handler{
			Store: d.Store, Verifier: verifier, OAT: oauthH,
		}).Mount(api)

		(&repohttp.Handler{
			Store: d.Store, Layout: d.Layout, Verifier: verifier, OAT: oauthH,
		}).Mount(api)

		(&issuehttp.Handler{
			Users: d.Store, Issues: d.Issues, Verifier: verifier, OAT: oauthH,
		}).Mount(api)

		(&mrhttp.Handler{
			Users: d.Store, MRs: d.MRs, Layout: d.Layout, Verifier: verifier, OAT: oauthH,
		}).Mount(api)

		if d.Wikis != nil {
			(&wikihttp.Handler{
				Users: d.Store, Wikis: d.Wikis, Verifier: verifier, OAT: oauthH,
			}).Mount(api)
		}

		if d.Insights != nil {
			(&insighthttp.Handler{
				Users: d.Store, Insights: d.Insights,
				Layout: d.Layout, Verifier: verifier, OAT: oauthH,
			}).Mount(api)
		}
	})

	// Git smart HTTP at the root, GitHub-style:
	//   /<org>/<proj>/<repo>.git/...
	gh := &githttp.Handler{
		Store:    d.Store,
		Layout:   d.Layout,
		Logger:   d.Log,
		PATReslv: &authhttp.PATResolver{Store: d.Store},
		OATReslv: oauthH,
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

// requireAdmin is the admin gate shared by every /api/v1/admin route. It
// loads the user fresh on each request so demotion takes effect immediately
// rather than after JWT rotation.
func requireAdmin(store *userstore.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := auth.RequireIdentity(r)
			if err != nil {
				httpapi.RenderError(w, r, err)
				return
			}
			u, err := store.GetUserByID(r.Context(), id.UserID)
			if err != nil {
				httpapi.RenderError(w, r, err)
				return
			}
			if !u.IsAdmin || !u.IsActive || u.ApprovalStatus != model.UserApprovalApproved {
				httpapi.RenderError(w, r, apperr.Forbidden("admin role required"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// seedFirstPartyClient guarantees an oauth_clients row exists for Esperanta
// and other "official" desktop apps. It runs on every boot so an operator
// can change WULING_OAUTH_DESKTOP_CLIENT_ID and we'll converge to that.
func seedFirstPartyClient(ctx context.Context, log *slog.Logger, store *oauthstore.Store, clientID string) {
	if clientID == "" {
		return
	}
	uid := uuid.Nil // unused; UpsertFirstPartyClient handles owner_user_id=NULL
	_ = uid
	_, err := store.UpsertFirstPartyClient(ctx, oauthstore.CreateClientParams{
		ClientID:    clientID,
		Name:        "Wuling Desktop",
		Description: "Official desktop client (Esperanta)",
		HomepageURL: "https://github.com/zixiao-labs/Kaltsit-Esperanta",
		RedirectURIs: []string{
			"http://127.0.0.1",
			"http://localhost",
			"urn:ietf:wg:oauth:2.0:oob",
		},
		DefaultScopes: []string{
			"user:read",
			"repo:read",
			"issue:read",
			"mr:read",
			"git:read",
			"git:write",
		},
	})
	if err != nil {
		log.Error("oauth: failed to seed first-party client", "err", err, "client_id", clientID)
		return
	}
	log.Info("oauth: seeded first-party client", "client_id", clientID)
}

// mustRandomHex returns 2*nBytes hex chars. Panic on failure — this is boot
// path and a /dev/urandom failure means the OS is hosed regardless.
func mustRandomHex(nBytes int) string {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		panic("rand.Read: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// chiSubrouter mounts a sub-router at prefix and returns it. We use this
// helper because chi's r.Route() takes a func, but our domain handlers want a
// chi.Router they can attach to.
func chiSubrouter(parent chi.Router, prefix string) chi.Router {
	sub := chi.NewRouter()
	parent.Mount(prefix, sub)
	return sub
}
