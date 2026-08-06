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

var (
	workflowETag     = etagFor(schemas.WorkflowV1)
	runnerConfigETag = etagFor(schemas.RunnerConfigV1)
)

func etagFor(content []byte) string {
	sum := sha256.Sum256(content)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// Workflow serves the workflow schema.
func (Handler) Workflow(w http.ResponseWriter, r *http.Request) {
	serve(w, r, schemas.WorkflowV1, workflowETag)
}

// RunnerConfig serves the runner-config schema.
func (Handler) RunnerConfig(w http.ResponseWriter, r *http.Request) {
	serve(w, r, schemas.RunnerConfigV1, runnerConfigETag)
}

func serve(w http.ResponseWriter, r *http.Request, content []byte, etag string) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if ifNoneMatchMatches(r.Header.Get("If-None-Match"), etag) {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json; charset=utf-8")
	w.Header().Set("ETag", etag)
	_, _ = w.Write(content)
}

// ifNoneMatchMatches implements weak validator comparison for comma-separated
// If-None-Match entity tags, including the "*" wildcard.
func ifNoneMatchMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(part)
		if tag == "*" {
			return true
		}
		tag = strings.TrimPrefix(tag, "W/")
		etagStrong := strings.TrimPrefix(etag, "W/")
		if tag == etag || tag == etagStrong {
			return true
		}
	}
	return false
}
