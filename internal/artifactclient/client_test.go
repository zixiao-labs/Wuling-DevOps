package artifactclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var testOptions = Options{
	ConnectTimeout:        time.Second,
	ResponseHeaderTimeout: time.Second,
	RequestTimeout:        time.Minute,
}

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

	client, err := New(server.URL, "shared-token", testOptions)
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
			client, err := New(server.URL, "", testOptions)
			require.NoError(t, err)
			_, err = client.Put(context.Background(), "packages/a/1", strings.NewReader("x"), 1, "")
			require.True(t, errors.Is(err, test.want))
		})
	}
}

func TestNewConfiguresHTTPTimeouts(t *testing.T) {
	t.Parallel()
	options := Options{
		ConnectTimeout:        2 * time.Second,
		ResponseHeaderTimeout: 3 * time.Second,
		RequestTimeout:        4 * time.Second,
	}
	client, err := New("https://artifacts.example.com", "", options)
	require.NoError(t, err)
	require.Equal(t, options.RequestTimeout, client.httpClient.Timeout)
	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.DialContext)
	require.Equal(t, options.ResponseHeaderTimeout, transport.ResponseHeaderTimeout)
}

func TestNewRejectsNonPositiveHTTPTimeouts(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"connect", "response header", "request"} {
		t.Run(field, func(t *testing.T) {
			options := testOptions
			switch field {
			case "connect":
				options.ConnectTimeout = 0
			case "response header":
				options.ResponseHeaderTimeout = 0
			case "request":
				options.RequestTimeout = 0
			}
			_, err := New("https://artifacts.example.com", "", options)
			require.EqualError(t, err, "artifact service HTTP timeouts must be positive")
		})
	}
}
