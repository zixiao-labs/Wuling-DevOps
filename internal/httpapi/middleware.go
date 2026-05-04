package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// RequestIDMiddleware attaches a stable X-Request-Id, generating one if
// absent. The id is also threaded into the per-request slog.Logger.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), requestIDKey{}, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type requestIDKey struct{}

// RequestID returns the per-request id, or "" if not set.
func RequestID(r *http.Request) string {
	if v, ok := r.Context().Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// LoggingMiddleware emits one access-log line per request.
func LoggingMiddleware(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			rid := RequestID(r)
			log := base.With("request_id", rid)
			r = r.WithContext(WithLogger(r.Context(), log))

			next.ServeHTTP(ww, r)

			log.Info("http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}

// RecoverMiddleware turns panics into 500s with a logged stack trace.
func RecoverMiddleware(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					base.Error("panic in handler",
						"err", rec,
						"path", r.URL.Path,
						"method", r.Method,
						"stack", string(debug.Stack()),
					)
					if !isHeaderWritten(w) {
						w.Header().Set("Content-Type", "application/json; charset=utf-8")
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"internal server error"}}`))
					}
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// isHeaderWritten is a best-effort check; chi's WrapResponseWriter exposes Status().
func isHeaderWritten(w http.ResponseWriter) bool {
	if ww, ok := w.(middleware.WrapResponseWriter); ok {
		return ww.Status() != 0
	}
	return false
}
