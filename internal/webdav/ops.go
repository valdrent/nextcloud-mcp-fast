// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package webdav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

// Entry is one row of a PROPFIND result.
type Entry struct {
	Path        string // relative to jail root, starts with "/"
	Name        string
	IsDir       bool
	Size        int64
	Modified    string // RFC 3339 when known
	ContentType string
}

// ListResult is a compact, paginated directory listing.
type ListResult struct {
	Entries []Entry
	Next    string // opaque continuation token ("" when done)
	Total   int    // total entries in this folder (-1 if unknown)
}

const propfindBody = `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:resourcetype/>
    <d:getcontentlength/>
    <d:getlastmodified/>
    <d:getcontenttype/>
  </d:prop>
</d:propfind>`

// propResponse mirrors the subset of a DAV:multistatus we need.
type propResponse struct {
	Responses []propEntry `xml:"response"`
}

type propEntry struct {
	Href     string   `xml:"href"`
	Propstat propStat `xml:"propstat"`
}

type propStat struct {
	Prop davProp `xml:"prop"`
}

type davProp struct {
	Resourcetype     resourceType `xml:"resourcetype"`
	GetContentLength string       `xml:"getcontentlength"`
	GetLastModified  string       `xml:"getlastmodified"`
	GetContentType   string       `xml:"getcontenttype"`
}

// resourceType captures whether the element contains a <collection/> child.
type resourceType struct {
	IsCollection bool
}

func (rt *resourceType) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "collection" {
				rt.IsCollection = true
			}
			depth := 1
			for depth > 0 {
				tok, err := d.Token()
				if err != nil {
					return err
				}
				switch tok.(type) {
				case xml.StartElement:
					depth++
				case xml.EndElement:
					depth--
				}
			}
		case xml.EndElement:
			return nil
		}
	}
}

// props returns the property name/body pairs for a response.
func (e *propEntry) props() []struct {
	Name string
	Body string
} {
	var out []struct{ Name, Body string }
	p := e.Propstat.Prop
	if p.Resourcetype.IsCollection {
		out = append(out, struct{ Name, Body string }{"resourcetype", "collection"})
	}
	if p.GetContentLength != "" {
		out = append(out, struct{ Name, Body string }{"getcontentlength", p.GetContentLength})
	}
	if p.GetLastModified != "" {
		out = append(out, struct{ Name, Body string }{"getlastmodified", p.GetLastModified})
	}
	if p.GetContentType != "" {
		out = append(out, struct{ Name, Body string }{"getcontenttype", p.GetContentType})
	}
	return out
}

// List performs a single PROPFIND (depth 1) on relDir and returns the page of
// entries starting at offset, capped at limit. When the folder has more
// entries than offset+limit, Next is set so callers can keep paging. Exactly
// one PROPFIND is issued per call: whether a next page exists is decided by
// scanning the same response for one more valid entry past the page.
func (c *Client) List(ctx context.Context, relDir string, offset, limit int) (*ListResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	resp, err := c.do(ctx, "PROPFIND", c.URL(relDir), http.Header{
		"Depth":        {"1"},
		"Content-Type": {"application/xml; charset=utf-8"},
	}, strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
	}

	var pr propResponse
	if err := xml.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding PROPFIND response: %w", err)
	}

	dir := normalizeDir(relDir)
	out := &ListResult{Total: -1}
	skipped := 0
	for i := range pr.Responses {
		r := &pr.Responses[i]
		rel, ok := toRel(decodeHref(r.Href), c.user)
		if !ok {
			continue
		}
		if rel == dir {
			continue // skip the folder itself
		}
		if skipped < offset {
			skipped++
			continue
		}
		if len(out.Entries) >= limit {
			// One more valid entry beyond the page: there is a next page.
			out.Next = strconv.Itoa(offset + limit)
			break
		}
		e := Entry{Path: rel, Name: baseName(rel)}
		for _, p := range r.props() {
			switch p.Name {
			case "resourcetype":
				e.IsDir = strings.Contains(p.Body, "collection")
			case "getcontentlength":
				e.Size, _ = strconv.ParseInt(p.Body, 10, 64)
			case "getlastmodified":
				e.Modified = p.Body
			case "getcontenttype":
				e.ContentType = p.Body
			}
		}
		out.Entries = append(out.Entries, e)
	}

	return out, nil
}

// Read streams a file (or a byte range) without buffering the whole body.
// offset/length: when length <= 0 the entire remainder is streamed. The
// returned reader must be closed by the caller.
func (c *Client) Read(ctx context.Context, relPath string, offset, length int64) (io.ReadCloser, http.Header, error) {
	hdr := http.Header{}
	if offset > 0 || length > 0 {
		if length > 0 {
			hdr["Range"] = []string{fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)}
		} else {
			hdr["Range"] = []string{fmt.Sprintf("bytes=%d-", offset)}
		}
	}
	resp, err := c.do(ctx, http.MethodGet, c.URL(relPath), hdr, nil)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		defer resp.Body.Close()
		return nil, nil, ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
	}
	return resp.Body, resp.Header, nil
}

// Write uploads content to relPath (overwriting). It creates the parent
// folders first: Nextcloud's WebDAV does not create intermediate directories
// on PUT, so a plain PUT to a missing subfolder would fail with 409/423.
func (c *Client) Write(ctx context.Context, relPath string, body io.Reader, size int64) error {
	if err := c.ensureParents(ctx, relPath); err != nil {
		return err
	}
	hdr := http.Header{}
	if size >= 0 {
		hdr.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	resp, err := c.do(ctx, http.MethodPut, c.URL(relPath), hdr, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
	}
	return nil
}

// ensureParents creates every ancestor folder of relPath (idempotently). The
// path must be a sanitized file path; the jail root itself is never touched.
func (c *Client) ensureParents(ctx context.Context, relPath string) error {
	parts := strings.Split(strings.Trim(relPath, "/"), "/")
	if len(parts) < 2 {
		return nil // no parent beyond the root
	}
	cur := ""
	for _, part := range parts[:len(parts)-1] {
		cur += "/" + part
		if err := c.MakeDir(ctx, cur); err != nil {
			if ncerr.Is(err, ncerr.CodeConflict) {
				continue // already exists: fine
			}
			return err
		}
	}
	return nil
}

// MakeDir creates a single folder (MKCOL). It returns CodeConflict when the
// folder already exists. Nextcloud answers an MKCOL on an existing folder with
// 405 MethodNotAllowed (Sabre's "MethodNotAllowed" exception), which is
// semantically a conflict, so it is mapped to CodeConflict as well.
func (c *Client) MakeDir(ctx context.Context, relPath string) error {
	resp, err := c.do(ctx, "MKCOL", c.URL(relPath), http.Header{}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusMethodNotAllowed:
		return ncerr.New(ncerr.CodeConflict, "folder already exists. %s", statusText(resp, 512))
	default:
		return ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
	}
}

// Move renames/moves a path. overwrite controls the Overwrite header. With
// overwrite=false Nextcloud answers 412 PreconditionFailed when the
// destination already exists; that is semantically a conflict, so it is mapped
// to CodeConflict (FromStatus treats 409 and 412 alike).
func (c *Client) Move(ctx context.Context, from, to string, overwrite bool) error {
	hdr := http.Header{"Destination": {c.URL(to)}}
	if overwrite {
		hdr.Set("Overwrite", "T")
	} else {
		hdr.Set("Overwrite", "F")
	}
	resp, err := c.do(ctx, "MOVE", c.URL(from), hdr, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
}

// Delete removes a file or folder. Note: Nextcloud deletes folders
// recursively, so deleting a non-empty folder removes its entire subtree.
func (c *Client) Delete(ctx context.Context, relPath string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.URL(relPath), http.Header{}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
}

// Stat returns metadata for a single path via PROPFIND depth 0.
func (c *Client) Stat(ctx context.Context, relPath string) (*Entry, error) {
	resp, err := c.do(ctx, "PROPFIND", c.URL(relPath), http.Header{
		"Depth":        {"0"},
		"Content-Type": {"application/xml; charset=utf-8"},
	}, strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
	}
	var pr propResponse
	if err := xml.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding PROPFIND response: %w", err)
	}
	for i := range pr.Responses {
		r := &pr.Responses[i]
		rel, ok := toRel(decodeHref(r.Href), c.user)
		if !ok {
			continue
		}
		e := Entry{Path: rel, Name: baseName(rel)}
		for _, p := range r.props() {
			switch p.Name {
			case "resourcetype":
				e.IsDir = strings.Contains(p.Body, "collection")
			case "getcontentlength":
				e.Size, _ = strconv.ParseInt(p.Body, 10, 64)
			case "getlastmodified":
				e.Modified = p.Body
			case "getcontenttype":
				e.ContentType = p.Body
			}
		}
		return &e, nil
	}
	return nil, ncerr.New(ncerr.CodeNotFound, "no response for %s", relPath)
}

// normalizeDir returns the canonical form of a directory path (trailing "/").
func normalizeDir(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	return "/" + strings.Trim(p, "/")
}

// toRel converts an absolute WebDAV href into a jail-relative path.
func toRel(href, user string) (string, bool) {
	const prefix = "/remote.php/dav/files/"
	i := strings.Index(href, prefix)
	if i < 0 {
		return "", false
	}
	rest := href[i+len(prefix):]
	// strip the username segment
	j := strings.Index(rest, "/")
	if j < 0 {
		return "", false
	}
	if rest[:j] != user {
		return "", false
	}
	rel := "/" + rest[j+1:]
	if rel == "/" {
		return "/", true
	}
	return rel, true
}

// Search finds files/folders whose name contains the (case-insensitive)
// query. It walks folders breadth-first up to maxDepth levels and stops after
// limit matches, so it stays cheap on large trees.
func (c *Client) Search(ctx context.Context, relDir, query string, maxDepth, limit int) (*ListResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := strings.ToLower(query)
	out := &ListResult{}
	type item struct {
		path  string
		depth int
	}
	queue := []item{{relDir, 0}}

	for len(queue) > 0 && len(out.Entries) < limit {
		cur := queue[0]
		queue = queue[1:]

		res, err := c.List(ctx, cur.path, 0, 200)
		if err != nil {
			if ncerr.Is(err, ncerr.CodeNotFound) || ncerr.Is(err, ncerr.CodeForbidden) {
				continue
			}
			return nil, err
		}
		for _, e := range res.Entries {
			if strings.Contains(strings.ToLower(e.Name), q) {
				out.Entries = append(out.Entries, e)
				if len(out.Entries) >= limit {
					break
				}
			}
			if e.IsDir && cur.depth < maxDepth {
				queue = append(queue, item{e.Path, cur.depth + 1})
			}
		}
	}
	return out, nil
}

func baseName(p string) string {
	p = strings.TrimSuffix(p, "/")
	i := strings.LastIndex(p, "/")
	return p[i+1:]
}
