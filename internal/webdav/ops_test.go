// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package webdav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

// propfindMock serves a PROPFIND response with n entries for user "alice".
func propfindMock(n int) *httptest.Server {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/remote.php/dav/files/alice/docs</D:href>
    <D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:propstat>
  </D:response>
`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `  <D:response>
    <D:href>/remote.php/dav/files/alice/docs/f%03d.txt</D:href>
    <D:propstat><D:prop>
      <D:resourcetype/>
      <D:getcontentlength>1</D:getcontentlength>
    </D:prop></D:propstat>
  </D:response>
`, i)
	}
	b.WriteString(`</D:multistatus>`)
	body := b.String()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, body)
	}))
}

func TestListTotalAndPagination(t *testing.T) {
	ts := propfindMock(5)
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	res, err := c.List(context.Background(), "/docs", 0, 200)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Entries) != 5 {
		t.Errorf("len(Entries) = %d, want 5", len(res.Entries))
	}
	if res.Next != "" {
		t.Errorf("Next = %q, want empty (all entries fit)", res.Next)
	}

	res, err = c.List(context.Background(), "/docs", 0, 3)
	if err != nil {
		t.Fatalf("List limit 3: %v", err)
	}
	if len(res.Entries) != 3 {
		t.Errorf("len(Entries) = %d, want 3", len(res.Entries))
	}
	if res.Next != "3" {
		t.Errorf("Next = %q, want %q", res.Next, strconv.Itoa(3))
	}

	res, err = c.List(context.Background(), "/docs", 2, 2)
	if err != nil {
		t.Fatalf("List offset 2 limit 2: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Errorf("len(Entries) = %d, want 2", len(res.Entries))
	}
	if res.Next != "4" {
		t.Errorf("Next = %q, want %q", res.Next, strconv.Itoa(4))
	}
	if res.Entries[0].Name != "f002.txt" || res.Entries[1].Name != "f003.txt" {
		t.Errorf("entries at offset 2 = %v, want f002/f003", names(res.Entries))
	}

	res, err = c.List(context.Background(), "/docs", 4, 2)
	if err != nil {
		t.Fatalf("List offset 4 limit 2: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Errorf("len(Entries) = %d, want 1 (last entry)", len(res.Entries))
	}
	if res.Next != "" {
		t.Errorf("Next = %q, want empty (end of folder)", res.Next)
	}
}

func names(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func TestListLimitClampedTo200(t *testing.T) {
	ts := propfindMock(5)
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// A limit above 200 must clamp to 200, not collapse to the default.
	res, err := c.List(context.Background(), "/docs", 0, 500)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Entries) != 5 {
		t.Errorf("len(Entries) = %d, want 5 (limit clamped to 200, not reset)", len(res.Entries))
	}
}

func TestMakeDirExistingFolderIsConflict(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "MKCOL" {
			t.Errorf("method = %s, want MKCOL", r.Method)
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprint(w, `<?xml version="1.0"?>
<d:error xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns">
  <s:exception>Sabre\DAV\Exception\MethodNotAllowed</s:exception>
  <s:message>Cannot create folder because it already exists</s:message>
</d:error>`)
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.MakeDir(context.Background(), "/docs")
	if !ncerr.Is(err, ncerr.CodeConflict) {
		t.Errorf("MakeDir(existing) error = %v, want code conflict", err)
	}
}

func TestMoveNoOverwriteExistingDestIsConflict(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "MOVE" {
			t.Errorf("method = %s, want MOVE", r.Method)
		}
		if got := r.Header.Get("Overwrite"); got != "F" {
			t.Errorf("Overwrite header = %q, want F", got)
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusPreconditionFailed)
		fmt.Fprint(w, `<?xml version="1.0"?>
<d:error xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns">
  <s:exception>Sabre\DAV\Exception\PreconditionFailed</s:exception>
  <s:message>The destination file exists already.</s:message>
</d:error>`)
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.Move(context.Background(), "/docs/a.txt", "/docs/b.txt", false)
	if !ncerr.Is(err, ncerr.CodeConflict) {
		t.Errorf("Move(overwrite=false, dest exists) error = %v, want code conflict", err)
	}
}

func TestStatNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			t.Errorf("method = %s, want PROPFIND", r.Method)
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `<?xml version="1.0"?>
<d:error xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns">
  <s:exception>Sabre\DAV\Exception\NotFound</s:exception>
  <s:message>File with name //nope could not be located</s:message>
</d:error>`)
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.Stat(context.Background(), "/nope")
	if !ncerr.Is(err, ncerr.CodeNotFound) {
		t.Errorf("Stat(missing) error = %v, want code not_found", err)
	}
}

func TestDeleteMissingIsNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.Delete(context.Background(), "/nope")
	if !ncerr.Is(err, ncerr.CodeNotFound) {
		t.Errorf("Delete(missing) error = %v, want code not_found", err)
	}
}
