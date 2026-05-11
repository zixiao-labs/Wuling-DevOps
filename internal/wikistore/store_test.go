package wikistore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

func TestValidatePath(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"simple", "Home.md", "Home.md", false},
		{"nested", "docs/usage.md", "docs/usage.md", false},
		{"windows backslash", "docs\\usage.md", "docs/usage.md", false},
		{"trim space", "  Home.md  ", "Home.md", false},
		{"redundant dot", "./docs/foo.md", "docs/foo.md", false},
		{"empty", "", "", true},
		{"absolute", "/etc/passwd.md", "", true},
		{"traversal", "../etc/passwd.md", "", true},
		{"traversal middle", "docs/../../etc/passwd.md", "", true},
		{"missing .md", "notes.txt", "", true},
		{"too deep", "a/b/c/d/e/f/g/h/i.md", "", true},
		{"control char", "ev\x00il.md", "", true},
		{"newline", "ev\nil.md", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidatePath(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				ae := apperr.As(err)
				require.NotNil(t, ae, "expected apperr")
				assert.Equal(t, apperr.CodeValidation, ae.Code)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
