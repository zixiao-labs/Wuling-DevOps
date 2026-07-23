// Command wuling-artifacts is the standalone Stage-2 blob service. Package,
// version, and release metadata remain in wuling-api; this process owns only
// immutable object bytes so it can scale and be deployed independently.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zixiao-labs/wuling-devops/internal/artifactblob"
)

type config struct {
	Addr      string
	Token     string
	MaxUpload int64
	Storage   artifactblob.Config
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func loadConfig() (config, error) {
	maxUpload, err := strconv.ParseInt(env("WULING_ARTIFACTS_MAX_UPLOAD_BYTES", "5368709120"), 10, 64)
	if err != nil || maxUpload < 1 {
		return config{}, errors.New("WULING_ARTIFACTS_MAX_UPLOAD_BYTES must be a positive integer")
	}
	useTLS, err := strconv.ParseBool(env("WULING_ARTIFACTS_STORAGE_TLS", "true"))
	if err != nil {
		return config{}, errors.New("WULING_ARTIFACTS_STORAGE_TLS must be true or false")
	}
	cfg := config{
		Addr:      env("WULING_ARTIFACTS_ADDR", ":8090"),
		Token:     strings.TrimSpace(os.Getenv("WULING_ARTIFACTS_INTERNAL_TOKEN")),
		MaxUpload: maxUpload,
		Storage: artifactblob.Config{
			Provider:  env("WULING_ARTIFACTS_STORAGE_PROVIDER", "local"),
			LocalDir:  env("WULING_ARTIFACTS_LOCAL_DIR", "./var/artifacts"),
			Endpoint:  strings.TrimSpace(os.Getenv("WULING_ARTIFACTS_STORAGE_ENDPOINT")),
			Region:    strings.TrimSpace(os.Getenv("WULING_ARTIFACTS_STORAGE_REGION")),
			Bucket:    strings.TrimSpace(os.Getenv("WULING_ARTIFACTS_STORAGE_BUCKET")),
			AccessKey: strings.TrimSpace(os.Getenv("WULING_ARTIFACTS_STORAGE_ACCESS_KEY")),
			SecretKey: strings.TrimSpace(os.Getenv("WULING_ARTIFACTS_STORAGE_SECRET_KEY")),
			UseTLS:    useTLS,
		},
	}
	if strings.EqualFold(env("WULING_ENV", "dev"), "prod") && cfg.Token == "" {
		return config{}, errors.New("WULING_ARTIFACTS_INTERNAL_TOKEN must be set in production")
	}
	return cfg, nil
}

type service struct {
	store     artifactblob.Store
	token     string
	maxUpload int64
}

func (s *service) routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.store.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "storage down"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	r.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"name": "wuling-artifacts", "stage": 2})
	})
	r.Group(func(r chi.Router) {
		r.Use(s.authorize)
		r.Put("/v1/blobs/*", s.put)
		r.Get("/v1/blobs/*", s.get)
		r.Head("/v1/blobs/*", s.head)
		r.Delete("/v1/blobs/*", s.delete)
	})
	return r
}

func (s *service) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Bearer " + s.token
		provided := r.Header.Get("Authorization")
		if s.token != "" && (len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid internal token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func objectKey(r *http.Request) (string, error) {
	key := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	return key, artifactblob.ValidateKey(key)
}

func (s *service) put(w http.ResponseWriter, r *http.Request) {
	key, err := objectKey(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if r.ContentLength > s.maxUpload {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "artifact exceeds upload limit"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	info, err := s.store.Put(r.Context(), key, r.Body, r.ContentLength, r.Header.Get("Content-Type"))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "artifact exceeds upload limit"})
			return
		}
		if errors.Is(err, artifactblob.ErrAlreadyExists) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "blob version already exists"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "storage write failed"})
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *service) open(w http.ResponseWriter, r *http.Request, withBody bool) {
	key, err := objectKey(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	object, err := s.store.Open(r.Context(), key)
	if err != nil {
		if errors.Is(err, artifactblob.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "blob not found"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "storage read failed"})
		return
	}
	defer object.Body.Close()
	w.Header().Set("Content-Type", object.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	if object.ETag != "" {
		w.Header().Set("ETag", `"`+object.ETag+`"`)
	}
	w.WriteHeader(http.StatusOK)
	if withBody {
		_, _ = io.Copy(w, object.Body)
	}
}

func (s *service) get(w http.ResponseWriter, r *http.Request)  { s.open(w, r, true) }
func (s *service) head(w http.ResponseWriter, r *http.Request) { s.open(w, r, false) }

func (s *service) delete(w http.ResponseWriter, r *http.Request) {
	key, err := objectKey(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.store.Delete(r.Context(), key); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "storage delete failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("invalid artifact service configuration", "err", err)
		os.Exit(1)
	}
	store, err := artifactblob.New(cfg.Storage)
	if err != nil {
		slog.Error("initialize artifact storage", "err", err)
		os.Exit(1)
	}
	svc := &service{store: store, token: cfg.Token, maxUpload: cfg.MaxUpload}
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           svc.routes(),
		ReadTimeout:       2 * time.Hour,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		slog.Info("wuling-artifacts listening", "addr", cfg.Addr, "provider", cfg.Storage.Provider)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("artifact service stopped", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
