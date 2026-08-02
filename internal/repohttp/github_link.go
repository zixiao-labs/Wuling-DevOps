package repohttp

import (
	"net/http"
	"strings"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/githubwebhook"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
)

type putGithubLinkReq struct {
	Owner          string `json:"owner"           validate:"required,min=1,max=128"`
	Name           string `json:"name"            validate:"required,min=1,max=128"`
	InstallationID int64  `json:"installation_id" validate:"required,gt=0"`
}

func (h *Handler) getGithubLink(w http.ResponseWriter, r *http.Request) {
	if h.GithubLinks == nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "github repo links are not configured"))
		return
	}
	repo, _, _, err := h.resolveAndCheck(r, PermRead)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	link, err := h.GithubLinks.GetByRepoID(r.Context(), repo.ID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if link == nil || !link.Active {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"linked": false})
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"linked":          true,
		"owner":           link.Owner,
		"name":            link.Name,
		"installation_id": link.InstallationID,
		"full_name":       link.Owner + "/" + link.Name,
	})
}

func (h *Handler) putGithubLink(w http.ResponseWriter, r *http.Request) {
	if h.GithubLinks == nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnavailable, "github repo links are not configured"))
		return
	}
	repo, projectID, orgID, err := h.resolveAndCheck(r, PermWrite)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	role, err := h.Store.MemberRole(r.Context(), orgID, id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !auth.CanManageMembers(role) {
		httpapi.RenderError(w, r, apperr.Forbidden("linking a GitHub repo requires maintainer or above"))
		return
	}
	var req putGithubLinkReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	link, err := h.GithubLinks.Upsert(r.Context(), githubwebhook.RepoLink{
		InstallationID: req.InstallationID,
		Owner:          strings.TrimSpace(req.Owner),
		Name:           strings.TrimSpace(req.Name),
		OrgID:          orgID,
		ProjectID:      projectID,
		RepoID:         repo.ID,
	})
	if err != nil {
		httpapi.RenderError(w, r, apperr.Wrap(apperr.CodeInternal, "upsert github link", err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"linked":          true,
		"owner":           link.Owner,
		"name":            link.Name,
		"installation_id": link.InstallationID,
		"full_name":       link.Owner + "/" + link.Name,
	})
}
