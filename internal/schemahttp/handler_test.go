package schemahttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowServesSchemaWithStableETag(t *testing.T) {
	h := Handler{}
	first := httptest.NewRecorder()
	h.Workflow(first, httptest.NewRequest(http.MethodGet, "/.well-known/wuling/schemas/v1/workflow.json", nil))

	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "application/schema+json; charset=utf-8", first.Header().Get("Content-Type"))
	require.NotEmpty(t, first.Header().Get("ETag"))
	require.Contains(t, first.Body.String(), `"title": "Wuling DevOps workflow"`)

	cached := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/.well-known/wuling/schemas/v1/workflow.json", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	h.Workflow(cached, request)
	require.Equal(t, http.StatusNotModified, cached.Code)
	require.Empty(t, cached.Body.String())
}

func TestRunnerConfigServesEmbeddedSchema(t *testing.T) {
	h := Handler{}
	recorder := httptest.NewRecorder()
	h.RunnerConfig(recorder, httptest.NewRequest(http.MethodGet, "/.well-known/wuling/schemas/v1/runner-config.json", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/schema+json; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), `"title": "Wuling DevOps runner-config.yaml"`)
	require.Contains(t, recorder.Body.String(), `"runner_data_disk"`)
}

func TestWorkflowSchemaRequiresPoolForIsolatedMode(t *testing.T) {
	h := Handler{}
	recorder := httptest.NewRecorder()
	h.Workflow(recorder, httptest.NewRequest(http.MethodGet, "/.well-known/wuling/schemas/v1/workflow.json", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &schema))
	definitions := schema["definitions"].(map[string]any)
	execution := definitions["executionConfig"].(map[string]any)
	oneOf := execution["oneOf"].([]any)
	sharedBranch := oneOf[0].(map[string]any)
	sharedMode := sharedBranch["properties"].(map[string]any)["mode"].(map[string]any)
	require.ElementsMatch(t, []any{"shared", "exclusive"}, sharedMode["enum"].([]any))
	isolatedBranch := oneOf[1].(map[string]any)
	require.Contains(t, isolatedBranch["required"], "pool")
	require.Equal(t, "isolated", isolatedBranch["properties"].(map[string]any)["mode"].(map[string]any)["const"])
}
