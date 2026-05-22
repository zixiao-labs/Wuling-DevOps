// Package wikihttp wires HTTP handlers for the per-project Wiki:
// list, get, put, delete, and history endpoints over a project-scoped
// Markdown wiki backed by a dedicated bare Git repo (see internal/wikistore).
//
// Routes mount under
// "/api/v1/orgs/{org_slug}/projects/{project_slug}/wiki" so they nest under
// the same project hierarchy as repos / issues / MRs. Authorization:
//
//   - Any org member can read.
//   - Any org member can edit (PUT) or delete pages — wikis are
//     collaborative; we don't gate to owner/admin (matches GitHub's
//     behaviour for repo collaborators).
package wikihttp

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
	"github.com/zixiao-labs/wuling-devops/internal/wikistore"
)

// Handler wires wiki handlers.
type Handler struct {
	Users    *userstore.Store
	Wikis    *wikistore.Store
	Verifier *auth.Verifier
	// OAT resolves OAuth-provider access tokens (wloat_…) so third-party
	// OAuth clients can read/write wiki pages with a bearer. When nil,
	// OAT-shaped bearers are rejected with the standard 401.
	OAT auth.OATResolver
}

// md is the package-wide goldmark instance. GFM gives us tables, strikethrough,
// task lists, autolinks. Unsafe HTML is intentionally enabled in the renderer
// because we sanitize through bluemonday afterwards — disabling here would
// drop legitimate HTML the policy is happy to admit (e.g. <details>).
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps(), gmhtml.WithUnsafe()),
)

// sanitizer is the package-wide bluemonday policy. UGCPolicy is the right
// default for "user-generated content": permits formatting, links, lists,
// images, etc., but blocks scripts, event handlers, iframes, and form
// elements. We extend it minimally to keep heading anchors useful.
var sanitizer = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// goldmark auto-heading-id writes <h2 id="..."> — UGCPolicy allows id on
	// most block elements already, but pin the allowance explicitly so a
	// future policy revision doesn't silently strip anchors.
	p.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	return p
}()

// renderMarkdown converts raw Markdown bytes to sanitized HTML.
func renderMarkdown(raw []byte) string {
	var buf bytes.Buffer
	if err := md.Convert(raw, &buf); err != nil {
		// Goldmark only errors on writer failures, which a bytes.Buffer can't
		// produce; fall back to the raw text rather than crashing the request.
		return string(sanitizer.SanitizeBytes(raw))
	}
	return string(sanitizer.SanitizeBytes(buf.Bytes()))
}

// Mount registers routes under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	r.Route("/orgs/{org_slug}/projects/{project_slug}/wiki", func(r chi.Router) {
		r.Use(auth.MiddlewareBearer(auth.BearerResolver{JWT: h.Verifier, OAT: h.OAT}, false))
		r.Get("/pages", h.listPages)
		// chi's "*" wildcard matches the remainder of the path including
		// slashes, so "docs/usage.md" routes correctly to the same handler.
		r.Get("/pages/*", h.getPage)
		r.Put("/pages/*", h.putPage)
		r.Delete("/pages/*", h.deletePage)
		r.Get("/history", h.history)
	})
}

// ----------------------------------------------------------------------------
// authorization helpers (parallel to issuehttp.resolveProject)
// ----------------------------------------------------------------------------

type projectCtx struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	UserID    uuid.UUID
	Username  string
	Role      string
}

func (h *Handler) resolveProject(r *http.Request) (*projectCtx, error) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		return nil, err
	}
	org, err := h.Users.GetOrgBySlug(r.Context(), chi.URLParam(r, "org_slug"))
	if err != nil {
		return nil, err
	}
	role, err := h.Users.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		return nil, err
	}
	// Mirror issuehttp: hide org existence from non-members.
	if role == "" {
		return nil, apperr.NotFound("project")
	}
	project, err := h.Users.GetProjectBySlug(r.Context(), org.ID, chi.URLParam(r, "project_slug"))
	if err != nil {
		return nil, err
	}
	return &projectCtx{
		OrgID:     org.ID,
		ProjectID: project.ID,
		UserID:    id.UserID,
		Username:  id.Username,
		Role:      role,
	}, nil
}

// pagePathFrom extracts the wildcard match for /pages/* routes. chi exposes
// it as URLParam "*".
func pagePathFrom(r *http.Request) string {
	return chi.URLParam(r, "*")
}

// ----------------------------------------------------------------------------
// handlers
// ----------------------------------------------------------------------------

func (h *Handler) listPages(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	pages, err := h.Wikis.ListPages(pc.OrgID, pc.ProjectID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func (h *Handler) getPage(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	page := pagePathFrom(r)
	raw, commitOID, err := h.Wikis.GetPage(pc.OrgID, pc.ProjectID, page)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, model.WikiPageContent{
		Path:      page,
		Raw:       string(raw),
		HTML:      renderMarkdown(raw),
		CommitOID: commitOID,
	})
}

type putPageReq struct {
	Content string `json:"content" validate:"max=1048576"`
	Message string `json:"message" validate:"max=512"`
}

func (h *Handler) putPage(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req putPageReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	page := pagePathFrom(r)
	authorEmail := pc.Username + "@wiki.local"
	oid, err := h.Wikis.PutPage(pc.OrgID, pc.ProjectID, page,
		[]byte(req.Content), pc.Username, authorEmail, req.Message)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// Re-fetch so the response carries the canonical (cleaned) path + HTML.
	raw, _, gerr := h.Wikis.GetPage(pc.OrgID, pc.ProjectID, page)
	if gerr != nil {
		// Race against another writer? Surface the commit but no body.
		httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
			"path":       strings.TrimPrefix(page, "/"),
			"commit_oid": oid,
		})
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, model.WikiPageContent{
		Path:      strings.TrimPrefix(page, "/"),
		Raw:       string(raw),
		HTML:      renderMarkdown(raw),
		CommitOID: oid,
	})
}

func (h *Handler) deletePage(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	page := pagePathFrom(r)
	authorEmail := pc.Username + "@wiki.local"
	if _, err := h.Wikis.DeletePage(pc.OrgID, pc.ProjectID, page,
		pc.Username, authorEmail, ""); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, perr := strconv.Atoi(l); perr == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	commits, err := h.Wikis.History(pc.OrgID, pc.ProjectID, limit)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"commits": commits})
}
