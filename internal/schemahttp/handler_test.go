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
	var firstDoc map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstDoc))
	require.Equal(t, "Wuling DevOps workflow", firstDoc["title"])

	cached := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/.well-known/wuling/schemas/v1/workflow.json", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	h.Workflow(cached, request)
	require.Equal(t, http.StatusNotModified, cached.Code)
	require.Equal(t, "public, max-age=3600", cached.Header().Get("Cache-Control"))
	require.Empty(t, cached.Body.String())
}

func TestRunnerConfigServesEmbeddedSchema(t *testing.T) {
	h := Handler{}
	recorder := httptest.NewRecorder()
	h.RunnerConfig(recorder, httptest.NewRequest(http.MethodGet, "/.well-known/wuling/schemas/v1/runner-config.json", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/schema+json; charset=utf-8", recorder.Header().Get("Content-Type"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &doc))
	require.Equal(t, "Wuling DevOps runner-config.yaml", doc["title"])
	require.Contains(t, recorder.Body.String(), `"runner_data_disk"`)
}

func TestWorkflowSchemaRequiresPoolForIsolatedMode(t *testing.T) {
	h := Handler{}
	recorder := httptest.NewRecorder()
	h.Workflow(recorder, httptest.NewRequest(http.MethodGet, "/.well-known/wuling/schemas/v1/workflow.json", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &schema))
	definitions, ok := schema["definitions"].(map[string]any)
	require.True(t, ok)
	execution, ok := definitions["executionConfig"].(map[string]any)
	require.True(t, ok)
	oneOf, ok := execution["oneOf"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(oneOf), 2)

	var sharedBranch, isolatedBranch map[string]any
	for _, branchAny := range oneOf {
		branch, ok := branchAny.(map[string]any)
		require.True(t, ok)
		props, _ := branch["properties"].(map[string]any)
		mode, _ := props["mode"].(map[string]any)
		if modeConst, _ := mode["const"].(string); modeConst == "isolated" {
			isolatedBranch = branch
			continue
		}
		if enum, _ := mode["enum"].([]any); len(enum) > 0 {
			sharedBranch = branch
		}
	}
	require.NotNil(t, sharedBranch)
	require.NotNil(t, isolatedBranch)
	require.Contains(t, sharedBranch["required"], "mode")
	sharedMode := sharedBranch["properties"].(map[string]any)["mode"].(map[string]any)
	require.ElementsMatch(t, []any{"shared", "exclusive"}, sharedMode["enum"].([]any))
	require.Contains(t, isolatedBranch["required"], "mode")
	require.Contains(t, isolatedBranch["required"], "pool")
	require.Equal(t, "isolated", isolatedBranch["properties"].(map[string]any)["mode"].(map[string]any)["const"])
}
