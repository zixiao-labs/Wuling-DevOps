package pipelinehttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
)

// resolveJob resolves the caller's project context, parses {job_id}, and
// verifies the job belongs to that project (so logs can't be read cross-tenant
// by guessing a UUID).
func (h *Handler) resolveJob(r *http.Request) (*pipelinestore.JobContext, error) {
	pc, err := h.resolveProject(r)
	if err != nil {
		return nil, err
	}
	jobID, perr := uuid.Parse(chi.URLParam(r, "job_id"))
	if perr != nil {
		return nil, apperr.New(apperr.CodeBadRequest, "invalid job id")
	}
	jc, err := h.Pipelines.JobCtx(r.Context(), jobID)
	if err != nil {
		return nil, err
	}
	if jc.ProjectID != pc.ProjectID {
		return nil, apperr.NotFound("job")
	}
	return jc, nil
}

func (h *Handler) getLogs(w http.ResponseWriter, r *http.Request) {
	jc, err := h.resolveJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	data, next, err := h.Pipelines.ReadLog(r.Context(), jc.JobID, offset, limit)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"data":    string(data),
		"offset":  next,
		"status":  jc.Status,
		"is_done": isTerminal(jc.Status),
	})
}

// streamLogs tails a job's log via Server-Sent Events. Each event is a JSON
// object {offset, data}; a final {done:true} event closes the stream once the
// job is terminal and the log has been fully drained. The frontend appends
// each chunk and stops on done.
func (h *Handler) streamLogs(w http.ResponseWriter, r *http.Request) {
	jc, err := h.resolveJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeUnsupported, "streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)

	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	jobID := jc.JobID
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	emit := func(payload map[string]any) bool {
		b, _ := json.Marshal(payload)
		if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		data, next, err := h.Pipelines.ReadLog(r.Context(), jobID, offset, pipelinestore.MaxLogReadBytes)
		if err != nil {
			return
		}
		if len(data) > 0 {
			if !emit(map[string]any{"offset": next, "data": string(data)}) {
				return
			}
			offset = next
		}
		// Re-read job status each tick to detect terminal state.
		cur, err := h.Pipelines.JobCtx(r.Context(), jobID)
		if err != nil {
			return
		}
		if isTerminal(cur.Status) && len(data) == 0 {
			emit(map[string]any{"done": true, "status": cur.Status})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) downloadArtifact(w http.ResponseWriter, r *http.Request) {
	jc, err := h.resolveJob(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	name := chi.URLParam(r, "name")
	f, err := h.Pipelines.OpenArtifact(jc.JobID, name)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	defer f.Close()
	// Build a safe Content-Disposition. The ASCII `filename` is reduced to
	// printable ASCII minus quote/backslash (defeats header injection via CR/LF
	// and breakage from quotes); the RFC 5987 `filename*` carries the real,
	// possibly non-ASCII name. name is a single route path component.
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if ascii == "" {
		ascii = "artifact"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", ascii, url.PathEscape(name)))
	_, _ = io.Copy(w, f)
}

func isTerminal(status string) bool {
	switch status {
	case "success", "failed", "canceled":
		return true
	}
	return false
}
