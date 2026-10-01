// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv

import (
	"encoding/base64"
	"strings"

	"github.com/valdrent/nextcloud-mcp-fast/internal/webdav"
)

// compactEntries renders entries as a minimal JSON-friendly slice to keep
// token usage low: only the fields an LLM needs to act on. Folder paths carry
// a trailing slash so they are unambiguous in listings (the WebDAV href of a
// collection includes it).
func compactEntries(entries []webdav.Entry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		path := e.Path
		if e.IsDir && !strings.HasSuffix(path, "/") {
			path += "/"
		}
		m := map[string]any{
			"path": path,
			"type": "file",
		}
		if e.IsDir {
			m["type"] = "dir"
		}
		if !e.IsDir {
			m["size"] = e.Size
		}
		if e.Modified != "" {
			m["modified"] = e.Modified
		}
		out = append(out, m)
	}
	return out
}

func orRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// looksText heuristically reports whether data is printable text (no NULs in
// the first 8 KiB and a high ratio of printable bytes).
func looksText(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	printable := 0
	for _, b := range data[:n] {
		if b == 0 {
			return false
		}
		if b >= 0x20 || b == '\t' || b == '\n' || b == '\r' {
			printable++
		}
	}
	return printable*10 >= n*9 // >= 90% printable
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	return base64.StdEncoding.DecodeString(s)
}
