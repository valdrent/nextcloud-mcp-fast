// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package sanitize

import (
	"path"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzSanitize is a fuzz test for the Sanitize function.
// It checks invariants: when Sanitize returns no error, the result:
// - starts with "/"
// - equals path.Clean(result)
// - contains no ".." segment
// - contains no NUL byte
// - is valid UTF-8
// Sanitize must never panic.
func FuzzSanitize(f *testing.F) {
	// Seed corpus with various inputs to maximize coverage
	corpus := []string{
		"",
		"/",
		"a/b",
		"../x",
		"/a/../../b",
		"%2e%2e/x",
		"%252e%252e%252fetc",
		"a\x00b",
		"ñ/文件",
		"//a//b/",
		".",
		"..",
		"/.",
		"/..",
		"a",
		"/a",
		"/a/b/c",
		"a/../b",
		"/a/../b",
		"a/./b",
		"/a/./b",
		"/a/b/../../c",
		"//double//slash",
		"/./a/./b/.",
		"%2e",
		"%2e%2e",
		"%2F",
		"%",
		"%2",
		"%2G",
		"file%20name",
		"path%2Fto%2Ffile",
		"normal.txt",
		"/normal.txt",
		"path/to/file.txt",
		"/path/to/file.txt",
	}

	for _, seed := range corpus {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// The function should never panic
		result, err := Sanitize(input)

		if err != nil {
			// When there's an error, we just ensure it doesn't panic
			// No further checks needed
			return
		}

		// When Sanitize returns no error, check invariants

		checkInvariants(t, result)
	})
}

// checkInvariants asserts the documented guarantees of a successful Sanitize.
func checkInvariants(t *testing.T, result string) {
	t.Helper()
	if !strings.HasPrefix(result, "/") {
		t.Errorf("result does not start with /: %q", result)
	}
	if path.Clean(result) != result {
		t.Errorf("result != path.Clean(result): %q != %q", result, path.Clean(result))
	}
	for _, seg := range strings.Split(strings.TrimPrefix(result, "/"), "/") {
		if seg == ".." {
			t.Errorf("result contains .. segment: %q", result)
		}
	}
	if strings.ContainsRune(result, 0) {
		t.Errorf("result contains NUL byte: %q", result)
	}
	if !utf8.ValidString(result) {
		t.Errorf("result is not valid UTF-8: %q", result)
	}
}
