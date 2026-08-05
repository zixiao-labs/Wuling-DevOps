// Package schemahttp serves versioned YAML Language Server JSON Schemas.
package schemahttp

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/zixiao-labs/wuling-devops/schemas"
)

// Handler exposes static, public schemas under /.well-known. Schema documents
// are embedded into the API binary so external repositories can reference a
// stable URL without depending on the source checkout being present.
type Handler struct{}

// Workflow serves the workflow schema.
func (Handler) Workflow(w http.ResponseWriter, r *http.Request) {
	serve(w, r, schemas.WorkflowV1)
}

// RunnerConfig serves the runner-config schema.
func (Handler) RunnerConfig(w http.ResponseWriter, r *http.Request) {
	serve(w, r, schemas.RunnerConfigV1)
}

func serve(w http.ResponseWriter, r *http.Request, content []byte) {
	sum := sha256.Sum256(content)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("ETag", etag)
	_, _ = w.Write(content)
}
