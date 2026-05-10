package githttp

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type closeCounter struct {
	io.Reader
	closed int
}

func (c *closeCounter) Close() error { c.closed++; return nil }

func TestGzipBody_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte("hello git"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	src := &closeCounter{Reader: bytes.NewReader(buf.Bytes())}
	gz, err := newGzipReader(src)
	require.NoError(t, err)

	out, err := io.ReadAll(gz)
	require.NoError(t, err)
	assert.Equal(t, "hello git", string(out))

	require.NoError(t, gz.Close())
	assert.Equal(t, 1, src.closed, "Close must close the underlying body exactly once")
}

func TestGzipBody_BadStream(t *testing.T) {
	src := &closeCounter{Reader: strings.NewReader("not gzip data")}
	_, err := newGzipReader(src)
	require.Error(t, err)
}
