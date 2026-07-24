// Package artifactclient connects the core API to the private Artifact
// Service. Browser clients never receive the shared internal token; uploads
// flow through wuling-api after project permissions have been checked.
package artifactclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrAlreadyExists = errors.New("artifact blob already exists")
	ErrTooLarge      = errors.New("artifact blob exceeds upload limit")
)

type ObjectInfo struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag,omitempty"`
}

type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
}

func New(baseURL, token string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("artifact service base URL must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("artifact service base URL must use http or https")
	}
	return &Client{
		baseURL:    parsed,
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{},
	}, nil
}

func (c *Client) blobURL(key string) string {
	parts := strings.Split(key, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.TrimRight(c.baseURL.String(), "/") + "/v1/blobs/" + strings.Join(parts, "/")
}

func (c *Client) Put(
	ctx context.Context,
	key string,
	body io.Reader,
	size int64,
	contentType string,
) (*ObjectInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.blobURL(key), body)
	if err != nil {
		return nil, fmt.Errorf("build artifact upload request: %w", err)
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.authorize(req)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload artifact blob: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusCreated:
		var out ObjectInfo
		if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
			return nil, fmt.Errorf("decode artifact upload response: %w", err)
		}
		return &out, nil
	case http.StatusConflict:
		return nil, ErrAlreadyExists
	case http.StatusRequestEntityTooLarge:
		return nil, ErrTooLarge
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return nil, fmt.Errorf("artifact service upload returned HTTP %d", res.StatusCode)
	}
}

func (c *Client) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.blobURL(key), nil)
	if err != nil {
		return fmt.Errorf("build artifact delete request: %w", err)
	}
	c.authorize(req)
	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete artifact blob: %w", err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("artifact service delete returned HTTP %d", res.StatusCode)
	}
	return nil
}

func (c *Client) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
