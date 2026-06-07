// Package pipelinehttp exposes the user-facing Pipelines API: list/inspect
// runs, trigger a manual run, cancel a run, and read job logs (range + SSE).
//
// Routes nest under the org/project hierarchy like issues and MRs:
//
//	/api/v1/orgs/{org}/projects/{project}/pipelines/...
//
// Authorization: any org member can read runs/logs; developer-or-above can
// trigger and cancel (same gate as pushing code).
package pipelinehttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// Handler wires the user-facing Pipelines endpoints.
type Handler struct {
	Users       *userstore.Store
	Pipelines   *pipelinestore.Store
	Layout      *repostore.Layout
	Verifier    *auth.Verifier
	OAT         auth.OATResolver
	DefaultTier string
}

// Mount registers routes under "/api/v1".
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.MiddlewareBearer(auth.BearerResolver{JWT: h.Verifier, OAT: h.OAT}, false))
		r.Route("/orgs/{org_slug}/projects/{project_slug}/pipelines", func(r chi.Router) {
			r.Get("/runs", h.listRuns)
			r.Post("/runs", h.triggerRun)
			r.Get("/runs/{run_id}", h.getRun)
			r.Post("/runs/{run_id}/cancel", h.cancelRun)
			r.Get("/jobs/{job_id}/logs", h.getLogs)
			r.Get("/jobs/{job_id}/logs/stream", h.streamLogs)
			r.Get("/jobs/{job_id}/artifacts/{name}", h.downloadArtifact)
		})
	})
}

// projectCtx is the resolved org/project + caller role.
type projectCtx struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	UserID    uuid.UUID
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
	if !auth.CanReadOrg(role) {
		return nil, apperr.NotFound("project")
	}
	project, err := h.Users.GetProjectBySlug(r.Context(), org.ID, chi.URLParam(r, "project_slug"))
	if err != nil {
		return nil, err
	}
	return &projectCtx{OrgID: org.ID, ProjectID: project.ID, UserID: id.UserID, Role: role}, nil
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	q := r.URL.Query()
	f := pipelinestore.ListRunsFilter{Status: q.Get("status")}
	if repoSlug := strings.TrimSpace(q.Get("repo")); repoSlug != "" {
		repo, err := h.Users.GetRepoBySlug(r.Context(), pc.ProjectID, repoSlug)
		if err != nil {
			httpapi.RenderError(w, r, err)
			return
		}
		f.RepoID = repo.ID
	}
	if l := q.Get("limit"); l != "" {
		if n, perr := strconv.Atoi(l); perr == nil {
			f.Limit = n
		}
	}
	runs, err := h.Pipelines.ListRunsByProject(r.Context(), pc.ProjectID, f)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	runID, perr := uuid.Parse(chi.URLParam(r, "run_id"))
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid run id"))
		return
	}
	run, err := h.Pipelines.GetRunWithSteps(r.Context(), runID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if run.ProjectID != pc.ProjectID {
		httpapi.RenderError(w, r, apperr.NotFound("pipeline run"))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, run)
}

type triggerRunReq struct {
	Repo     string `json:"repo"     validate:"required"`
	Ref      string `json:"ref"      validate:"omitempty,max=255"`
	Workflow string `json:"workflow" validate:"required"`
}

// triggerRun manually dispatches one workflow file at a ref. The workflow must
// declare `workflow_dispatch` (GitHub-compatible manual-trigger opt-in).
func (h *Handler) triggerRun(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !auth.CanWriteRepo(pc.Role) {
		httpapi.RenderError(w, r, apperr.Forbidden("triggering pipelines requires developer or above"))
		return
	}
	var req triggerRunReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	repo, err := h.Users.GetRepoBySlug(r.Context(), pc.ProjectID, req.Repo)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	repoPath := h.Layout.Path(pc.OrgID, pc.ProjectID, repo.ID)

	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		ref = repo.DefaultBranch
	}
	sha, err := git.Resolve(repoPath, ref)
	if err != nil {
		if git.IsNotFound(err) {
			httpapi.RenderError(w, r, apperr.NotFound("ref "+ref))
			return
		}
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}

	discovered, err := pipeline.Discover(repoPath, sha)
	if err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	var target *pipeline.DiscoveredWorkflow
	for i := range discovered {
		if discovered[i].Path == req.Workflow {
			target = &discovered[i]
			break
		}
	}
	if target == nil {
		httpapi.RenderError(w, r, apperr.NotFound("workflow "+req.Workflow))
		return
	}
	if target.ParseErr != nil {
		httpapi.RenderError(w, r, apperr.Validation("workflow failed to parse: "+target.ParseErr.Error(), nil))
		return
	}
	if !target.Workflow.MatchEvent("manual", "") {
		httpapi.RenderError(w, r, apperr.Validation("workflow does not enable workflow_dispatch (manual trigger)", nil))
		return
	}

	run, err := h.Pipelines.CreateRun(r.Context(), pipelinestore.CreateRunParams{
		OrgID:         pc.OrgID,
		ProjectID:     pc.ProjectID,
		RepoID:        repo.ID,
		WorkflowPath:  target.Path,
		Event:         "manual",
		GitRef:        ref,
		CommitSHA:     sha,
		CommitMessage: firstLine(commitMessage(repoPath, sha)),
		TriggeredBy:   pc.UserID,
		Workflow:      target.Workflow,
		DefaultTier:   h.DefaultTier,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, run)
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if !auth.CanWriteRepo(pc.Role) {
		httpapi.RenderError(w, r, apperr.Forbidden("canceling pipelines requires developer or above"))
		return
	}
	runID, perr := uuid.Parse(chi.URLParam(r, "run_id"))
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid run id"))
		return
	}
	run, err := h.Pipelines.GetRun(r.Context(), runID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if run.ProjectID != pc.ProjectID {
		httpapi.RenderError(w, r, apperr.NotFound("pipeline run"))
		return
	}
	if err := h.Pipelines.CancelRun(r.Context(), runID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// commitMessage best-effort reads a commit's message; empty on any error so a
// missing message never blocks a trigger.
func commitMessage(repoPath, sha string) string {
	commits, err := git.Log(repoPath, sha, 1)
	if err != nil || len(commits) == 0 {
		return ""
	}
	return commits[0].Message
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
