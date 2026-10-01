// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRequireBearer(t *testing.T) {
	const tok = "0123456789abcdef0123456789abcdef"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requireBearer(tok, next)

	cases := []struct {
		name, auth string
		want       int
	}{
		{"no header", "", 401},
		{"wrong token", "Bearer nope", 401},
		{"wrong scheme", "Basic " + tok, 401},
		{"bearer", "Bearer " + tok, 200},
		{"lowercase bearer", "bearer " + tok, 200},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, rec.Code, tc.want)
		}
		if tc.want == 401 && rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: missing WWW-Authenticate", tc.name)
		}
	}
}

func TestHTTPServerHardening(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"

	// Create a minimal MCP server
	impl := &mcp.Implementation{
		Name:    "test",
		Version: "1.0",
	}
	srv := mcp.NewServer(impl, nil)

	// Build the handler stack
	handler := buildHTTPHandler(srv, token)

	// Build the HTTP server with hardening
	httpServer := buildHTTPServer("127.0.0.1:0", handler)

	// Verify timeout settings
	if httpServer.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", httpServer.ReadHeaderTimeout)
	}
	if httpServer.ReadTimeout != 60*time.Second {
		t.Errorf("ReadTimeout = %v, want 60s", httpServer.ReadTimeout)
	}
	if httpServer.WriteTimeout != 2*time.Minute {
		t.Errorf("WriteTimeout = %v, want 2m", httpServer.WriteTimeout)
	}
	if httpServer.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", httpServer.IdleTimeout)
	}
	if httpServer.MaxHeaderBytes != 64<<10 {
		t.Errorf("MaxHeaderBytes = %d, want %d", httpServer.MaxHeaderBytes, 64<<10)
	}

	// Test stateless mode: GET with valid bearer should return 405
	// (In stateless mode, GET and DELETE are not allowed)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET with valid bearer in stateless mode: status %d, want 405", rec.Code)
	}

	// A well-formed initialize with a valid bearer must reach the MCP handler
	// and succeed.
	initMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	req = httptest.NewRequest("POST", "/", bytes.NewReader(initMsg))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("POST initialize with valid bearer: status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}
