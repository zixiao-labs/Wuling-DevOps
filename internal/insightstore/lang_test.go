package insightstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguageFromFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain go", "main.go", "Go"},
		{"nested", "internal/foo/bar.rs", "Rust"},
		{"tsx", "src/app.tsx", "TypeScript"},
		{"dockerfile no ext", "Dockerfile", "Dockerfile"},
		{"dockerfile lower", "dockerfile", "Dockerfile"},
		{"makefile path", "build/Makefile", "Makefile"},
		{"gemfile", "Gemfile", "Ruby"},
		{"unknown ext", "weird.zomg", ""},
		{"no ext + unknown name", "LICENSE", ""},
		{"hidden", ".gitignore", ""},
		{"empty", "", ""},
		{"uppercase ext", "Foo.GO", "Go"},
		{"md long", "README.markdown", "Markdown"},
		{"sh aliases", "deploy.bash", "Shell"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, LanguageFromFilename(tc.in))
		})
	}
}

func TestParseSince(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 30 * 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"2w", 14 * 24 * time.Hour, false},
		{"1y", 365 * 24 * time.Hour, false},
		{"60", 60 * time.Second, false},
		{"abc", 0, true},
		{"7x", 0, true},
		{"-1d", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseSince(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]int64{"b": 2, "a": 1, "c": 3}
	assert.Equal(t, []string{"a", "b", "c"}, SortedKeys(m))
}
