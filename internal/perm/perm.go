// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package perm implements the local permission guard. It is independent of
// Nextcloud's own ACLs: it bounds what the MCP server will even attempt, based
// on the configured level (read / write / destructive).
package perm

import (
	"fmt"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

// Level is a permission level.
type Level int

const (
	Read Level = iota
	Write
	Destructive
)

// Guard enforces the configured permission level per operation class.
type Guard struct {
	level Level
}

// New builds a Guard from a config-style level string.
func New(level string) (*Guard, error) {
	switch level {
	case "read":
		return &Guard{level: Read}, nil
	case "write":
		return &Guard{level: Write}, nil
	case "destructive", "full":
		return &Guard{level: Destructive}, nil
	default:
		return nil, fmt.Errorf("unknown permission level %q", level)
	}
}

// Allow checks that the guard's level covers op. It returns a semantic error
// when denied so handlers can return it directly.
func (g *Guard) Allow(op Level) error {
	if g.level >= op {
		return nil
	}
	name := map[Level]string{Read: "read", Write: "write", Destructive: "destructive"}
	return ncerr.New(ncerr.CodePermissionDenied,
		"operation %q requires permission level %q but the server is configured with %q; restart with NEXTCLOUD_MCP_PERMISSIONS=%s to enable it",
		name[op], name[op], name[g.level], name[op])
}
