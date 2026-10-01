// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package webdav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteCreatesParentFolders(t *testing.T) {
	var mkcols []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			mkcols = append(mkcols, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			if !strings.HasSuffix(r.URL.Path, "/sub/deep/nested.txt") {
				t.Errorf("PUT path = %s, want .../sub/deep/nested.txt", r.URL.Path)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.Write(context.Background(), "/sub/deep/nested.txt", strings.NewReader("x"), 1)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := []string{"/remote.php/dav/files/alice/sub", "/remote.php/dav/files/alice/sub/deep"}
	if len(mkcols) != len(want) {
		t.Fatalf("MKCOL calls = %v, want %v", mkcols, want)
	}
	for i := range want {
		if mkcols[i] != want[i] {
			t.Errorf("MKCOL[%d] = %s, want %s", i, mkcols[i], want[i])
		}
	}
}

func TestWriteSkipsExistingParents(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprint(w, `<d:error xmlns:d="DAV:"><s:exception>Sabre\DAV\Exception\MethodNotAllowed</s:exception></d:error>`)
		case http.MethodPut:
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.Write(context.Background(), "/sub/nested.txt", strings.NewReader("x"), 1)
	if err != nil {
		t.Errorf("Write with existing parent should succeed, got: %v", err)
	}
}

func TestReadRangeHeader(t *testing.T) {
	var gotRange string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		gotRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusPartialContent)
		fmt.Fprint(w, "abcdef")
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	body, _, err := c.Read(context.Background(), "/docs/a.txt", 2, 3)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	data := make([]byte, 10)
	n, _ := body.Read(data)
	body.Close()
	if string(data[:n]) != "abcdef" {
		t.Errorf("body = %q, want %q", data[:n], "abcdef")
	}
	if gotRange != "bytes=2-4" {
		t.Errorf("Range header = %q, want %q", gotRange, "bytes=2-4")
	}
}

func TestReadSuffixRange(t *testing.T) {
	var gotRange string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusPartialContent)
		fmt.Fprint(w, "tail")
	}))
	defer ts.Close()

	c, err := NewClient(&Credentials{Host: ts.URL, Username: "alice", Password: "p"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	body, _, err := c.Read(context.Background(), "/docs/a.txt", 10, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	body.Close()
	if gotRange != "bytes=10-" {
		t.Errorf("Range header = %q, want %q", gotRange, "bytes=10-")
	}
}
