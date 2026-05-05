// Package apperr defines the canonical error type used by the API layer.
//
// Domain code returns *Error with a stable Code; the HTTP layer renders it as
// the JSON envelope {"error": {"code": "...", "message": "...", "details": {}}}.
// Codes are taxonomic (validation, not_found, conflict, ...) — never expose raw
// driver or libgit2 errors to clients.
package apperr

import (
	"errors"
	"fmt"
)

// Code is a stable, machine-readable error code.
type Code string

const (
	CodeInternal       Code = "internal"
	CodeValidation     Code = "validation"
	CodeUnauthorized   Code = "unauthorized"
	CodeForbidden      Code = "forbidden"
	CodeNotFound       Code = "not_found"
	CodeConflict       Code = "conflict"
	CodeRateLimited    Code = "rate_limited"
	CodeUnavailable    Code = "unavailable"
	CodeUnsupported    Code = "unsupported"
	CodeBadRequest     Code = "bad_request"
	CodeAlreadyExists  Code = "already_exists"
	CodeAuthentication Code = "authentication"
)

// Error is the canonical API-layer error.
type Error struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	Cause   error          `json:"-"`
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// HTTPStatus maps Code to an HTTP status code.
func (e *Error) HTTPStatus() int {
	switch e.Code {
	case CodeValidation, CodeBadRequest:
		return 400
	case CodeUnauthorized, CodeAuthentication:
		return 401
	case CodeForbidden:
		return 403
	case CodeNotFound:
		return 404
	case CodeConflict, CodeAlreadyExists:
		return 409
	case CodeUnsupported:
		return 415
	case CodeRateLimited:
		return 429
	case CodeUnavailable:
		return 503
	default:
		return 500
	}
}

// Constructors. Use these instead of fmt.Errorf so callers can errors.As to *Error.

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// WithDetails returns a copy of err with Details set. The original *Error is
// not mutated, so callers can safely re-decorate shared error sentinels.
func WithDetails(err *Error, details map[string]any) *Error {
	if err == nil {
		return nil
	}
	newErr := *err
	newErr.Details = details
	return &newErr
}

// Helpers for common cases.

func NotFound(what string) *Error {
	return New(CodeNotFound, fmt.Sprintf("%s not found", what))
}

func Validation(message string, details map[string]any) *Error {
	e := New(CodeValidation, message)
	e.Details = details
	return e
}

func Internal(cause error) *Error {
	return Wrap(CodeInternal, "internal server error", cause)
}

func Conflict(message string) *Error {
	return New(CodeConflict, message)
}

func Forbidden(message string) *Error {
	return New(CodeForbidden, message)
}

func Unauthorized(message string) *Error {
	return New(CodeUnauthorized, message)
}

// As returns the underlying *Error, or nil if err is not one.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}
