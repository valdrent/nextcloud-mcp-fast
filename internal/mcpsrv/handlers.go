// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
	"github.com/valdrent/nextcloud-mcp-fast/internal/perm"
	"github.com/valdrent/nextcloud-mcp-fast/internal/sanitize"
)

// listArgs are the arguments for list_files.
type listArgs struct {
	Path   string `json:"path" jsonschema:"Directory to list, relative to root (default '/')"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max entries to return (default 50, max 200)"`
	Offset int    `json:"offset,omitempty" jsonschema:"Skip this many entries (for pagination)"`
}

func (s *Server) handleList(ctx context.Context, req *mcp.CallToolRequest, a listArgs) (*mcp.CallToolResult, any, error) {
	if a.Offset < 0 {
		return nil, nil, ncerr.New(ncerr.CodeBadRequest, "offset must not be negative")
	}
	client, p, err := s.run(ctx, req, nil, perm.Read, orRoot(a.Path))
	if err != nil {
		return nil, nil, err
	}
	limit := a.Limit
	if limit <= 0 {
		limit = s.cfg.MaxListEntries
	}

	res, err := client.List(ctx, p, a.Offset, limit)
	s.recordOutcome(req, nil, err)
	if err != nil {
		return nil, nil, err
	}

	out := map[string]any{
		"path":    p,
		"count":   len(res.Entries),
		"entries": compactEntries(res.Entries),
	}
	if res.Next != "" {
		out["next_offset"] = a.Offset + len(res.Entries)
	}
	r, err := jsonResult(out)
	return r, nil, err
}

// readArgs are the arguments for read_file.
type readArgs struct {
	Path     string `json:"path" jsonschema:"File to read, relative to root"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"Start byte (0-based)"`
	Length   int64  `json:"length,omitempty" jsonschema:"Max bytes to read (default: up to the configured cap)"`
	Encoding string `json:"encoding,omitempty" jsonschema:"'text' (default) or 'base64' for binary"`
}

// rangeTotal extracts the total size from a Content-Range header
// ("bytes start-end/total"), or -1 when absent or unknown.
func rangeTotal(cr string) int64 {
	total := int64(-1)
	if i := strings.Index(cr, "/"); i >= 0 {
		if _, err := fmt.Sscanf(cr[i+1:], "%d", &total); err != nil {
			return -1
		}
	}
	return total
}

func binaryFileError(contentType string, n int) error {
	if contentType != "" {
		contentType += ", "
	}
	return ncerr.New(ncerr.CodeUnsupportedType, "file looks binary (%s%d bytes); re-read with encoding=base64 if you really need the raw bytes", contentType, n)
}

func (s *Server) handleRead(ctx context.Context, req *mcp.CallToolRequest, a readArgs) (*mcp.CallToolResult, any, error) {
	if a.Offset < 0 || a.Length < 0 {
		return nil, nil, ncerr.New(ncerr.CodeBadRequest, "offset and length must not be negative")
	}
	client, p, err := s.run(ctx, req, nil, perm.Read, a.Path)
	if err != nil {
		return nil, nil, err
	}
	length := a.Length
	if length <= 0 || length > s.cfg.MaxReadBytes {
		length = s.cfg.MaxReadBytes
	}

	body, hdr, err := client.Read(ctx, p, a.Offset, length)
	if err != nil {
		s.recordOutcome(req, nil, err)
		return nil, nil, err
	}
	defer body.Close()

	limit := length + 4096 // small slack for chunk boundaries
	data, err := io.ReadAll(io.LimitReader(body, limit))
	if err != nil {
		s.recordOutcome(req, nil, err)
		return nil, nil, ncerr.New(ncerr.CodeTooLarge, "failed reading file: %v", err)
	}
	s.recordOutcome(req, nil, nil)

	total := rangeTotal(hdr.Get("Content-Range"))

	if int64(len(data)) > length {
		data = data[:length] // server ignored the range; honor the cap
	}
	truncated := int64(len(data)) >= length && (total < 0 || int64(len(data))+a.Offset < total)
	out := map[string]any{"path": p}
	if a.Encoding == "base64" {
		out["encoding"] = "base64"
		out["content"] = encodeBase64(data)
	} else {
		text, ok := textPrefix(data, truncated)
		if !ok {
			return nil, nil, binaryFileError(hdr.Get("Content-Type"), len(data))
		}
		data = text
		out["encoding"] = "text"
		out["content"] = string(data)
		out["trust"] = "untrusted"
	}
	out["bytes"] = len(data)
	out["truncated"] = truncated
	if truncated {
		out["next_offset"] = a.Offset + int64(len(data))
	}
	r, err := jsonResult(out)
	return r, nil, err
}

// writeArgs are the arguments for write_file.
type writeArgs struct {
	Path      string `json:"path" jsonschema:"Destination file path, relative to root"`
	Content   string `json:"content" jsonschema:"File content (text or base64 when encoding=base64)"`
	Encoding  string `json:"encoding,omitempty" jsonschema:"'text' (default) or 'base64'"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"Replace the file if it exists (default false; requires the 'destructive' permission level)"`
}

func (s *Server) handleWrite(ctx context.Context, req *mcp.CallToolRequest, a writeArgs) (*mcp.CallToolResult, any, error) {
	if a.Overwrite {
		if err := s.allow(req, perm.Destructive); err != nil {
			return nil, nil, err
		}
	}
	client, p, err := s.run(ctx, req, nil, perm.Write, a.Path)
	if err != nil {
		return nil, nil, err
	}

	var data []byte
	if a.Encoding == "base64" {
		data, err = decodeBase64(a.Content)
		if err != nil {
			return nil, nil, ncerr.New(ncerr.CodeBadRequest, "invalid base64 content: %v", err)
		}
	} else {
		data = []byte(a.Content)
	}

	err = client.Write(ctx, p, strings.NewReader(string(data)), int64(len(data)), a.Overwrite)
	s.recordOutcome(req, nil, err)
	if err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("wrote %d bytes to %s", len(data), p)), nil, nil
}

// mkdirArgs are the arguments for create_folder.
type mkdirArgs struct {
	Path string `json:"path" jsonschema:"Folder path to create, relative to root"`
}

func (s *Server) handleMkdir(ctx context.Context, req *mcp.CallToolRequest, a mkdirArgs) (*mcp.CallToolResult, any, error) {
	client, p, err := s.run(ctx, req, nil, perm.Write, a.Path)
	if err != nil {
		return nil, nil, err
	}
	err = client.MakeDir(ctx, p)
	s.recordOutcome(req, nil, err)
	if err != nil {
		return nil, nil, err
	}
	return textResult("created folder " + p), nil, nil
}

// moveArgs are the arguments for move_file.
type moveArgs struct {
	From      string `json:"from" jsonschema:"Source path, relative to root"`
	To        string `json:"to" jsonschema:"Destination path, relative to root"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"Replace destination if it exists (requires the 'destructive' permission level)"`
}

func (s *Server) handleMove(ctx context.Context, req *mcp.CallToolRequest, a moveArgs) (*mcp.CallToolResult, any, error) {
	if a.Overwrite {
		if err := s.allow(req, perm.Destructive); err != nil {
			return nil, nil, err
		}
	}
	client, from, err := s.run(ctx, req, nil, perm.Write, a.From)
	if err != nil {
		return nil, nil, err
	}
	to, err := sanitize.Sanitize(a.To)
	if err != nil {
		return nil, nil, ncerr.New(ncerr.CodePathForbidden, "%v", err)
	}
	err = client.Move(ctx, from, to, a.Overwrite)
	s.recordOutcome(req, nil, err)
	if err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("moved %s -> %s", from, to)), nil, nil
}

// deleteArgs are the arguments for delete.
type deleteArgs struct {
	Path string `json:"path" jsonschema:"File or folder to delete (folders are deleted recursively), relative to root"`
}

func (s *Server) handleDelete(ctx context.Context, req *mcp.CallToolRequest, a deleteArgs) (*mcp.CallToolResult, any, error) {
	client, p, err := s.run(ctx, req, nil, perm.Destructive, a.Path)
	if err != nil {
		return nil, nil, err
	}
	err = client.Delete(ctx, p)
	s.recordOutcome(req, nil, err)
	if err != nil {
		return nil, nil, err
	}
	return textResult("deleted " + p), nil, nil
}

// searchArgs are the arguments for search_files.
type searchArgs struct {
	Path     string `json:"path,omitempty" jsonschema:"Directory to search in (default '/')"`
	Query    string `json:"query" jsonschema:"Case-insensitive name substring to match"`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"How deep to descend (default 3, max 6)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Max matches (default 50, max 200)"`
}

func (s *Server) handleSearch(ctx context.Context, req *mcp.CallToolRequest, a searchArgs) (*mcp.CallToolResult, any, error) {
	client, p, err := s.run(ctx, req, nil, perm.Read, orRoot(a.Path))
	if err != nil {
		return nil, nil, err
	}
	depth := a.MaxDepth
	if depth <= 0 {
		depth = 3
	}
	if depth > 6 {
		depth = 6
	}
	limit := a.Limit
	if limit <= 0 {
		limit = s.cfg.MaxListEntries
	}

	res, err := client.Search(ctx, p, a.Query, depth, limit)
	s.recordOutcome(req, nil, err)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{
		"query":   a.Query,
		"count":   len(res.Entries),
		"entries": compactEntries(res.Entries),
	}
	r, err := jsonResult(out)
	return r, nil, err
}

// statArgs are the arguments for stat.
type statArgs struct {
	Path string `json:"path" jsonschema:"Path to inspect, relative to root"`
}

func (s *Server) handleStat(ctx context.Context, req *mcp.CallToolRequest, a statArgs) (*mcp.CallToolResult, any, error) {
	client, p, err := s.run(ctx, req, nil, perm.Read, a.Path)
	if err != nil {
		return nil, nil, err
	}
	e, err := client.Stat(ctx, p)
	s.recordOutcome(req, nil, err)
	if err != nil {
		return nil, nil, err
	}
	path := e.Path
	if e.IsDir && !strings.HasSuffix(path, "/") {
		path += "/"
	}
	out := map[string]any{
		"path":        path,
		"name":        e.Name,
		"isDir":       e.IsDir,
		"size":        e.Size,
		"modified":    e.Modified,
		"contentType": e.ContentType,
	}
	r, err := jsonResult(out)
	return r, nil, err
}

// --- helpers ---
