package artifactblob

import (
	"bytes"
	"context"
	"io"
	"testing"

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
	for _, key := range []string{"", "/absolute", "a/../b", "a//b", "a/./b", "trailing/"} {
		require.Error(t, ValidateKey(key), key)
	}
	require.NoError(t, ValidateKey("projects/a/packages/b/1.0.0"))
}
