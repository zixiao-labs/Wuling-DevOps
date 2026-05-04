// Package githttp implements the Git smart HTTP protocol via subprocess
// piping into the upstream `git-upload-pack` and `git-receive-pack` binaries.
//
// Why subprocess instead of pure libgit2? libgit2 doesn't natively serve the
// smart HTTP protocol — you'd have to assemble pkt-line, ref advertisement,
// and pack negotiation by hand on top of its primitives. Shelling out to git
// is what Gitea/Forgejo/Gerrit all do for the same reason: it's the reference
// implementation, security-reviewed, fast, and one binary upgrade away from
// staying current with protocol-v2 evolutions.
//
// libgit2 still owns the metadata read APIs (refs/tree/blob/log) — those need
// in-process access for the JSON endpoints, and shelling out for them would
// incur fork overhead per request.
//
// Endpoints handled here:
//
//	GET  /<org>/<proj>/<repo>.git/info/refs?service=git-upload-pack
//	GET  /<org>/<proj>/<repo>.git/info/refs?service=git-receive-pack
//	POST /<org>/<proj>/<repo>.git/git-upload-pack
//	POST /<org>/<proj>/<repo>.git/git-receive-pack
//
// Authentication is HTTP Basic. The password may be a PAT (preferred,
// recognised by the "wlpat_" prefix) or the user's account password
// (allowed for compatibility; can be disabled in config later).
package githttp

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler hosts the smart-HTTP routes.
type Handler struct {
	Store    *userstore.Store
	Layout   *repostore.Layout
	PWReslv  auth.PasswordResolver
	PATReslv auth.PATResolver

	// GitBinary lets ops override the path (e.g. for a vendored git). Empty =
	// look up "git" on PATH.
	GitBinary string
}

// Mount registers the smart-HTTP routes. Path style is GitHub-compatible:
//
//	GET/POST /<org>/<proj>/<repo>.git/...
func (h *Handler) Mount(r chi.Router) {
	r.Get("/{org_slug}/{project_slug}/{repo_dot_git}/info/refs", h.infoRefs)
	r.Post("/{org_slug}/{project_slug}/{repo_dot_git}/git-upload-pack", h.uploadPack)
	r.Post("/{org_slug}/{project_slug}/{repo_dot_git}/git-receive-pack", h.receivePack)
}

// resolveRepoFromURL trims the trailing ".git" and looks the repo up.
// Permission checks are also done here.
func (h *Handler) resolveRepoFromURL(r *http.Request, needWrite bool) (*authedRequest, error) {
	repoSeg := chi.URLParam(r, "repo_dot_git")
	repoSlug := strings.TrimSuffix(repoSeg, ".git")
	if repoSlug == repoSeg {
		// Some clients omit the .git suffix on the URL; we still serve them.
		repoSlug = repoSeg
	}

	repo, projectID, orgID, err := h.Store.ResolveRepoPath(
		r.Context(),
		chi.URLParam(r, "org_slug"),
		chi.URLParam(r, "project_slug"),
		repoSlug,
	)
	if err != nil {
		return nil, err
	}

	identity, err := h.authenticate(r)
	if err != nil {
		// For public repos and read-only operations, allow anonymous fetch.
		// (Stage 1: public means anyone authenticated; we'll relax to truly
		// anonymous in a later stage with rate limiting.)
		if !needWrite && repo.Visibility == "public" {
			identity = nil
		} else {
			return nil, err
		}
	}

	if identity != nil {
		role, err := h.Store.MemberRole(r.Context(), orgID, identity.UserID)
		if err != nil {
			return nil, err
		}
		if role == "" && repo.Visibility != "public" {
			return nil, apperr.NotFound("repo")
		}
		if needWrite && role == "" {
			return nil, apperr.Forbidden("write access required")
		}
		// PAT scope check: writes require repo:write.
		if needWrite && identity.Source == auth.IdentitySourcePAT {
			if !hasScope(identity.Scopes, "repo:write") {
				return nil, apperr.Forbidden("token lacks repo:write scope")
			}
		}
	} else if needWrite {
		// anonymous + write -> deny
		return nil, apperr.Unauthorized("authentication required")
	}

	return &authedRequest{
		Repo:      repo,
		RepoPath:  h.Layout.Path(orgID, projectID, repo.ID),
		Identity:  identity,
	}, nil
}

type authedRequest struct {
	Repo     *model.Repo
	RepoPath string
	Identity *auth.Identity
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// authenticate parses HTTP Basic auth and returns the resolved identity.
//
// We accept two password formats:
//
//   - A wlpat_ PAT (preferred). Resolved by PATResolver.
//   - The user's account password. Resolved by PasswordResolver.
//
// Bearer auth is *not* accepted on smart-HTTP endpoints because no major Git
// CLI sends it that way. This is intentional and not a bug.
func (h *Handler) authenticate(r *http.Request) (*auth.Identity, error) {
	authz := r.Header.Get("Authorization")
	if authz == "" || !strings.HasPrefix(authz, "Basic ") {
		return nil, apperr.Unauthorized("HTTP Basic auth required")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authz, "Basic "))
	if err != nil {
		return nil, apperr.Unauthorized("invalid auth header")
	}
	idx := strings.IndexByte(string(raw), ':')
	if idx < 0 {
		return nil, apperr.Unauthorized("invalid auth header")
	}
	username := string(raw[:idx])
	password := string(raw[idx+1:])
	if username == "" || password == "" {
		return nil, apperr.Unauthorized("missing credentials")
	}
	if strings.HasPrefix(password, auth.AccessTokenPrefix) {
		return h.PATReslv.ResolvePAT(r.Context(), username, password)
	}
	return h.PWReslv.ResolvePassword(r.Context(), username, password)
}

// ----------------------------------------------------------------------------
// /info/refs
// ----------------------------------------------------------------------------

func (h *Handler) infoRefs(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service != "git-upload-pack" && service != "git-receive-pack" {
		writeErr(w, r, apperr.New(apperr.CodeBadRequest, "service must be git-upload-pack or git-receive-pack"))
		return
	}
	needWrite := service == "git-receive-pack"
	ar, err := h.resolveRepoFromURL(r, needWrite)
	if err != nil {
		writeErr(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Pragma", "no-cache")

	// pkt-line preamble for smart HTTP.
	prefix := "# service=" + service + "\n"
	if _, err := w.Write([]byte(pktLine(prefix) + "0000")); err != nil {
		return
	}

	subcmd := strings.TrimPrefix(service, "git-") // upload-pack | receive-pack
	cmd := h.gitCommand(r.Context(), subcmd, "--stateless-rpc", "--advertise-refs", ar.RepoPath)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Headers are already on the wire; we can't switch to JSON here.
		// Just close the response.
		return
	}
}

// pktLine wraps payload in Git's pkt-line framing: 4-hex-len + payload.
func pktLine(payload string) string {
	const hex = "0123456789abcdef"
	n := len(payload) + 4
	out := []byte{
		hex[(n>>12)&0xf],
		hex[(n>>8)&0xf],
		hex[(n>>4)&0xf],
		hex[n&0xf],
	}
	return string(out) + payload
}

// ----------------------------------------------------------------------------
// /git-upload-pack and /git-receive-pack
// ----------------------------------------------------------------------------

func (h *Handler) uploadPack(w http.ResponseWriter, r *http.Request) {
	h.servicePack(w, r, "upload-pack", false)
}

func (h *Handler) receivePack(w http.ResponseWriter, r *http.Request) {
	h.servicePack(w, r, "receive-pack", true)
}

func (h *Handler) servicePack(w http.ResponseWriter, r *http.Request, sub string, needWrite bool) {
	if ct := r.Header.Get("Content-Type"); ct != "application/x-git-"+sub+"-request" {
		writeErr(w, r, apperr.New(apperr.CodeBadRequest, "unexpected Content-Type"))
		return
	}
	ar, err := h.resolveRepoFromURL(r, needWrite)
	if err != nil {
		writeErr(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-"+sub+"-result")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Pragma", "no-cache")

	body := r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := newGzipReader(body)
		if err != nil {
			writeErr(w, r, apperr.New(apperr.CodeBadRequest, "invalid gzip body"))
			return
		}
		defer gz.Close()
		body = gz
	}

	cmd := h.gitCommand(r.Context(), sub, "--stateless-rpc", ar.RepoPath)
	cmd.Stdin = body
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Headers already sent.
		return
	}

	// On a successful push, mark the repo non-empty.
	if needWrite {
		_ = h.Store.MarkRepoNotEmpty(r.Context(), ar.Repo.ID)
	}
}

// gitCommand builds an exec.Cmd for a git sub-command, isolating the
// subprocess environment.
func (h *Handler) gitCommand(ctx context.Context, sub string, args ...string) *exec.Cmd {
	bin := h.GitBinary
	if bin == "" {
		bin = "git"
	}
	full := append([]string{sub}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	// Empty env to keep the host environment from leaking; only PATH retained.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8"}
	return cmd
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// writeErr renders an error to a smart-HTTP response. The response is JSON
// only if headers haven't been written yet; otherwise we just close.
func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	e := apperr.As(err)
	if e == nil {
		e = apperr.Internal(err)
	}
	if e.Code == apperr.CodeUnauthorized || e.Code == apperr.CodeAuthentication {
		w.Header().Set("WWW-Authenticate", `Basic realm="wuling-git", charset="UTF-8"`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.HTTPStatus())
	_, _ = io.WriteString(w, `{"error":{"code":"`+string(e.Code)+`","message":"`+escape(e.Message)+`"}}`)
}

func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return r.Replace(s)
}

// errors guard — keep package usable even if some imports trim out.
var _ = errors.New
