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
	"net/url"
	"os"
	"path/filepath"
	"strings"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/minio/minio-go/v7"
	miniocredentials "github.com/minio/minio-go/v7/pkg/credentials"
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
	case "s3", "aws", "r2":
		if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
			return nil, errors.New("S3-compatible storage requires endpoint, bucket, access key, and secret key")
		}
		client, err := minio.New(cfg.Endpoint, &minio.Options{
			Creds:        miniocredentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
			Secure:       cfg.UseTLS,
			Region:       cfg.Region,
			BucketLookup: minio.BucketLookupAuto,
		})
		if err != nil {
			return nil, fmt.Errorf("create object storage client: %w", err)
		}
		return &s3Store{client: client, bucket: cfg.Bucket}, nil
	case "oss":
		return newOSSStore(cfg)
	default:
		return nil, fmt.Errorf("unsupported artifact storage provider %q", cfg.Provider)
	}
}

func newOSSStore(cfg Config) (Store, error) {
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("OSS storage requires endpoint, region, bucket, access key, and secret key")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if !strings.Contains(endpoint, "://") {
		scheme := "https"
		if !cfg.UseTLS {
			scheme = "http"
		}
		endpoint = scheme + "://" + endpoint
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Host == "" {
		return nil, errors.New("OSS storage endpoint is invalid")
	}
	basePath, err := url.PathUnescape(strings.Trim(endpointURL.EscapedPath(), "/"))
	if err != nil {
		return nil, errors.New("OSS storage endpoint base path is invalid")
	}
	bucketScoped := ossEndpointTargetsBucket(endpoint, cfg.Bucket)
	endpointURL.Path = ""
	endpointURL.RawPath = ""
	endpoint = endpointURL.String()
	options := aliyunoss.LoadDefaultConfig().
		WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey)).
		WithEndpoint(endpoint).
		WithRegion(strings.TrimSpace(cfg.Region)).
		WithDisableSSL(!cfg.UseTLS)
	if bucketScoped {
		// Bucket-scoped endpoints and reverse-proxy base paths already select
		// their destination. CNAME mode prevents the SDK from prepending the
		// bucket again and keeps object keys relative to that base URL.
		options.WithUseCName(true)
	}
	client := aliyunoss.NewClient(options)
	return &ossStore{
		client:   client,
		uploader: aliyunoss.NewUploader(client),
		bucket:   cfg.Bucket,
		basePath: basePath,
	}, nil
}

func ossEndpointTargetsBucket(endpoint, bucket string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	bucket = strings.ToLower(strings.TrimSpace(bucket))
	return strings.HasPrefix(host, bucket+".") || strings.Trim(parsed.EscapedPath(), "/") != ""
}

func ValidateKey(key string) error {
	if key == "" || len(key) > 1024 || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return errors.New("invalid blob key")
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." || part == ".metadata" || strings.ContainsRune(part, '\x00') {
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
		code := minio.ToErrorResponse(err).Code
		if code == "PreconditionFailed" || code == "ConditionalRequestConflict" {
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

type ossStore struct {
	client   *aliyunoss.Client
	uploader *aliyunoss.Uploader
	bucket   string
	basePath string
}

func (s *ossStore) objectKey(key string) string {
	if s.basePath == "" {
		return key
	}
	return s.basePath + "/" + key
}

type sizedReader struct {
	reader    io.Reader
	remaining int64
}

func (r *sizedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func (r *sizedReader) Len() int { return int(r.remaining) }

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}

func (s *ossStore) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (*ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	request := &aliyunoss.PutObjectRequest{
		Bucket:          aliyunoss.Ptr(s.bucket),
		Key:             aliyunoss.Ptr(s.objectKey(key)),
		ContentType:     aliyunoss.Ptr(contentType),
		ForbidOverwrite: aliyunoss.Ptr("true"),
	}
	counted := &countingReader{reader: body}
	body = counted
	if size >= 0 {
		request.ContentLength = aliyunoss.Ptr(size)
		body = &sizedReader{reader: body, remaining: size}
	}
	info, err := s.uploader.UploadFrom(ctx, request, body)
	if err != nil {
		if ossErrorCode(err) == "FileAlreadyExists" {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	etag := ""
	if info.ETag != nil {
		etag = strings.Trim(*info.ETag, `"`)
	}
	return &ObjectInfo{Key: key, Size: counted.read, ContentType: contentType, ETag: etag}, nil
}

func (s *ossStore) Open(ctx context.Context, key string) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	result, err := s.client.GetObject(ctx, &aliyunoss.GetObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.objectKey(key)),
	})
	if err != nil {
		if isOSSNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	contentType := "application/octet-stream"
	if result.ContentType != nil && *result.ContentType != "" {
		contentType = *result.ContentType
	}
	etag := ""
	if result.ETag != nil {
		etag = strings.Trim(*result.ETag, `"`)
	}
	return &Object{ObjectInfo: ObjectInfo{
		Key: key, Size: result.ContentLength, ContentType: contentType, ETag: etag,
	}, Body: result.Body}, nil
}

func (s *ossStore) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &aliyunoss.DeleteObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.objectKey(key)),
	})
	return err
}

func (s *ossStore) Ping(ctx context.Context) error {
	ok, err := s.client.IsBucketExist(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("artifact bucket %q does not exist", s.bucket)
	}
	return nil
}

func ossErrorCode(err error) string {
	var serviceError interface{ ErrorCode() string }
	if errors.As(err, &serviceError) {
		return serviceError.ErrorCode()
	}
	return ""
}

func isOSSNotFound(err error) bool {
	switch ossErrorCode(err) {
	case "NoSuchKey", "NoSuchObject", "NotFound":
		return true
	default:
		return false
	}
}
