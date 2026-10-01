// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package sanitize implements the path jail that keeps every WebDAV request
// inside the user's root. It is deliberately conservative: anything it cannot
// prove safe is rejected.
package sanitize

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const maxDecodeRounds = 5

// Sanitize validates and normalizes a user-supplied path, returning the safe
// relative path (always starting with "/") to use in WebDAV requests. It:
//
//  1. rejects NUL bytes and non-UTF8 input,
//  2. percent-decodes recursively (up to maxDecodeRounds) so that
//     %252e%252e encodings cannot smuggle traversal past a single pass,
//  3. normalizes Unicode to NFC,
//  4. collapses // and resolves "." and ".." lexically (path.Clean),
//  5. rejects any path that still escapes the root after cleaning.
//
// An empty input is normalized to "/" (the root).
func Sanitize(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("path contains NUL byte")
	}
	if !utf8.ValidString(p) {
		return "", fmt.Errorf("path is not valid UTF-8")
	}

	decoded, err := recursiveDecode(p)
	if err != nil {
		return "", err
	}
	// Percent-decoding can produce bytes the raw input check never saw
	// (e.g. "%80" or "%00"), so validate the decoded form again.
	if strings.ContainsRune(decoded, 0) {
		return "", fmt.Errorf("path contains NUL byte")
	}
	if !utf8.ValidString(decoded) {
		return "", fmt.Errorf("path is not valid UTF-8")
	}

	decoded = norm.NFC.String(decoded)

	// Treat the input as a path relative to the jail root. A leading "/" is
	// allowed and stripped; everything else must stay inside.
	rel := strings.TrimPrefix(decoded, "/")

	cleaned := path.Clean("/" + rel)

	// After Clean, any escape would manifest as ".." segments or a path that
	// is exactly "/.." — but path.Clean("/a/../..") == "/", so the real check
	// is to re-verify lexically against the decoded (pre-clean) form.
	if escapes(decoded) {
		return "", fmt.Errorf("path escapes the allowed root")
	}

	return cleaned, nil
}

// recursiveDecode percent-decodes until a fixed point is reached or
// maxDecodeRounds is exceeded (an error, since it indicates adversarial
// double-encoding).
func recursiveDecode(s string) (string, error) {
	prev := s
	for i := 0; i < maxDecodeRounds; i++ {
		next, err := url.PathUnescape(prev)
		if err != nil {
			return "", fmt.Errorf("invalid percent-encoding in path")
		}
		if next == prev {
			return next, nil
		}
		prev = next
	}
	if prev != s {
		return "", fmt.Errorf("path still changes after %d decode rounds", maxDecodeRounds)
	}
	return prev, nil
}

// escapes reports whether the raw (decoded) path contains a traversal that
// would leave the root. It works on the decoded string before Clean so that
// sequences like "/a/../../etc" are caught even though Clean would collapse
// them to "/etc".
func escapes(p string) bool {
	seg := strings.Split(strings.TrimPrefix(p, "/"), "/")
	depth := 0
	for _, s := range seg {
		switch s {
		case "", ".":
			continue
		case "..":
			depth--
		default:
			depth++
		}
		if depth < 0 {
			return true
		}
	}
	return false
}
