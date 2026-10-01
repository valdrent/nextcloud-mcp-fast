// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

// newAuditLogger builds the JSON audit logger. It writes to stderr only:
// stdout carries the stdio MCP transport.
func newAuditLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))
}

// auditMiddleware logs every tools/call: tool, account (host|user, never
// secrets), path arguments, outcome and duration. Mutating tools log at Info,
// read tools at Debug.
func (s *Server) auditMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		start := time.Now()
		res, err := next(ctx, method, req)

		attrs := []any{"duration_ms", time.Since(start).Milliseconds()}
		name := ""
		if ctr, ok := req.(*mcp.CallToolRequest); ok && ctr.Params != nil {
			name = ctr.Params.Name
			var args map[string]any
			_ = json.Unmarshal(ctr.Params.Arguments, &args)
			for _, k := range []string{"path", "from", "to", "overwrite"} {
				if v, ok := args[k]; ok {
					attrs = append(attrs, k, v)
				}
			}
			attrs = append(attrs, "account", s.breakerKey(ctr, nil))
		}
		attrs = append([]any{"tool", name, "outcome", outcomeOf(res, err)}, attrs...)

		level := slog.LevelDebug
		switch name {
		case "write_file", "create_folder", "move_file", "delete":
			level = slog.LevelInfo
		default:
			// Read-only tools are logged at debug level.
		}
		s.log.Log(ctx, level, "tool_call", attrs...)
		return res, err
	}
}

// outcomeOf returns "ok" or the semantic error code ("error" when unknown).
func outcomeOf(res mcp.Result, err error) string {
	if err != nil {
		return "error"
	}
	if ctr, ok := res.(*mcp.CallToolResult); ok && ctr != nil && ctr.IsError {
		if len(ctr.Content) > 0 {
			if tc, ok := ctr.Content[0].(*mcp.TextContent); ok {
				// Error text has the form "<code>: <message>".
				if i := strings.Index(tc.Text, ":"); i > 0 && isCode(tc.Text[:i]) {
					return tc.Text[:i]
				}
			}
		}
		return "error"
	}
	return "ok"
}

func isCode(c string) bool {
	switch ncerr.Code(c) {
	case ncerr.CodePathForbidden, ncerr.CodeNotFound, ncerr.CodeUnauthorized, ncerr.CodeForbidden,
		ncerr.CodePermissionDenied, ncerr.CodeCircuitOpen, ncerr.CodeRateLimited, ncerr.CodeTooLarge,
		ncerr.CodeUnsupportedType, ncerr.CodeBadRequest, ncerr.CodeConflict, ncerr.CodeServerError, ncerr.CodeTimeout:
		return true
	default:
		return false
	}
}
