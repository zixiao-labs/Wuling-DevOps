package githttp

import (
	"compress/gzip"
	"io"
)

// newGzipReader returns a gzip.Reader that closes the underlying body on Close.
// Split into its own file so the main handler stays focused on protocol logic.
func newGzipReader(body io.ReadCloser) (io.ReadCloser, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return nil, err
	}
	return &gzipBody{gz: gz, src: body}, nil
}

type gzipBody struct {
	gz  *gzip.Reader
	src io.ReadCloser
}

func (g *gzipBody) Read(p []byte) (int, error) { return g.gz.Read(p) }
func (g *gzipBody) Close() error {
	_ = g.gz.Close()
	return g.src.Close()
}
