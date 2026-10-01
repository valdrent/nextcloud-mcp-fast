//go:build e2e

// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// e2e runs the real, compiled binary as a child process and drives it with an
// MCP client over stdio, against an in-process WebDAV server. It exercises the
// full path that mocks cannot: process startup, env-var config parsing, stdio
// framing, and the healthcheck flag.
//
// This tier is gated behind the `e2e` build tag so it never runs in the default
// `go test ./...`. Run it with `make e2e` (or `go test -tags=e2e ./test/e2e`).

const binary = "bin/nextcloud-mcp-fast"

func TestBinaryEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in -short mode")
	}

	bin := mustBuildPath(t)
	ts := mockWebDAV(t)
	defer ts.Close()

	cmd := exec.Command(bin)
	cmd.Env = append(
		filterEnv(),
		"NEXTCLOUD_HOST="+ts.URL,
		"NEXTCLOUD_USERNAME=alice",
		"NEXTCLOUD_PASSWORD=app-password",
		"NEXTCLOUD_MCP_PERMISSIONS=read",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("client connect to binary: %v\nstderr:\n%s", err, stderr.String())
	}
	defer cs.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Tool discovery through the real binary.
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"list_files", "read_file", "write_file", "create_folder", "move_file", "delete", "search_files", "stat"} {
		if !names[want] {
			t.Errorf("binary missing tool %q; got %v", want, names)
		}
	}

	// Read a file through the real binary.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_file",
		Arguments: map[string]any{"path": "/docs/notes.txt"},
	})
	if err != nil {
		t.Fatalf("CallTool read_file: %v", err)
	}
	if res.IsError {
		t.Fatalf("read_file error through binary: %+v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "hello world") {
		t.Errorf("expected file content, got: %s", text)
	}

	// Traversal must be rejected by the binary.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_file",
		Arguments: map[string]any{"path": "/../../etc/passwd"},
	})
	if err != nil {
		t.Fatalf("CallTool traversal: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "path_forbidden") {
		t.Errorf("expected path_forbidden from binary, got: %+v", res.Content)
	}
}

func TestBinaryHealthcheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in -short mode")
	}
	bin := mustBuildPath(t)

	out, err := exec.Command(bin, "--healthcheck").Output()
	if err != nil {
		t.Fatalf("healthcheck failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Errorf("unexpected healthcheck output: %s", out)
	}
}

func mustBuildPath(t *testing.T) string {
	t.Helper()
	bin, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("abs binary path: %v", err)
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("binary not built at %s (run `make build` first): %v", bin, err)
	}
	return bin
}

// filterEnv returns the current environment minus any NEXTCLOUD_* variables so
// test config is fully controlled here.
func filterEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "NEXTCLOUD_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// mockWebDAV is a minimal WebDAV server (PROPFIND + GET) for the e2e tier.
func mockWebDAV(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			fmt.Fprint(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/remote.php/dav/files/alice/docs/notes.txt</D:href>
    <D:propstat><D:prop>
      <D:resourcetype/>
      <D:getcontentlength>11</D:getcontentlength>
      <D:getlastmodified>Wed, 01 Jan 2026 00:00:00 GMT</D:getlastmodified>
      <D:getcontenttype>text/plain</D:getcontenttype>
    </D:prop></D:propstat>
  </D:response>
</D:multistatus>`)
			w.WriteHeader(http.StatusMultiStatus)
		case "GET":
			if strings.HasSuffix(r.URL.Path, "notes.txt") {
				w.Header().Set("Content-Length", "11")
				fmt.Fprint(w, "hello world")
			} else {
				http.NotFound(w, r)
			}
		default:
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
		}
	})
	return httptest.NewServer(mux)
}
