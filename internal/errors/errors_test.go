// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"errors"
	"net/http"
	"testing"
)

func TestFromStatus(t *testing.T) {
	cases := []struct {
		status int
		want   Code
	}{
		{http.StatusOK, ""}, // nil
		{http.StatusCreated, ""},
		{http.StatusUnauthorized, CodeUnauthorized},
		{http.StatusForbidden, CodeForbidden},
		{http.StatusNotFound, CodeNotFound},
		{http.StatusConflict, CodeConflict},
		{http.StatusRequestEntityTooLarge, CodeTooLarge},
		{http.StatusTooManyRequests, CodeRateLimited},
		{http.StatusInternalServerError, CodeServerError},
		{http.StatusBadGateway, CodeServerError},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			got := FromStatus(tc.status, "detail")
			if tc.want == "" {
				if got != nil {
					t.Fatalf("FromStatus(%d) = %v, want nil", tc.status, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("FromStatus(%d) = nil, want %s", tc.status, tc.want)
			}
			if got.Code != tc.want {
				t.Fatalf("FromStatus(%d).Code = %s, want %s", tc.status, got.Code, tc.want)
			}
		})
	}
}

func TestIs(t *testing.T) {
	base := New(CodeNotFound, "missing")
	wrapped := errors.Join(nil, base) // wrap in a generic error chain

	if !Is(base, CodeNotFound) {
		t.Errorf("Is(direct) = false, want true")
	}
	if !Is(wrapped, CodeNotFound) {
		t.Errorf("Is(wrapped) = false, want true")
	}
	if Is(base, CodeForbidden) {
		t.Errorf("Is with wrong code = true, want false")
	}
	if Is(errors.New("plain"), CodeNotFound) {
		t.Errorf("Is on non-semantic error = true, want false")
	}
}

func TestErrorString(t *testing.T) {
	e := New(CodeNotFound, "nope")
	if got := e.Error(); got != "not_found: nope" {
		t.Errorf("Error() = %q", got)
	}
	withStatus := &Error{Code: CodeServerError, Status: 502, Message: "bad"}
	if got := withStatus.Error(); got != "server_error: bad (http 502)" {
		t.Errorf("Error() with status = %q", got)
	}
}
