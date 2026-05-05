// Package httpapi wires the chi router, middleware, and response helpers used
// by every domain handler.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// Validator is the shared validator instance. Domain code should call
// Validator.Struct(req) and pass the resulting error to RenderError.
var Validator = validator.New(validator.WithRequiredStructEnabled())

// MaxJSONBodyBytes caps decode size to keep memory bounded under malicious load.
const MaxJSONBodyBytes = 1 << 20 // 1 MiB

// DecodeJSON reads JSON into v with size limits and unknown-field rejection.
// It also rejects trailing data after the JSON value so requests with extra
// payload (e.g. "{}<garbage>") fail loudly instead of being silently ignored.
//
// w is required so MaxBytesReader can close the connection when an oversized
// body is presented; passing a nil ResponseWriter would leave the connection
// in an undefined state on overlong requests.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return apperr.New(apperr.CodeBadRequest, "request body too large")
		case errors.Is(err, io.EOF):
			return apperr.New(apperr.CodeBadRequest, "request body is empty")
		default:
			return apperr.Wrap(apperr.CodeBadRequest, "invalid JSON body", err)
		}
	}
	// Reject trailing data: the request body must contain exactly one JSON value.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return apperr.New(apperr.CodeBadRequest, "request body must only contain a single JSON value")
	}
	if err := Validator.Struct(v); err != nil {
		return validationToAppErr(err)
	}
	return nil
}

func validationToAppErr(err error) *apperr.Error {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		details := map[string]any{}
		for _, fe := range verrs {
			// e.g. {"username": "required"}
			details[fe.Field()] = fe.Tag()
		}
		return apperr.Validation("request validation failed", details)
	}
	return apperr.Wrap(apperr.CodeValidation, "invalid request", err)
}

// WriteJSON renders a successful JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Once headers/status are sent we can't change them; just log.
		slog.Default().Warn("write json failed", "err", err)
	}
}

// errorEnvelope is the response shape for all error responses.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// RenderError writes an error with the canonical envelope. Internal errors
// are logged at error level; client errors at debug.
func RenderError(w http.ResponseWriter, r *http.Request, err error) {
	e := apperr.As(err)
	if e == nil {
		e = apperr.Internal(err)
	}

	log := loggerFromRequest(r)
	if e.Code == apperr.CodeInternal {
		log.Error("internal error", "path", r.URL.Path, "method", r.Method, "err", e.Cause)
	} else {
		log.Debug("client error", "code", e.Code, "path", r.URL.Path, "method", r.Method, "msg", e.Message)
	}

	body := errorEnvelope{Error: errorBody{
		Code:    string(e.Code),
		Message: e.Message,
		Details: e.Details,
	}}
	if e.Code == apperr.CodeInternal {
		// Never leak driver text.
		body.Error.Message = "internal server error"
	}
	WriteJSON(w, e.HTTPStatus(), body)
}

// PathValue is a typed accessor for chi.URLParam-style values; its main job
// is to let handlers stay decoupled from the router import. For chi we just
// pass through to chi.URLParam where used.

// LoggerKey is the context key used to attach a per-request logger.
type loggerCtxKey struct{}

// WithLogger returns a context with the given logger attached.
func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerCtxKey{}, log)
}

func loggerFromRequest(r *http.Request) *slog.Logger {
	if log, ok := r.Context().Value(loggerCtxKey{}).(*slog.Logger); ok && log != nil {
		return log
	}
	return slog.Default()
}

// CleanSlug normalizes a slug for case-insensitive lookup.
func CleanSlug(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
