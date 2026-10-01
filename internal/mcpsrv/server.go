// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package mcpsrv wires the MCP server: tools, permission guard, circuit
// breaker, and per-request credential resolution (single-user or pass-through).
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/valdrent/nextcloud-mcp-fast/internal/accounts"
	"github.com/valdrent/nextcloud-mcp-fast/internal/breaker"
	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
	"github.com/valdrent/nextcloud-mcp-fast/internal/perm"
	"github.com/valdrent/nextcloud-mcp-fast/internal/sanitize"
	"github.com/valdrent/nextcloud-mcp-fast/internal/webdav"
)

// Server bundles everything a tool handler needs.
type Server struct {
	cfg     *config.Config
	reg     *accounts.Registry
	guard   *perm.Guard
	breaker *breaker.Breaker
}

// New builds a Server from config.
func New(cfg *config.Config, reg *accounts.Registry) (*Server, error) {
	guard, err := perm.New(cfg.Permissions)
	if err != nil {
		return nil, err
	}
	ensureDefaults(cfg)
	breakerThreshold := cfg.CircuitBreakerThreshold
	if breakerThreshold <= 0 {
		breakerThreshold = 5
	}
	breakerWindow := cfg.CircuitBreakerWindow
	if breakerWindow <= 0 {
		breakerWindow = time.Minute
	}
	return &Server{
		cfg:     cfg,
		reg:     reg,
		guard:   guard,
		breaker: breaker.New(breakerThreshold, breakerWindow),
	}, nil
}

// ensureDefaults fills in the documented defaults for MaxReadBytes and
// MaxListEntries when the config was built without going through config.Load
// (e.g. tests or embedded use). Zero values are replaced with the same
// defaults Load applies.
func ensureDefaults(cfg *config.Config) {
	if cfg.MaxReadBytes <= 0 {
		cfg.MaxReadBytes = 1 << 20
	}
	if cfg.MaxListEntries <= 0 {
		cfg.MaxListEntries = 50
	}
}

// BuildMCP creates the MCP server with all tools registered.
func (s *Server) BuildMCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "nextcloud-mcp-fast",
		Version: s.cfg.Version,
	}, &mcp.ServerOptions{
		Instructions: instructions(s.cfg),
	})

	mcp.AddTool(srv, tool("list_files", "List files and folders in a directory (paginated). Returns compact entries: path, name, isDir, size, modified.", perm.Read, true), s.handleList)
	mcp.AddTool(srv, tool("read_file", "Read a file's content. Supports offset/length for partial reads of large files. Binary or oversized content is rejected with a clear error.", perm.Read, true), s.handleRead)
	mcp.AddTool(srv, tool("write_file", "Create or overwrite a file at the given path. Parent folders are created automatically if missing.", perm.Write, false), s.handleWrite)
	mcp.AddTool(srv, tool("create_folder", "Create a folder.", perm.Write, false), s.handleMkdir)
	mcp.AddTool(srv, tool("move_file", "Move or rename a file/folder. Use overwrite=true to replace an existing destination.", perm.Write, false), s.handleMove)
	mcp.AddTool(srv, tool("delete", "Delete a file or folder (folders are deleted recursively by Nextcloud). Destructive: requires the 'destructive' permission level.", perm.Destructive, false), s.handleDelete)
	mcp.AddTool(srv, tool("search_files", "Search for files/folders by name substring within a directory tree (bounded depth).", perm.Read, true), s.handleSearch)
	mcp.AddTool(srv, tool("stat", "Get metadata (size, modified, type) for a single path.", perm.Read, true), s.handleStat)

	return srv
}

func tool(name, desc string, level perm.Level, readOnly bool) *mcp.Tool {
	destr := level == perm.Destructive
	return &mcp.Tool{
		Name:        name,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destr,
		},
	}
}

// credFromHeaders extracts pass-through credentials from HTTP headers.
func credFromHeaders(h http.Header) (host, user, pass string) {
	host = h.Get("X-Nextcloud-Host")
	user = h.Get("X-Nextcloud-Username")
	pass = h.Get("X-Nextcloud-Password")
	return
}

// resolveClient resolves credentials for a request. In single-user mode the
// default account is always used. In passthrough mode the caller must supply
// a complete set of credentials (host, username and password) via headers or
// arguments; they are never mixed with the configured defaults, so a partial
// override can neither leak the default App Password to another host nor
// bypass the breaker by swapping one field at a time.
func (s *Server) resolveClient(ctx context.Context, req *mcp.CallToolRequest, extra map[string]any) (*webdav.Client, error) {
	host, user, pass := s.credsFor(req, extra)
	return s.reg.Resolve(ctx, host, user, pass)
}

// credsFor returns the effective credentials for a request (see resolveClient).
// In passthrough mode credentials come only from HTTP headers (X-Nextcloud-*);
// they are never accepted as tool arguments, so an LLM cannot read or echo a
// password it saw in another context.
func (s *Server) credsFor(req *mcp.CallToolRequest, _ map[string]any) (host, user, pass string) {
	if !s.cfg.AllowPassthrough {
		return s.cfg.Host, s.cfg.Username, s.cfg.Password
	}

	var h http.Header
	if req != nil && req.GetExtra() != nil {
		h = req.GetExtra().Header
	}
	host, user, pass = credFromHeaders(h)
	return host, user, pass
}

// breakerKey derives the per-account circuit-breaker key from the same
// credential source as resolveClient, so the breaker cannot be evaded by
// varying a single field.
func (s *Server) breakerKey(req *mcp.CallToolRequest, extra map[string]any) string {
	host, user, _ := s.credsFor(req, extra)
	return host + "|" + user
}

// guardAndBreaker runs the permission check and breaker for an operation.
func (s *Server) guardAndBreaker(req *mcp.CallToolRequest, extra map[string]any, op perm.Level) error {
	if err := s.guard.Allow(op); err != nil {
		return err
	}
	return s.breaker.Check(s.breakerKey(req, extra))
}

// recordOutcome feeds the breaker with success/failure.
func (s *Server) recordOutcome(req *mcp.CallToolRequest, extra map[string]any, err error) {
	if err == nil {
		s.breaker.Forget(s.breakerKey(req, extra))
		return
	}
	var se *ncerr.Error
	if errors.As(err, &se) && (se.Code == ncerr.CodeCircuitOpen || se.Code == ncerr.CodePermissionDenied) {
		return // local policy errors don't count against the account
	}
	s.breaker.Record(s.breakerKey(req, extra))
}

// run is the common prologue for every handler: permission + breaker check,
// credential resolution, path sanitization. It returns the client and the
// sanitized path, or a semantic error.
func (s *Server) run(ctx context.Context, req *mcp.CallToolRequest, extra map[string]any, op perm.Level, rawPath string) (*webdav.Client, string, error) {
	if err := s.guardAndBreaker(req, extra, op); err != nil {
		return nil, "", err
	}
	p, err := sanitize.Sanitize(rawPath)
	if err != nil {
		return nil, "", ncerr.New(ncerr.CodePathForbidden, "%v", err)
	}
	client, err := s.resolveClient(ctx, req, extra)
	if err != nil {
		// Only genuine auth failures count; local policy errors (e.g. host
		// not allowed) must not feed the breaker with attacker-chosen keys.
		if ncerr.Is(err, ncerr.CodeUnauthorized) {
			s.recordOutcome(req, extra, err)
		}
		return nil, "", err
	}
	return client, p, nil
}

// textResult wraps a string as MCP text content.
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// jsonResult marshals v to compact JSON text content.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

func instructions(cfg *config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Nextcloud Files MCP server (permission level: %s). ", cfg.Permissions)
	b.WriteString("All paths are relative to the account's file root and must start with '/'. ")
	b.WriteString("Use list_files to explore, read_file for content (supports offset/length), write_file to create/overwrite, move_file to rename, and delete to remove. ")
	if cfg.AllowPassthrough {
		b.WriteString("Multi-account pass-through is enabled: the transport layer selects the account per request (via X-Nextcloud-* headers); no credentials are needed in tool arguments. ")
	}
	return b.String()
}
