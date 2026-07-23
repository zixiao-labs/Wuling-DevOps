package artifactblob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/stretchr/testify/require"
)

func TestLocalStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store, err := NewLocal(t.TempDir())
	require.NoError(t, err)

	written, err := store.Put(context.Background(), "projects/a/packages/b/1.0.0", bytes.NewBufferString("artifact"), 8, "application/test")
	require.NoError(t, err)
	require.Equal(t, int64(8), written.Size)

	object, err := store.Open(context.Background(), written.Key)
	require.NoError(t, err)
	defer object.Body.Close()
	require.Equal(t, "application/test", object.ContentType)
	body, err := io.ReadAll(object.Body)
	require.NoError(t, err)
	require.Equal(t, "artifact", string(body))
	_, err = store.Put(context.Background(), written.Key, bytes.NewBufferString("replacement"), 11, "application/test")
	require.ErrorIs(t, err, ErrAlreadyExists)

	require.NoError(t, store.Delete(context.Background(), written.Key))
}

func TestValidateKeyRejectsTraversal(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"", "/absolute", "a/../b", "a//b", "a/./b", "trailing/", ".metadata", "a/.metadata/b"} {
		require.Error(t, ValidateKey(key), key)
	}
	require.NoError(t, ValidateKey("projects/a/packages/b/1.0.0"))
	require.NoError(t, ValidateKey("projects/a/metadata/b"))
}

func TestS3StoreConflictWrite(t *testing.T) {
	t.Parallel()
	var writes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/artifacts/projects/a/package", r.URL.Path)
		require.Equal(t, "*", r.Header.Get("If-None-Match"))
		writes++
		if writes == 1 {
			w.Header().Set("ETag", `"first"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code><Message>exists</Message></Error>`)
	}))
	defer server.Close()

	store, err := New(Config{
		Provider: "s3", Endpoint: strings.TrimPrefix(server.URL, "http://"),
		Region: "us-east-1", Bucket: "artifacts", AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	_, err = store.Put(context.Background(), "projects/a/package", bytes.NewBufferString("first"), 5, "application/test")
	require.NoError(t, err)
	_, err = store.Put(context.Background(), "projects/a/package", bytes.NewBufferString("second"), 6, "application/test")
	require.ErrorIs(t, err, ErrAlreadyExists)
}

func TestOSSStoreUsesBucketScopedBasePathAndForbidsOverwrite(t *testing.T) {
	t.Parallel()
	var writes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/base/projects/a/package", r.URL.Path)
		require.Equal(t, "true", r.Header.Get("x-oss-forbid-overwrite"))
		require.Empty(t, r.Header.Get("If-None-Match"))
		writes++
		if writes == 1 {
			w.Header().Set("ETag", `"first"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `<Error><Code>FileAlreadyExists</Code><Message>exists</Message></Error>`)
	}))
	defer server.Close()

	endpoint := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	store, err := New(Config{
		Provider: "oss", Endpoint: strings.TrimPrefix(endpoint, "http://") + "/base",
		Region: "cn-test", Bucket: "artifacts", AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)

	info, err := store.Put(context.Background(), "projects/a/package", bytes.NewBufferString("first"), 5, "application/test")
	require.NoError(t, err)
	require.Equal(t, int64(5), info.Size)
	_, err = store.Put(context.Background(), "projects/a/package", bytes.NewBufferString("second"), 6, "application/test")
	require.ErrorIs(t, err, ErrAlreadyExists)
}

func TestOSSStoreMultipartForbidsOverwriteAtCommitBoundaries(t *testing.T) {
	t.Parallel()
	var initiated, completed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/base/projects/a/large-package", r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			initiated = true
			require.Equal(t, "true", r.Header.Get("x-oss-forbid-overwrite"))
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>artifacts</Bucket><Key>base/projects/a/large-package</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut && r.URL.Query().Get("uploadId") == "upload-1":
			w.Header().Set("ETag", fmt.Sprintf(`"part-%s"`, r.URL.Query().Get("partNumber")))
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "upload-1":
			completed = true
			require.Equal(t, "true", r.Header.Get("x-oss-forbid-overwrite"))
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><Bucket>artifacts</Bucket><Key>base/projects/a/large-package</Key><ETag>"complete"</ETag></CompleteMultipartUploadResult>`)
		default:
			t.Errorf("unexpected OSS request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	endpoint := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	store, err := New(Config{
		Provider: "oss", Endpoint: strings.TrimPrefix(endpoint, "http://") + "/base",
		Region: "cn-test", Bucket: "artifacts", AccessKey: "key", SecretKey: "secret",
	})
	require.NoError(t, err)
	ossStore := store.(*ossStore)
	ossStore.uploader = aliyunoss.NewUploader(ossStore.client, func(options *aliyunoss.UploaderOptions) {
		options.PartSize = 5
		options.ParallelNum = 1
	})

	_, err = store.Put(context.Background(), "projects/a/large-package", bytes.NewBufferString("multipart"), 9, "application/test")
	require.NoError(t, err)
	require.True(t, initiated)
	require.True(t, completed)
}

func TestOSSEndpointTargetsBucket(t *testing.T) {
	t.Parallel()
	require.True(t, ossEndpointTargetsBucket("https://artifacts.oss-cn-hangzhou.aliyuncs.com", "artifacts"))
	require.True(t, ossEndpointTargetsBucket("https://proxy.example.com/oss/artifacts", "artifacts"))
	require.False(t, ossEndpointTargetsBucket("https://oss-cn-hangzhou.aliyuncs.com", "artifacts"))
}
