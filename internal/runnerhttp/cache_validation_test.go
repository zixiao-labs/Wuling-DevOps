package runnerhttp

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/artifactclient"
)

func TestValidateCacheLookupLimits(t *testing.T) {
	t.Parallel()
	if err := validateCacheLookup("key", "version", []string{"prefix-"}); err != nil {
		t.Fatalf("valid cache lookup rejected: %v", err)
	}
	for _, test := range []struct {
		name        string
		key         string
		version     string
		restoreKeys []string
	}{
		{name: "missing key", version: "v"},
		{name: "missing version", key: "k"},
		{name: "key too long", key: strings.Repeat("k", MaxPipelineCacheKeyBytes+1), version: "v"},
		{name: "version too long", key: "k", version: strings.Repeat("v", MaxPipelineCacheVersionBytes+1)},
		{name: "too many restore keys", key: "k", version: "v", restoreKeys: make([]string, MaxPipelineCacheRestoreKeys+1)},
		{name: "empty restore key", key: "k", version: "v", restoreKeys: []string{""}},
		{name: "control character", key: "bad\nkey", version: "v"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCacheLookup(test.key, test.version, test.restoreKeys); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestClassifyCacheUploadError(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	storageErr := errors.New("storage failed")
	for _, test := range []struct {
		name    string
		putErr  error
		bodyErr error
		code    apperr.Code
	}{
		{name: "body too large", putErr: storageErr, bodyErr: &http.MaxBytesError{Limit: 4}, code: apperr.CodePayloadTooLarge},
		{name: "artifact too large", putErr: artifactclient.ErrTooLarge, code: apperr.CodePayloadTooLarge},
		{name: "body read", putErr: storageErr, bodyErr: readErr, code: apperr.CodeBadRequest},
		{name: "already exists", putErr: artifactclient.ErrAlreadyExists, code: apperr.CodeConflict},
		{name: "storage", putErr: storageErr, code: apperr.CodeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := apperr.As(classifyCacheUploadError(test.putErr, test.bodyErr))
			if err == nil || err.Code != test.code {
				t.Fatalf("error = %#v, want code %s", err, test.code)
			}
		})
	}
}

func TestValidateCacheUploadSHA256(t *testing.T) {
	t.Parallel()
	got, err := validateCacheUpload("key", "version", strings.Repeat("A", 64))
	if err != nil {
		t.Fatalf("valid sha rejected: %v", err)
	}
	if got != strings.Repeat("a", 64) {
		t.Fatalf("sha not normalized: %q", got)
	}
	for _, invalid := range []string{"not-a-sha", " " + strings.Repeat("a", 64), strings.Repeat("a", 64) + " "} {
		if _, err := validateCacheUpload("key", "version", invalid); err == nil {
			t.Fatalf("expected invalid sha error for %q", invalid)
		}
	}
}
