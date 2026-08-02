package runnerhttp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/autoscale"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
)

type runnerConfigResponse struct {
	Content        string     `json:"content"`
	Exists         bool       `json:"exists"`
	BlobSHA        string     `json:"blob_sha"`
	CommitSHA      string     `json:"commit_sha"`
	Branch         string     `json:"branch"`
	Path           string     `json:"path"`
	ProjectSlug    string     `json:"project_slug"`
	RepoSlug       string     `json:"repo_slug"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
	UpdatedBy      string     `json:"updated_by,omitempty"`
	Valid          bool       `json:"valid"`
	ParseError     string     `json:"parse_error,omitempty"`
	Warnings       []string   `json:"warnings"`
	CreatedProject bool       `json:"created_project,omitempty"`
	CreatedRepo    bool       `json:"created_repo,omitempty"`
	Unchanged      bool       `json:"unchanged,omitempty"`
}

type putRunnerConfigReq struct {
	Content     string  `json:"content"       validate:"max=262144"`
	Message     string  `json:"message"       validate:"max=512"`
	BaseBlobSHA *string `json:"base_blob_sha"`
}

func (h *Handler) getRunnerConfig(w http.ResponseWriter, r *http.Request) {
	orgID, _, _, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	f, err := h.OrgConfig.Read(r.Context(), orgID, orgconfig.RunnerConfigPath)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if f.Exists() {
		w.Header().Set("ETag", strconv.Quote(f.BlobSHA))
	}
	httpapi.WriteJSON(w, http.StatusOK, h.buildConfigResponse(r.Context(), orgID, f))
}

func (h *Handler) putRunnerConfig(w http.ResponseWriter, r *http.Request) {
	orgID, userID, role, err := h.orgManageCtx(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !auth.CanManageMembers(role) {
		httpapi.RenderError(w, r, apperr.Forbidden("editing runner-config.yaml requires maintainer or above"))
		return
	}
	var req putRunnerConfigReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := validateBaseBlobSHA(req.BaseBlobSHA); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	base, err := resolvePrecondition(r, req.BaseBlobSHA)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	content := []byte(req.Content)
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		httpapi.RenderError(w, r, apperr.Validation("runner-config.yaml must be valid UTF-8 text", nil))
		return
	}
	cfg, perr := autoscale.Parse(content)
	if perr != nil {
		httpapi.RenderError(w, r, apperr.Validation("runner-config.yaml is invalid",
			map[string]any{"parse_error": perr.Error()}))
		return
	}
	user, err := h.Users.GetUserByID(r.Context(), userID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	res, err := h.OrgConfig.Write(r.Context(), orgconfig.WriteParams{
		OrgID:   orgID,
		Name:    orgconfig.RunnerConfigPath,
		Content: content,
		Message: commitMessage(req.Message),
		Author: git.Author{
			Name:  firstNonEmpty(user.DisplayName, user.Username),
			Email: firstNonEmpty(user.Email, user.Username+"@users.wuling.local"),
			When:  time.Now().UTC(),
		},
		BaseBlobSHA: base,
		EnsureRepo:  true,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}

	h.OrgConfig.Invalidate(orgID)

	resp := h.buildConfigResponse(r.Context(), orgID, res.File)
	resp.CreatedProject = res.CreatedProject
	resp.CreatedRepo = res.CreatedRepo
	resp.Unchanged = res.Unchanged
	resp.Valid = true
	resp.ParseError = ""
	resp.Warnings = h.configWarnings(r.Context(), orgID, cfg)
	if res.File.Exists() {
		w.Header().Set("ETag", strconv.Quote(res.File.BlobSHA))
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) buildConfigResponse(ctx context.Context, orgID uuid.UUID, f *orgconfig.File) runnerConfigResponse {
	resp := runnerConfigResponse{
		Content:     "",
		Exists:      f.Exists(),
		BlobSHA:     f.BlobSHA,
		CommitSHA:   f.CommitSHA,
		Branch:      f.Branch,
		Path:        orgconfig.RunnerConfigPath,
		ProjectSlug: h.OrgConfig.ProjectSlug,
		RepoSlug:    h.OrgConfig.RepoSlug,
		UpdatedBy:   f.UpdatedBy,
		Warnings:    []string{},
	}
	if f.Exists() {
		resp.Content = string(f.Content)
		cfg, perr := autoscale.Parse(f.Content)
		if perr != nil {
			resp.Valid = false
			resp.ParseError = perr.Error()
		} else {
			resp.Valid = true
			resp.Warnings = h.configWarnings(ctx, orgID, cfg)
		}
	}
	if !f.UpdatedAt.IsZero() {
		t := f.UpdatedAt
		resp.UpdatedAt = &t
	}
	return resp
}

func (h *Handler) configWarnings(ctx context.Context, orgID uuid.UUID, cfg *autoscale.Config) []string {
	out := []string{}
	if cfg.Version != 0 && cfg.Version != 1 {
		out = append(out, fmt.Sprintf("version %d is not recognised; the control plane implements version 1", cfg.Version))
	}
	if h.Secrets == nil {
		return out
	}
	secs, err := h.Secrets.ListOrg(ctx, orgID)
	if err != nil {
		return out
	}
	have := map[string]struct{}{}
	for _, s := range secs {
		have[s.Name] = struct{}{}
	}
	for i := range cfg.Pools {
		p := cfg.Pools[i]
		if _, ok := have[p.CredentialSecretName()]; !ok {
			out = append(out, fmt.Sprintf(
				"pool %q references credentials_secret %q, which is not an org secret — the autoscaler will skip this pool until you add it",
				p.Name, p.CredentialSecretName()))
		}
		if p.Provider == "proxmox" || p.Provider == "vcenter" {
			out = append(out, fmt.Sprintf(
				"pool %q uses provider %q, whose VM provisioning is a placeholder in this build; the autoscaler will log a warning and skip it",
				p.Name, p.Provider))
		}
	}
	return out
}

func validateBaseBlobSHA(s *string) error {
	if s == nil || *s == "" {
		return nil
	}
	if len(*s) != 40 {
		return apperr.Validation("base_blob_sha must be 40 hex characters or empty", nil)
	}
	for _, c := range *s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return apperr.Validation("base_blob_sha must be 40 hex characters or empty", nil)
		}
	}
	return nil
}

func resolvePrecondition(r *http.Request, body *string) (*string, error) {
	header := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	switch {
	case header == "*":
		return nil, apperr.New(apperr.CodeBadRequest,
			`If-Match: * is not accepted; echo blob_sha from GET, or send base_blob_sha:"" to create the file`)
	case header != "" && body != nil && !strings.EqualFold(header, *body):
		return nil, apperr.New(apperr.CodeBadRequest, "If-Match and base_blob_sha disagree")
	case header != "":
		h := strings.ToLower(header)
		return &h, nil
	case body != nil:
		b := strings.ToLower(*body)
		return &b, nil
	}
	return nil, apperr.New(apperr.CodeBadRequest,
		`base_blob_sha (or If-Match) is required; GET this endpoint first and echo blob_sha, or send "" to create the file`)
}

func commitMessage(userMsg string) string {
	if m := strings.TrimSpace(userMsg); m != "" {
		return m
	}
	return "Update " + orgconfig.RunnerConfigPath
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
