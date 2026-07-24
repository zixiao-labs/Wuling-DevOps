package artifactclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPutAndDelete(t *testing.T) {
	t.Parallel()
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer shared-token", r.Header.Get("Authorization"))
		require.Equal(t, "/v1/blobs/projects/project/packages/package/1.0.0", r.URL.EscapedPath())
		switch r.Method {
		case http.MethodPut:
			require.Equal(t, int64(8), r.ContentLength)
			require.Equal(t, "application/gzip", r.Header.Get("Content-Type"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, "artifact", string(body))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"key":"projects/project/packages/package/1.0.0","size":8,"content_type":"application/gzip"}`)
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "shared-token")
	require.NoError(t, err)
	key := "projects/project/packages/package/1.0.0"
	info, err := client.Put(context.Background(), key, strings.NewReader("artifact"), 8, "application/gzip")
	require.NoError(t, err)
	require.Equal(t, key, info.Key)
	require.Equal(t, int64(8), info.Size)
	require.NoError(t, client.Delete(context.Background(), key))
	require.True(t, deleted)
}

func TestPutMapsServiceConflictsAndLimits(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "conflict", status: http.StatusConflict, want: ErrAlreadyExists},
		{name: "too large", status: http.StatusRequestEntityTooLarge, want: ErrTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			client, err := New(server.URL, "")
			require.NoError(t, err)
			_, err = client.Put(context.Background(), "packages/a/1", strings.NewReader("x"), 1, "")
			require.True(t, errors.Is(err, test.want))
		})
	}
}
