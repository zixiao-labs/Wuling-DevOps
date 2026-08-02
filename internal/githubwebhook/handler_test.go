package githubwebhook_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/githubwebhook"
	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
)

const webhookSecret = "webhook-test-secret"

func mount(h *githubwebhook.Handler) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) {
		h.Mount(api)
	})
	return r
}

func post(t *testing.T, mux http.Handler, event, delivery string, body []byte, sig string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Result()
}

func TestPing_OK(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)

	h := &githubwebhook.Handler{
		Secret: webhookSecret,
		Store:  &githubwebhook.Store{Pool: pool},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := mount(h)
	body := []byte(`{"zen":"Design for failure.","hook_id":1}`)
	sig := githubwebhook.SignBody(webhookSecret, body)

	res := post(t, mux, "ping", "delivery-ping-1", body, sig)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	assert.Equal(t, true, out["ok"])
}

func TestBadSignature_Unauthorized(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)

	h := &githubwebhook.Handler{
		Secret: webhookSecret,
		Store:  &githubwebhook.Store{Pool: pool},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := mount(h)
	body := []byte(`{"zen":"x"}`)

	res := post(t, mux, "ping", "delivery-bad-sig", body, "sha256=00")
	defer res.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestDuplicateDelivery_OK(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)

	h := &githubwebhook.Handler{
		Secret: webhookSecret,
		Store:  &githubwebhook.Store{Pool: pool},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := mount(h)
	body := []byte(`{"zen":"idempotent"}`)
	sig := githubwebhook.SignBody(webhookSecret, body)

	res1 := post(t, mux, "ping", "delivery-dup-1", body, sig)
	defer res1.Body.Close()
	require.Equal(t, http.StatusOK, res1.StatusCode)

	res2 := post(t, mux, "ping", "delivery-dup-1", body, sig)
	defer res2.Body.Close()
	assert.Equal(t, http.StatusOK, res2.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(res2.Body).Decode(&out))
	assert.Equal(t, true, out["duplicate"])
}

func TestUnknownEvent_Accepted(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)

	h := &githubwebhook.Handler{
		Secret: webhookSecret,
		Store:  &githubwebhook.Store{Pool: pool},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := mount(h)
	body := []byte(`{"action":"opened","number":1}`)
	sig := githubwebhook.SignBody(webhookSecret, body)

	res := post(t, mux, "pull_request", "delivery-pr-1", body, sig)
	defer res.Body.Close()
	assert.Equal(t, http.StatusAccepted, res.StatusCode)
}
