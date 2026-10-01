// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package perm

import (
	"testing"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

func TestNew(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"read", false},
		{"write", false},
		{"destructive", false},
		{"full", false},
		{"", true},
		{"admin", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := New(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("New(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestAllowMatrix(t *testing.T) {
	cases := []struct {
		level string
		op    Level
		allow bool
	}{
		{"read", Read, true},
		{"read", Write, false},
		{"read", Destructive, false},
		{"write", Read, true},
		{"write", Write, true},
		{"write", Destructive, false},
		{"destructive", Read, true},
		{"destructive", Write, true},
		{"destructive", Destructive, true},
	}
	for _, tc := range cases {
		t.Run(tc.level+"->"+opName(tc.op), func(t *testing.T) {
			g, err := New(tc.level)
			if err != nil {
				t.Fatalf("New(%q): %v", tc.level, err)
			}
			err = g.Allow(tc.op)
			if tc.allow {
				if err != nil {
					t.Fatalf("Allow(%s) unexpected error: %v", opName(tc.op), err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Allow(%s) expected permission_denied, got nil", opName(tc.op))
			}
			if !ncerr.Is(err, ncerr.CodePermissionDenied) {
				t.Fatalf("expected code %s, got: %v", ncerr.CodePermissionDenied, err)
			}
		})
	}
}

func opName(l Level) string {
	switch l {
	case Read:
		return "read"
	case Write:
		return "write"
	case Destructive:
		return "destructive"
	default:
		return "?"
	}
}
