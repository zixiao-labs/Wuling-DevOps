// Package artifactblob provides the storage boundary for the standalone
// Artifact Service. The core API stores only immutable blob keys; this package
// maps those keys to local disk or an S3-compatible object store.
package artifactblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type ObjectInfo struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag,omitempty"`
}

type Object struct {
	ObjectInfo
	Body io.ReadCloser
}

var (
	ErrAlreadyExists = errors.New("blob already exists")
	ErrNotFound      = errors.New("blob not found")
)

type Store interface {
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (*ObjectInfo, error)
	Open(ctx context.Context, key string) (*Object, error)
	Delete(ctx context.Context, key string) error
	Ping(ctx context.Context) error
}

type Config struct {
	Provider  string
	LocalDir  string
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseTLS    bool
}

func New(cfg Config) (Store, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "local", "disk":
		return NewLocal(cfg.LocalDir)
	case "s3", "aws", "r2", "oss":
		if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
			return nil, errors.New("S3-compatible storage requires endpoint, bucket, access key, and secret key")
		}
		lookup := minio.BucketLookupAuto
		if strings.EqualFold(cfg.Provider, "oss") {
			// Alibaba OSS's S3 compatibility endpoint accepts only virtual-hosted
			// bucket addressing (`bucket.endpoint`), never path-style requests.
			lookup = minio.BucketLookupDNS
		}
		client, err := minio.New(cfg.Endpoint, &minio.Options{
			Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
			Secure:       cfg.UseTLS,
			Region:       cfg.Region,
			BucketLookup: lookup,
		})
		if err != nil {
			return nil, fmt.Errorf("create object storage client: %w", err)
		}
		return &s3Store{client: client, bucket: cfg.Bucket}, nil
	default:
		return nil, fmt.Errorf("unsupported artifact storage provider %q", cfg.Provider)
	}
}

func ValidateKey(key string) error {
	if key == "" || len(key) > 1024 || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return errors.New("invalid blob key")
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, '\x00') {
			return errors.New("invalid blob key")
		}
	}
	return nil
}

type localStore struct{ root string }

func NewLocal(root string) (Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("local artifact directory cannot be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}
	return &localStore{root: abs}, nil
}

func (s *localStore) path(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	value := filepath.Join(append([]string{s.root}, strings.Split(key, "/")...)...)
	if value != s.root && !strings.HasPrefix(value, s.root+string(filepath.Separator)) {
		return "", errors.New("invalid blob key")
	}
	return value, nil
}

func (s *localStore) metadataPath(key string) string {
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(s.root, ".metadata", hex.EncodeToString(digest[:]))
}

func (s *localStore) Put(_ context.Context, key string, body io.Reader, _ int64, contentType string) (*ObjectInfo, error) {
	target, err := s.path(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	written, copyErr := io.Copy(tmp, body)
	closeErr := tmp.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return nil, err
	}
	// Hard-linking the fully-written temp file is an atomic create-if-absent
	// operation on the same filesystem. Unlike Rename it never overwrites an
	// immutable version that another publisher won the race to create.
	if err := os.Link(tmpName, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(target))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := os.MkdirAll(filepath.Join(s.root, ".metadata"), 0o750); err != nil {
		_ = os.Remove(target)
		return nil, err
	}
	if err := os.WriteFile(s.metadataPath(key), []byte(contentType), 0o640); err != nil {
		_ = os.Remove(target)
		return nil, err
	}
	return &ObjectInfo{Key: key, Size: written, ContentType: contentType}, nil
}

func (s *localStore) Open(_ context.Context, key string) (*Object, error) {
	target, err := s.path(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	contentType := mime.TypeByExtension(filepath.Ext(target))
	if raw, err := os.ReadFile(s.metadataPath(key)); err == nil {
		contentType = strings.TrimSpace(string(raw))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &Object{ObjectInfo: ObjectInfo{Key: key, Size: stat.Size(), ContentType: contentType}, Body: file}, nil
}

func (s *localStore) Delete(_ context.Context, key string) error {
	target, err := s.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(target)
	_ = os.Remove(s.metadataPath(key))
	return err
}

func (s *localStore) Ping(_ context.Context) error {
	file, err := os.CreateTemp(s.root, ".health-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		return err
	}
	return os.Remove(name)
}

type s3Store struct {
	client *minio.Client
	bucket string
}

func (s *s3Store) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (*ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	opts := minio.PutObjectOptions{ContentType: contentType}
	opts.SetMatchETagExcept("*")
	info, err := s.client.PutObject(ctx, s.bucket, key, body, size, opts)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "PreconditionFailed" {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return &ObjectInfo{Key: key, Size: info.Size, ContentType: contentType, ETag: info.ETag}, nil
}

func (s *s3Store) Open(ctx context.Context, key string) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		code := minio.ToErrorResponse(err).Code
		if code == "NoSuchKey" || code == "NoSuchObject" || code == "NotFound" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	body, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return &Object{ObjectInfo: ObjectInfo{Key: key, Size: info.Size,
		ContentType: info.ContentType, ETag: info.ETag}, Body: body}, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *s3Store) Ping(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("artifact bucket %q does not exist", s.bucket)
	}
	return nil
}
