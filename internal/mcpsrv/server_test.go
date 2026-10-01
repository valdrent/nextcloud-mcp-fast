// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/valdrent/nextcloud-mcp-fast/internal/accounts"
	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	"github.com/valdrent/nextcloud-mcp-fast/internal/mcpsrv"
)

// mockWebDAV is a tiny in-memory WebDAV server good enough to exercise the
// client and tool wiring end to end.
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
    <D:href>/remote.php/dav/files/alice/docs</D:href>
    <D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:propstat>
  </D:response>
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

func newTestServer(t *testing.T, host string) *mcp.Server {
	t.Helper()
	return newTestServerWithPerm(t, host, "read")
}

func newTestServerWithPerm(t *testing.T, host, permLevel string) *mcp.Server {
	t.Helper()
	cfg := &config.Config{
		Mode:         "stdio",
		Host:         host,
		Username:     "alice",
		Password:     "app-password",
		Permissions:  permLevel,
		AllowedHosts: []string{host},
	}
	reg := accounts.NewRegistry(cfg)
	s, err := mcpsrv.New(cfg, reg)
	if err != nil {
		t.Fatalf("mcpsrv.New: %v", err)
	}
	return s.BuildMCP()
}

func TestToolsListAndRead(t *testing.T) {
	ts := mockWebDAV(t)
	defer ts.Close()

	server := newTestServer(t, ts.URL)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	ctx := context.Background()

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"list_files", "read_file", "write_file", "create_folder", "move_file", "delete", "search_files", "stat"} {
		if !names[want] {
			t.Errorf("missing tool %q; got %v", want, names)
		}
	}

	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_files",
		Arguments: map[string]any{"path": "/docs"},
	})
	if err != nil {
		t.Fatalf("CallTool list_files: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_files returned error: %+v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "notes.txt") {
		t.Errorf("expected notes.txt in listing, got: %s", text)
	}

	res, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_file",
		Arguments: map[string]any{"path": "/docs/notes.txt"},
	})
	if err != nil {
		t.Fatalf("CallTool read_file: %v", err)
	}
	if res.IsError {
		t.Fatalf("read_file returned error: %+v", res.Content)
	}
	text = res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "hello world") {
		t.Errorf("expected file content, got: %s", text)
	}

	// Path traversal must be rejected with a semantic error.
	res, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_file",
		Arguments: map[string]any{"path": "/../../etc/passwd"},
	})
	if err != nil {
		t.Fatalf("CallTool traversal: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected traversal to be rejected, got: %s", res.Content[0].(*mcp.TextContent).Text)
	} else if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "path_forbidden") {
		t.Errorf("expected path_forbidden code, got: %s", res.Content[0].(*mcp.TextContent).Text)
	}
}

func TestPermissionGuardBlocksWrite(t *testing.T) {
	ts := mockWebDAV(t)
	defer ts.Close()

	server := newTestServerWithPerm(t, ts.URL, "read")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	res, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "write_file",
		Arguments: map[string]any{"path": "/x.txt", "content": "hi"},
	})
	if err != nil {
		t.Fatalf("CallTool write_file: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected permission_denied, got success: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "permission_denied") {
		t.Errorf("expected permission_denied code, got: %s", text)
	}
}
