package runnerhttp

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
)

// MaxLogChunkBytes caps one log append. The runner flushes more often than
// this; the cap just bounds a single request.
const MaxLogChunkBytes = 1 << 20 // 1 MiB

// ----------------------------------------------------------------------------
// register
// ----------------------------------------------------------------------------

type registerReq struct {
	Token  string   `json:"token"  validate:"required"`
	Name   string   `json:"name"   validate:"omitempty,max=128"`
	Labels []string `json:"labels" validate:"omitempty,dive,max=64"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	runner, err := h.Runners.Register(r.Context(), runnerstore.RegisterParams{
		RawToken: req.Token, Name: req.Name, Labels: req.Labels,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// runner.Token is the raw wlrt_ token — shown once, here.
	httpapi.WriteJSON(w, http.StatusCreated, runner)
}

// ----------------------------------------------------------------------------
// heartbeat
// ----------------------------------------------------------------------------

type heartbeatReq struct {
	Status string `json:"status" validate:"omitempty,oneof=idle busy"`
}

func (h *Handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	ri := runnerFromCtx(r)
	var req heartbeatReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := h.Runners.Heartbeat(r.Context(), ri.RunnerID, req.Status); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ----------------------------------------------------------------------------
// acquire
// ----------------------------------------------------------------------------

type checkoutInfo struct {
	CloneURL string `json:"clone_url"`
	Ref      string `json:"ref"`
	SHA      string `json:"sha"`
}

type acquireResponse struct {
	*pipelinestore.AcquiredJob
	Secrets  map[string]string `json:"secrets"`
	Checkout checkoutInfo      `json:"checkout"`
}

// acquire claims the next job for the runner. Returns 204 when the queue has
// nothing for it (the runner polls again). On success the response carries the
// decrypted secret set and a clone URL — the runner authenticates the clone
// with its own wlrt_ token (read-only, org-scoped; see githttp).
func (h *Handler) acquire(w http.ResponseWriter, r *http.Request) {
	ri := runnerFromCtx(r)
	aj, err := h.Pipelines.AcquireJob(r.Context(), ri.RunnerID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if aj == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	secrets, err := h.Secrets.ResolveForProject(r.Context(), aj.OrgID, aj.ProjectID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	resp := acquireResponse{
		AcquiredJob: aj,
		Secrets:     secrets,
		Checkout: checkoutInfo{
			CloneURL: h.cloneURL(r, aj.OrgSlug, aj.ProjectSlug, aj.RepoSlug),
			Ref:      aj.GitRef,
			SHA:      aj.CommitSHA,
		},
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

// ----------------------------------------------------------------------------
// job callbacks (log / step / complete / artifact)
// ----------------------------------------------------------------------------

// ownedJob loads the job's context and verifies it is assigned to the calling
// runner. A job that has been canceled/reassigned returns 409 so the runner
// aborts promptly.
func (h *Handler) ownedJob(r *http.Request) (*pipelinestore.JobContext, error) {
	ri := runnerFromCtx(r)
	jobID, perr := uuid.Parse(chi.URLParam(r, "job_id"))
	if perr != nil {
		return nil, apperr.New(apperr.CodeBadRequest, "invalid job id")
	}
	jc, err := h.Pipelines.JobCtx(r.Context(), jobID)
	if err != nil {
		return nil, err
	}
	if jc.RunnerID == nil || *jc.RunnerID != ri.RunnerID {
		return nil, apperr.Forbidden("job not assigned to this runner")
	}
	if jc.Status != "running" {
		return nil, apperr.New(apperr.CodeConflict, "job is no longer running")
	}
	return jc, nil
}

func (h *Handler) appendLog(w http.ResponseWriter, r *http.Request) {
	jc, err := h.ownedJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	body := http.MaxBytesReader(w, r.Body, MaxLogChunkBytes)
	data, rerr := io.ReadAll(body)
	if rerr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "log chunk too large"))
		return
	}
	size, err := h.Pipelines.AppendLog(r.Context(), jc.JobID, data)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"size": size})
}

type patchStepReq struct {
	Status string `json:"status" validate:"required,oneof=running success failed canceled skipped"`
}

func (h *Handler) patchStep(w http.ResponseWriter, r *http.Request) {
	jc, err := h.ownedJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	number, perr := strconv.Atoi(chi.URLParam(r, "number"))
	if perr != nil || number < 1 {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid step number"))
		return
	}
	var req patchStepReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := h.Pipelines.UpdateStep(r.Context(), pipelinestore.UpdateStepParams{
		JobID: jc.JobID, Number: number, Status: req.Status,
	}); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type completeReq struct {
	Conclusion string `json:"conclusion" validate:"required,oneof=success failed canceled"`
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	jc, err := h.ownedJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req completeReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if err := h.Pipelines.CompleteJob(r.Context(), jc.JobID, req.Conclusion); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) uploadArtifact(w http.ResponseWriter, r *http.Request) {
	jc, err := h.ownedJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	name := chi.URLParam(r, "name")
	body := http.MaxBytesReader(w, r.Body, pipelinestore.MaxArtifactBytes+1)
	size, err := h.Pipelines.SaveArtifact(jc.JobID, name, body)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"name": name, "size": size})
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func (h *Handler) cloneURL(r *http.Request, org, project, repo string) string {
	base := h.CloneBaseURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
			scheme = p
		}
		base = scheme + "://" + r.Host
	}
	return strings.TrimRight(base, "/") + "/" + org + "/" + project + "/" + repo + ".git"
}
