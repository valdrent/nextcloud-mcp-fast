// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package sanitize

import "testing"

func TestSanitize(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "/", false},
		{"/", "/", false},
		{"docs", "/docs", false},
		{"/docs/report.pdf", "/docs/report.pdf", false},
		{"a/b/../c", "/a/c", false},
		{"./x", "/x", false},
		{"//docs//file.txt", "/docs/file.txt", false},
		{"docs/..", "/", false},
		{"../etc/passwd", "", true},
		{"/../etc/passwd", "", true},
		{"a/../../b", "", true},
		{"%2e%2e/etc/passwd", "", true},          // single-encoded ..
		{"%252e%252e/etc/passwd", "", true},      // double-encoded ..
		{"docs/%2e%2e/secret", "/secret", false}, // encoded .. resolves inside jail
		{"/a\x00b", "", true},                    // NUL
		{"my file.txt", "/my file.txt", false},
		{"café/resume.pdf", "/café/resume.pdf", false},
	}
	for _, tc := range cases {
		got, err := Sanitize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Sanitize(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Sanitize(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
