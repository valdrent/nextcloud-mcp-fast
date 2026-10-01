// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package webdav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSearchUsesDAVSearch(t *testing.T) {
	var gotBody string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "SEARCH" {
			t.Errorf("method = %s, want SEARCH", r.Method)
		}
		if r.URL.Path != "/remote.php/dav/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/xml") {
			t.Errorf("content-type = %s", ct)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/remote.php/dav/files/alice/docs/a%3Cb%26c.txt</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>3</d:getcontentlength></d:prop></d:propstat></d:response>
<d:response><d:href>/remote.php/dav/files/alice/docs/x/y/z/deep.txt</d:href><d:propstat><d:prop><d:resourcetype/></d:prop></d:propstat></d:response>
</d:multistatus>`)
	})
	res, err := c.Search(context.Background(), "/docs", `a<b&c`, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Path != "/docs/a<b&c.txt" || res.Entries[0].Size != 3 {
		t.Fatalf("entries = %+v (deep entry must be filtered by depth)", res.Entries)
	}
	for _, want := range []string{
		"<d:href>/files/alice/docs</d:href>", "<d:depth>infinity</d:depth>",
		"%a&lt;b&amp;c%", "<d:nresults>40</d:nresults>", "d:like",
	} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %q:\n%s", want, gotBody)
		}
	}
}

func TestSearchFallsBackToBFS(t *testing.T) {
	var searches int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "SEARCH" {
			searches++
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		// One big folder (250 entries) with the match at the very end.
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
		b.WriteString(`<d:response><d:href>/remote.php/dav/files/alice/big/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>`)
		for i := 0; i < 250; i++ {
			name := fmt.Sprintf("f%03d.txt", i)
			if i == 240 {
				name = "needle.txt"
			}
			fmt.Fprintf(&b, `<d:response><d:href>/remote.php/dav/files/alice/big/%s</d:href><d:propstat><d:prop><d:resourcetype/></d:prop></d:propstat></d:response>`, name)
		}
		b.WriteString(`</d:multistatus>`)
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, b.String())
	})
	res, err := c.Search(context.Background(), "/big", "NEEDLE", 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if searches != 1 {
		t.Errorf("SEARCH attempts = %d, want 1", searches)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "needle.txt" {
		t.Fatalf("entries = %+v, want needle.txt beyond first 200", res.Entries)
	}
}

func TestSearchWildcardPostFilter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">
<d:response><d:href>/remote.php/dav/files/alice/abc.txt</d:href><d:propstat><d:prop><d:resourcetype/></d:prop></d:propstat></d:response>
<d:response><d:href>/remote.php/dav/files/alice/A_C.txt</d:href><d:propstat><d:prop><d:resourcetype/></d:prop></d:propstat></d:response>
</d:multistatus>`)
	})
	res, err := c.Search(context.Background(), "/", "a_c", 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "A_C.txt" {
		t.Fatalf("entries = %+v, want only A_C.txt", res.Entries)
	}
}

func TestSearchScopeEscaped(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"></d:multistatus>`)
	}))
	defer ts.Close()
	c, _ := NewClient(&Credentials{Host: ts.URL, Username: "al ice", Password: "p"}, &http.Client{})
	if _, err := c.Search(context.Background(), "/a#b/c d", "x", 2, 5); err != nil {
		t.Fatal(err)
	}
	want := "<d:href>/files/al%20ice/a%23b/c%20d</d:href>"
	if !strings.Contains(gotBody, want) {
		t.Errorf("body missing %q:\n%s", want, gotBody)
	}
}

func TestLimitedReaderExactAndOver(t *testing.T) {
	read := func(n, limit int) error {
		lr := &limitedReader{rc: io.NopCloser(strings.NewReader(strings.Repeat("x", n))), limit: int64(limit)}
		_, err := io.ReadAll(lr)
		return err
	}
	if err := read(100, 100); err != nil {
		t.Errorf("exact-size body: %v", err)
	}
	if err := read(101, 100); err == nil {
		t.Errorf("limit+1 body should fail")
	}
}
