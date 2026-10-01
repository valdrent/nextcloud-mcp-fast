// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/valdrent/nextcloud-mcp-fast/internal/accounts"
	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	"github.com/valdrent/nextcloud-mcp-fast/internal/mcpsrv"
)

// connect builds a server from cfg with an optional logger and returns a client session.
func connect(t *testing.T, cfg *config.Config, logger *slog.Logger) *mcp.ClientSession {
	t.Helper()
	s, err := mcpsrv.New(cfg, accounts.NewRegistry(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if logger != nil {
		s.SetLogger(logger)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.BuildMCP().Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close(); ss.Close() })
	return cs
}

func TestNotFoundDoesNotTripBreaker(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	cs := connect(t, &config.Config{Mode: "stdio", Host: ts.URL, Username: "alice", Password: "p",
		Permissions: "read", CircuitBreakerThreshold: 3, CircuitBreakerWindow: 60e9}, nil)
	for i := 0; i < 6; i++ {
		text, isErr := callText(t, cs, "stat", map[string]any{"path": "/nope"})
		if !isErr || !strings.Contains(text, "not_found") {
			t.Fatalf("call %d: want not_found, got %q", i, text)
		}
	}
}

func TestServerErrorsTripBreakerAndZeroDisables(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	cfg := &config.Config{Mode: "stdio", Host: ts.URL, Username: "alice", Password: "p",
		Permissions: "read", CircuitBreakerThreshold: 3, CircuitBreakerWindow: 60e9}
	cs := connect(t, cfg, nil)
	var last string
	for i := 0; i < 5; i++ {
		last, _ = callText(t, cs, "stat", map[string]any{"path": "/x"})
	}
	if !strings.Contains(last, "circuit_open") {
		t.Errorf("want circuit_open after repeated 500s, got %q", last)
	}

	cfg.CircuitBreakerThreshold = 0
	cs = connect(t, cfg, nil)
	for i := 0; i < 10; i++ {
		last, _ = callText(t, cs, "stat", map[string]any{"path": "/x"})
	}
	if strings.Contains(last, "circuit_open") {
		t.Errorf("threshold 0 must disable the breaker, got %q", last)
	}
}

func TestOverwriteRules(t *testing.T) {
	m := newStatefulMock()
	ts := httptest.NewServer(http.HandlerFunc(m.handler))
	defer ts.Close()
	m.baseURL = ts.URL

	cs, done := newFullTestServer(t, ts.URL, "write")
	defer done()
	if text, isErr := callText(t, cs, "write_file", map[string]any{"path": rootFilePath, "content": "one"}); isErr {
		t.Fatalf("first write: %s", text)
	}
	text, isErr := callText(t, cs, "write_file", map[string]any{"path": rootFilePath, "content": "two"})
	if !isErr || !strings.Contains(text, "conflict") || !strings.Contains(text, "overwrite=true") {
		t.Errorf("second write should conflict with hint, got %q", text)
	}
	text, isErr = callText(t, cs, "write_file", map[string]any{"path": rootFilePath, "content": "two", "overwrite": true})
	if !isErr || !strings.Contains(text, "permission_denied") {
		t.Errorf("overwrite at write level should be denied, got %q", text)
	}
	text, isErr = callText(t, cs, "move_file", map[string]any{"from": rootFilePath, "to": "/g.txt", "overwrite": true})
	if !isErr || !strings.Contains(text, "permission_denied") {
		t.Errorf("move overwrite at write level should be denied, got %q", text)
	}
	if m.files[aliceFilePath] != "one" {
		t.Errorf("file was modified: %q", m.files[aliceFilePath])
	}

	cs2, done2 := newFullTestServer(t, ts.URL, "destructive")
	defer done2()
	if text, isErr := callText(t, cs2, "write_file", map[string]any{"path": rootFilePath, "content": "two", "overwrite": true}); isErr {
		t.Fatalf("overwrite as destructive: %s", text)
	}
	if m.files[aliceFilePath] != "two" {
		t.Errorf("overwrite did not apply")
	}
}

func TestAuditLog(t *testing.T) {
	m := newStatefulMock()
	ts := httptest.NewServer(http.HandlerFunc(m.handler))
	defer ts.Close()
	m.baseURL = ts.URL

	const secret = "s3cr3t-app-password-XYZ"
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cs := connect(t, &config.Config{Mode: "stdio", Host: ts.URL, Username: "alice", Password: secret,
		Permissions: "write"}, logger)

	callText(t, cs, "write_file", map[string]any{"path": auditFilePath, "content": "hi"})
	callText(t, cs, "write_file", map[string]any{"path": auditFilePath, "content": "hi"}) // conflict
	callText(t, cs, "stat", map[string]any{"path": auditFilePath})                        // debug: not logged

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("password leaked into audit log:\n%s", out)
	}
	for _, want := range []string{`"tool":"write_file"`, `"path":"` + auditFilePath + `"`, `"outcome":"ok"`,
		`"outcome":"conflict"`, `"account":"` + ts.URL + `|alice"`, `"duration_ms"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"tool":"stat"`) {
		t.Errorf("read tools must log at debug only:\n%s", out)
	}
}

const (
	rootFilePath  = "/f.txt"
	aliceFilePath = "/remote.php/dav/files/alice/f.txt"
	auditFilePath = "/audit.txt"
)
