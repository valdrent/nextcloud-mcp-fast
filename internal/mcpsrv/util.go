// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv

import (
	"bytes"
	"encoding/base64"
	"strings"
	"unicode/utf8"

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

// textPrefix reports whether data is text (no NUL bytes, valid UTF-8) and
// returns the longest prefix that does not end inside a UTF-8 sequence. When
// truncated is true, up to 3 trailing bytes forming an incomplete rune (the
// read was cut mid-character) are tolerated and trimmed from the result.
func textPrefix(data []byte, truncated bool) ([]byte, bool) {
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, false
	}
	if truncated {
		for k := 1; k <= utf8.UTFMax-1 && k <= len(data); k++ {
			if utf8.RuneStart(data[len(data)-k]) {
				if !utf8.FullRune(data[len(data)-k:]) {
					data = data[:len(data)-k]
				}
				break
			}
		}
	}
	if !utf8.Valid(data) {
		return nil, false
	}
	return data, true
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	return base64.StdEncoding.DecodeString(s)
}
