package stage2store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNormalizedList(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"devops", "go", "react"}, normalizedList([]string{
		" DevOps ", "go", "DEVOPS", "", " React ",
	}))
}

func TestValidMergeStrategies(t *testing.T) {
	t.Parallel()
	require.True(t, validMergeStrategies([]string{"merge", "squash", "rebase"}))
	require.True(t, validMergeStrategies([]string{"squash"}))
	require.False(t, validMergeStrategies(nil))
	require.False(t, validMergeStrategies([]string{"fast-forward"}))
}

func TestParseDate(t *testing.T) {
	t.Parallel()
	value, err := ParseDate("2026-07-10")
	require.NoError(t, err)
	require.Equal(t, 2026, value.Year())
	require.Equal(t, 7, int(value.Month()))
	require.Equal(t, 10, value.Day())

	_, err = ParseDate("10/07/2026")
	require.Error(t, err)
}

func TestFormatBlobKeySanitizesVersionPath(t *testing.T) {
	t.Parallel()
	projectID := uuid.MustParse("018f0000-0000-7000-8000-000000000001")
	packageID := uuid.MustParse("018f0000-0000-7000-8000-000000000002")
	require.Equal(t,
		"projects/018f0000-0000-7000-8000-000000000001/packages/018f0000-0000-7000-8000-000000000002/1.2.3-debug",
		formatBlobKey(projectID, packageID, "1.2.3/../debug"),
	)
}
