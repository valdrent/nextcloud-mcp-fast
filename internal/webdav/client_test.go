// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package webdav

import (
	"net/http"
	"testing"
)

func TestToRel(t *testing.T) {
	const user = "alice"
	cases := []struct {
		href string
		want string
		ok   bool
	}{
		{"/remote.php/dav/files/alice/", "/", true},
		{"/remote.php/dav/files/alice/docs", "/docs", true},
		{"/remote.php/dav/files/alice/docs/notes.txt", "/docs/notes.txt", true},
		{"/remote.php/dav/files/bob/secret", "", false}, // other user rejected
		{"/other/path", "", false},                      // wrong prefix
	}
	for _, tc := range cases {
		t.Run(tc.href, func(t *testing.T) {
			got, ok := toRel(tc.href, user)
			if ok != tc.ok || got != tc.want {
				t.Errorf("toRel(%q) = (%q, %v), want (%q, %v)", tc.href, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"/docs/notes.txt": "notes.txt",
		"/docs/":          "docs",
		"/file.md":        "file.md",
		"/":               "",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeDir(t *testing.T) {
	cases := map[string]string{
		"":       "/",
		"/":      "/",
		"docs":   "/docs",
		"/docs/": "/docs",
	}
	for in, want := range cases {
		if got := normalizeDir(in); got != want {
			t.Errorf("normalizeDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestURL(t *testing.T) {
	c, err := NewClient(&Credentials{Host: "https://cloud.example.com", Username: "alice"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got := c.URL("/docs/notes.txt")
	want := "https://cloud.example.com/remote.php/dav/files/alice/docs/notes.txt"
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestCredentialsID(t *testing.T) {
	c := &Credentials{Host: "https://cloud.example.com", Username: "alice"}
	if got := c.ID(); got != "https://cloud.example.com|alice" {
		t.Errorf("ID() = %q", got)
	}
}

func TestURLEscapesSegments(t *testing.T) {
	c, err := NewClient(&Credentials{Host: "https://cloud.example.com", Username: "ali ce"}, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	cases := map[string]string{
		"/docs/notes.txt":  "https://cloud.example.com/remote.php/dav/files/ali%20ce/docs/notes.txt",
		"/a#b/c?d/e%f g":   "https://cloud.example.com/remote.php/dav/files/ali%20ce/a%23b/c%3Fd/e%25f%20g",
		"/":                "https://cloud.example.com/remote.php/dav/files/ali%20ce",
		"":                 "https://cloud.example.com/remote.php/dav/files/ali%20ce",
		"/docs//notes.txt": "https://cloud.example.com/remote.php/dav/files/ali%20ce/docs/notes.txt",
	}
	for in, want := range cases {
		if got := c.URL(in); got != want {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeHref(t *testing.T) {
	cases := map[string]string{
		"/remote.php/dav/files/alice/docs/notes%20with%20spaces.txt": "/remote.php/dav/files/alice/docs/notes with spaces.txt",
		"/remote.php/dav/files/alice/plain":                          "/remote.php/dav/files/alice/plain",
		"/remote.php/dav/files/alice/a%zz":                           "/remote.php/dav/files/alice/a%zz", // invalid: unchanged
	}
	for in, want := range cases {
		if got := decodeHref(in); got != want {
			t.Errorf("decodeHref(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToRelDecoded(t *testing.T) {
	got, ok := toRel(decodeHref("/remote.php/dav/files/alice/docs/my%20file.txt"), "alice")
	if !ok || got != "/docs/my file.txt" {
		t.Errorf("toRel(decoded) = (%q, %v), want (\"/docs/my file.txt\", true)", got, ok)
	}
}
