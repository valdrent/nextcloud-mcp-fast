// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package errors defines semantic, LLM-friendly error types. Every error the
// server can produce carries a stable machine-readable code plus a short,
// actionable message so that language models can recover without guessing.
package errors

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error identifier.
type Code string

const (
	CodePathForbidden    Code = "path_forbidden"    // path escapes the jail
	CodeNotFound         Code = "not_found"         // 404 from WebDAV
	CodeUnauthorized     Code = "unauthorized"      // 401: bad/missing credentials
	CodeForbidden        Code = "forbidden"         // 403: authenticated but not allowed
	CodePermissionDenied Code = "permission_denied" // local permission level too low
	CodeCircuitOpen      Code = "circuit_open"      // breaker tripped, retry later
	CodeRateLimited      Code = "rate_limited"      // 429 from server
	CodeTooLarge         Code = "too_large"         // exceeds configured size cap
	CodeUnsupportedType  Code = "unsupported_type"  // binary or oversized file content
	CodeBadRequest       Code = "bad_request"       // invalid arguments
	CodeConflict         Code = "conflict"          // 409: e.g. target exists on move
	CodeServerError      Code = "server_error"      // 5xx from WebDAV
	CodeTimeout          Code = "timeout"           // request deadline exceeded
)

// Error is a semantic error with a code, an HTTP status (when originating from
// a WebDAV response), and a human/LLM-readable message.
type Error struct {
	Code    Code
	Status  int // 0 when not HTTP-derived
	Message string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s: %s (http %d)", e.Code, e.Message, e.Status)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New builds a semantic error.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// FromStatus maps an HTTP status to a semantic error with a default message.
func FromStatus(status int, detail string) *Error {
	switch {
	case status == http.StatusOK || (status >= 200 && status < 300):
		return nil
	case status == http.StatusUnauthorized:
		return New(CodeUnauthorized, "authentication failed; check the App Password. %s", detail)
	case status == http.StatusForbidden:
		return New(CodeForbidden, "the account is not allowed to access this resource. %s", detail)
	case status == http.StatusNotFound:
		return New(CodeNotFound, "file or folder not found. %s", detail)
	case status == http.StatusConflict || status == http.StatusPreconditionFailed:
		return New(CodeConflict, "conflict (e.g. destination already exists or the resource is not in the expected state). %s", detail)
	case status == http.StatusRequestEntityTooLarge:
		return New(CodeTooLarge, "payload exceeds server limits. %s", detail)
	case status == http.StatusTooManyRequests:
		return New(CodeRateLimited, "rate limited by the Nextcloud server; slow down and retry. %s", detail)
	case status >= 500:
		return New(CodeServerError, "Nextcloud server error (http %d). %s", status, detail)
	default:
		return &Error{Code: CodeServerError, Status: status, Message: fmt.Sprintf("unexpected response http %d. %s", status, detail)}
	}
}

// Is reports whether err is a semantic Error with the given code.
func Is(err error, code Code) bool {
	var se *Error
	if errors.As(err, &se) {
		return se.Code == code
	}
	return false
}
