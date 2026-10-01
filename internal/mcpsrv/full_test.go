// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/valdrent/nextcloud-mcp-fast/internal/accounts"
	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	"github.com/valdrent/nextcloud-mcp-fast/internal/mcpsrv"
)

// statefulMock is a tiny in-memory WebDAV server with real state: it stores
// files, creates folders, moves and deletes them, and answers PROPFIND from
// the stored tree. Good enough to exercise the full tool surface end to end.
type statefulMock struct {
	baseURL string            // absolute URL of the test server (for Destination headers)
	files   map[string]string // path (with user prefix) -> content
	folders map[string]bool
}

func newStatefulMock() *statefulMock {
	return &statefulMock{files: map[string]string{}, folders: map[string]bool{"/remote.php/dav/files/alice": true}}
}

func (m *statefulMock) handler(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch r.Method {
	case "PROPFIND":
		m.propfind(w, r, p)
	case "MKCOL":
		m.mkcol(w, p)
	case http.MethodPut:
		m.put(w, r, p)
	case http.MethodGet:
		m.get(w, r, p)
	case "MOVE":
		m.move(w, r, p)
	case http.MethodDelete:
		m.del(w, p)
	default:
		http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
	}
}

const (
	xmlFileProps = `<D:resourcetype/>
<D:getcontentlength>%d</D:getcontentlength>
<D:getlastmodified>Wed, 01 Jan 2026 00:00:00 GMT</D:getlastmodified>
<D:getcontenttype>text/plain</D:getcontenttype>`
	xmlDirProps = `<D:resourcetype><D:collection/></D:resourcetype>`
)

func (m *statefulMock) fileXML(href, content string) string {
	return `<D:response><D:href>` + href + `</D:href><D:propstat><D:prop>` +
		fmt.Sprintf(xmlFileProps, len(content)) + `</D:prop></D:propstat></D:response>`
}

func dirXML(href string) string {
	return `<D:response><D:href>` + href + `</D:href><D:propstat><D:prop>` + xmlDirProps + `</D:prop></D:propstat></D:response>`
}

// propfindBody renders the multistatus body for p, or ok=false when p is unknown.
func (m *statefulMock) propfindBody(p, depth string) (string, bool) {
	if depth == "0" {
		if content, ok := m.files[p]; ok {
			return m.fileXML(p, content), true
		}
		return dirXML(p), m.folders[p]
	}
	// depth 1: the folder itself plus its direct children
	if !m.folders[p] {
		return "", false
	}
	var b strings.Builder
	b.WriteString(dirXML(p))
	// sorted so paginated listings are stable across requests
	fps := make([]string, 0, len(m.files))
	for fp := range m.files {
		fps = append(fps, fp)
	}
	sort.Strings(fps)
	for _, fp := range fps {
		if parentOf(fp) == p {
			b.WriteString(m.fileXML(fp, m.files[fp]))
		}
	}
	for f := range m.folders {
		if parentOf(f) == p {
			b.WriteString(dirXML(f))
		}
	}
	return b.String(), true
}

func (m *statefulMock) propfind(w http.ResponseWriter, r *http.Request, p string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	body, ok := m.propfindBody(p, r.Header.Get("Depth"))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:">`+body+`</D:multistatus>`)
}

// parseByteRange parses "bytes=start-end" (end optional) for a body of n bytes.
func parseByteRange(rng string, n int) (start, end int) {
	end = n - 1
	spec, found := strings.CutPrefix(rng, "bytes=")
	if !found {
		return start, end
	}
	if i := strings.IndexByte(spec, '-'); i >= 0 {
		start, _ = strconv.Atoi(spec[:i])
		if spec[i+1:] != "" {
			end, _ = strconv.Atoi(spec[i+1:])
		}
	}
	return start, end
}

func (m *statefulMock) mkcol(w http.ResponseWriter, p string) {
	if m.folders[p] {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !m.folders[parentOf(p)] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	m.folders[p] = true
	w.WriteHeader(http.StatusCreated)
}

func (m *statefulMock) put(w http.ResponseWriter, r *http.Request, p string) {
	if !m.folders[parentOf(p)] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if _, exists := m.files[p]; exists && r.Header.Get("If-None-Match") == "*" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	m.files[p] = string(body)
	w.WriteHeader(http.StatusCreated)
}

func (m *statefulMock) get(w http.ResponseWriter, r *http.Request, p string) {
	content, ok := m.files[p]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" {
		start, end := parseByteRange(rng, len(content))
		if start > end || start >= len(content) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if end >= len(content) {
			end = len(content) - 1
		}
		w.Header().Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(len(content)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(content[start : end+1]))
		return
	}
	w.Header().Set("Content-Length", itoa(len(content)))
	w.Write([]byte(content))
}

func (m *statefulMock) move(w http.ResponseWriter, r *http.Request, p string) {
	dst := strings.TrimPrefix(r.Header.Get("Destination"), m.baseURL)
	if !strings.HasPrefix(dst, "/remote.php/dav/files/") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	src := p
	content, ok := m.files[src]
	if !ok && !m.folders[src] {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if _, exists := m.files[dst]; exists && r.Header.Get("Overwrite") == "F" {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if ok {
		m.files[dst] = content
		delete(m.files, src)
	} else {
		m.folders[dst] = true
		delete(m.folders, src)
	}
	w.WriteHeader(http.StatusCreated)
}

func (m *statefulMock) del(w http.ResponseWriter, p string) {
	if _, ok := m.files[p]; ok {
		delete(m.files, p)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if m.folders[p] {
		for fp := range m.files {
			if strings.HasPrefix(fp, p+"/") {
				delete(m.files, fp)
			}
		}
		for f := range m.folders {
			if strings.HasPrefix(f, p+"/") {
				delete(m.folders, f)
			}
		}
		delete(m.folders, p)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func parentOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func itoa(n int) string { return strconv.Itoa(n) }

// newFullTestServer builds an MCP server wired to a stateful mock WebDAV.
func newFullTestServer(t *testing.T, host, permLevel string) (*mcp.ClientSession, func()) {
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
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.BuildMCP().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return clientSession, func() {
		clientSession.Close()
		serverSession.Close()
	}
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	return text, res.IsError
}

// mustCall calls a tool and fails the test if it returns a tool error.
func mustCall(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	text, isErr := callText(t, cs, tool, args)
	if isErr {
		t.Fatalf("%s failed: %s", tool, text)
	}
	return text
}

// mustFailWith calls a tool and requires a tool error containing code.
func mustFailWith(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any, code string) {
	t.Helper()
	text, isErr := callText(t, cs, tool, args)
	if !isErr {
		t.Fatalf("%s should fail, got: %s", tool, text)
	}
	if !strings.Contains(text, code) {
		t.Errorf("%s: expected %s code, got: %s", tool, code, text)
	}
}

// decodeJSON unmarshals a tool result into v or fails the test.
func decodeJSON(t *testing.T, text string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), v); err != nil {
		t.Fatalf("result not JSON: %s", text)
	}
}

func TestFullLifecycle(t *testing.T) {
	m := newStatefulMock()
	ts := httptest.NewServer(http.HandlerFunc(m.handler))
	defer ts.Close()
	m.baseURL = ts.URL

	cs, done := newFullTestServer(t, ts.URL, "destructive")
	defer done()

	lifecycleWriteAndStat(t, cs)
	lifecycleListReadSearch(t, cs)
	lifecycleMoveAndFolders(t, cs)
	lifecycleDelete(t, cs)
}

// write to a nested path that does not exist yet (parents are created), then
// stat the new file: size and type must be correct.
func lifecycleWriteAndStat(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	text := mustCall(t, cs, "write_file", map[string]any{"path": nestedPath, "content": "hello nested"})
	if !strings.Contains(text, "12 bytes") {
		t.Errorf("write_file result = %q, want 12 bytes", text)
	}

	var st struct {
		Size  int64  `json:"size"`
		IsDir bool   `json:"isDir"`
		Path  string `json:"path"`
	}
	decodeJSON(t, mustCall(t, cs, "stat", map[string]any{"path": nestedPath}), &st)
	if st.Size != 12 || st.IsDir {
		t.Errorf("stat = %+v, want size 12, isDir false", st)
	}
}

// list the parent folder, read back with offset/length (Range request path)
// and search by name substring.
func lifecycleListReadSearch(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	var list struct {
		Count   int `json:"count"`
		Entries []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	decodeJSON(t, mustCall(t, cs, "list_files", map[string]any{"path": "/a/b"}), &list)
	if list.Count != 1 || len(list.Entries) != 1 {
		t.Fatalf("list = %+v, want 1 entry", list)
	}
	if list.Entries[0].Type != "file" || list.Entries[0].Size != 12 {
		t.Errorf("entry = %+v, want file size 12", list.Entries[0])
	}

	var rd struct {
		Content   string `json:"content"`
		Encoding  string `json:"encoding"`
		Truncated bool   `json:"truncated"`
	}
	decodeJSON(t, mustCall(t, cs, "read_file", map[string]any{"path": nestedPath, "offset": 6, "length": 6}), &rd)
	if rd.Content != "nested" || rd.Encoding != "text" {
		t.Errorf("read = %+v, want content \"nested\" text", rd)
	}

	var sr struct {
		Count int `json:"count"`
	}
	text := mustCall(t, cs, "search_files", map[string]any{"query": "nested"})
	decodeJSON(t, text, &sr)
	if sr.Count != 1 {
		t.Errorf("search count = %d, want 1 (%s)", sr.Count, text)
	}
}

// move without overwrite onto an existing destination conflicts; with
// overwrite it succeeds and the source is gone; create_folder on an existing
// folder conflicts.
func lifecycleMoveAndFolders(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	mustCall(t, cs, "write_file", map[string]any{"path": targetPath, "content": "target"})
	mustFailWith(t, cs, "move_file", map[string]any{"from": nestedPath, "to": targetPath, "overwrite": false}, "conflict")

	mustCall(t, cs, "move_file", map[string]any{"from": nestedPath, "to": targetPath, "overwrite": true})
	if _, isErr := callText(t, cs, "stat", map[string]any{"path": nestedPath}); !isErr {
		t.Errorf("stat on moved source should fail")
	}

	mustFailWith(t, cs, "create_folder", map[string]any{"path": "/a/b"}, "conflict")
}

// delete the file, then the folder tree; the root listing ends empty and a
// stat on a missing path is a clean not_found.
func lifecycleDelete(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	mustCall(t, cs, "delete", map[string]any{"path": targetPath})
	mustCall(t, cs, "delete", map[string]any{"path": "/a"})

	var fl struct {
		Count int `json:"count"`
	}
	text := mustCall(t, cs, "list_files", map[string]any{"path": "/"})
	decodeJSON(t, text, &fl)
	if fl.Count != 0 {
		t.Errorf("final root count = %d, want 0 (%s)", fl.Count, text)
	}

	mustFailWith(t, cs, "stat", map[string]any{"path": "/a/b/gone.txt"}, "not_found")
}

func TestListPaginationWalksAll(t *testing.T) {
	m := newStatefulMock()
	m.folders["/remote.php/dav/files/alice/big"] = true
	for i := 0; i < 120; i++ {
		m.files["/remote.php/dav/files/alice/big/f"+strconv.Itoa(1000+i)] = "x"
	}
	ts := httptest.NewServer(http.HandlerFunc(m.handler))
	defer ts.Close()
	m.baseURL = ts.URL
	cs, done := newFullTestServer(t, ts.URL, "read")
	defer done()

	seen := map[string]bool{}
	var offsets []int
	off := 0
	for {
		text, isErr := callText(t, cs, "list_files", map[string]any{"path": "/big", "limit": 50, "offset": off})
		if isErr {
			t.Fatalf("list_files: %s", text)
		}
		var out struct {
			Entries []struct {
				Path string `json:"path"`
			} `json:"entries"`
			Next *int `json:"next_offset"`
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, off)
		for _, e := range out.Entries {
			seen[e.Path] = true
		}
		if out.Next == nil {
			break
		}
		if *out.Next <= off {
			t.Fatalf("next_offset %d did not advance from %d", *out.Next, off)
		}
		off = *out.Next
	}
	if len(seen) != 120 {
		t.Errorf("saw %d entries, want 120", len(seen))
	}
	if fmt.Sprint(offsets) != "[0 50 100]" {
		t.Errorf("offsets = %v, want [0 50 100]", offsets)
	}
}

func TestReadOffsetAndNegative(t *testing.T) {
	m := newStatefulMock()
	m.files["/remote.php/dav/files/alice/big.txt"] = strings.Repeat("a", 3<<20)
	ts := httptest.NewServer(http.HandlerFunc(m.handler))
	defer ts.Close()
	m.baseURL = ts.URL
	cs, done := newFullTestServer(t, ts.URL, "read")
	defer done()

	text, isErr := callText(t, cs, "read_file", map[string]any{"path": "/big.txt", "offset": 2 << 20})
	if isErr {
		t.Fatalf("read_file: %s", text)
	}
	if !strings.Contains(text, strings.Repeat("a", 128<<10)) || strings.Contains(text, strings.Repeat("a", 128<<10+1)) {
		t.Errorf("expected exactly 128KiB of content, got %d bytes of response", len(text))
	}
	if _, isErr := callText(t, cs, "read_file", map[string]any{"path": "/big.txt", "offset": -1}); !isErr {
		t.Errorf("negative offset should error")
	}
}

func TestReadFileTextSafety(t *testing.T) {
	m := newStatefulMock()
	// 1023 ASCII bytes then a 3-byte rune straddling a 1024-byte cut.
	m.files["/remote.php/dav/files/alice/u.txt"] = strings.Repeat("a", 1023) + "€€"
	m.files["/remote.php/dav/files/alice/b.bin"] = "ab\x00\x01cd"
	ts := httptest.NewServer(http.HandlerFunc(m.handler))
	defer ts.Close()
	m.baseURL = ts.URL
	cs, done := newFullTestServer(t, ts.URL, "read")
	defer done()

	text, isErr := callText(t, cs, "read_file", map[string]any{"path": "/u.txt", "length": 1024})
	if isErr {
		t.Fatalf("read_file: %s", text)
	}
	var got struct {
		Content    string `json:"content"`
		Bytes      int    `json:"bytes"`
		Truncated  bool   `json:"truncated"`
		NextOffset int64  `json:"next_offset"`
		Trust      string `json:"trust"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if ok := got.Bytes == 1023 && got.NextOffset == 1023 && got.Truncated; !ok || got.Trust != "untrusted" || !utf8.ValidString(got.Content) {
		t.Errorf("unexpected result: bytes=%d next=%d trunc=%v trust=%q", got.Bytes, got.NextOffset, got.Truncated, got.Trust)
	}

	if text, isErr := callText(t, cs, "read_file", map[string]any{"path": "/b.bin"}); !isErr || !strings.Contains(text, "unsupported_type") && !strings.Contains(text, "base64") {
		t.Errorf("binary without base64 should error, got %q (isErr=%v)", text, isErr)
	}
	if text, isErr := callText(t, cs, "read_file", map[string]any{"path": "/b.bin", "encoding": "base64"}); isErr || !strings.Contains(text, "YWIAAWNk") {
		t.Errorf("base64 read failed: %q", text)
	}
}

func TestInstructionsUntrusted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(newStatefulMock().handler))
	defer ts.Close()
	cs, done := newFullTestServer(t, ts.URL, "read")
	defer done()
	if ins := cs.InitializeResult().Instructions; !strings.Contains(ins, "untrusted data") {
		t.Errorf("instructions missing untrusted sentence: %q", ins)
	}
}

const (
	nestedPath = "/a/b/nested.txt"
	targetPath = "/a/b/target.txt"
)
