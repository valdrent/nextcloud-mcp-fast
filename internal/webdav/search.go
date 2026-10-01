// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

const (
	// searchTimeout bounds a whole Search call (SEARCH or BFS fallback).
	searchTimeout = 60 * time.Second
	// maxSearchDirs bounds the directories the BFS fallback may visit.
	maxSearchDirs = 500
)

// Search finds files/folders whose name contains the (case-insensitive)
// query within maxDepth levels below relDir, returning at most limit matches.
// It uses Nextcloud's server-side WebDAV SEARCH and falls back to a bounded
// breadth-first PROPFIND walk when the server does not support it.
func (c *Client) Search(ctx context.Context, relDir, query string, maxDepth, limit int) (*ListResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	res, ok, err := c.searchDAV(ctx, relDir, query, maxDepth, limit)
	if err != nil {
		return nil, err
	}
	if ok {
		return res, nil
	}
	return c.searchBFS(ctx, relDir, query, maxDepth, limit)
}

// searchBody renders the DASL basicsearch request. query is XML-escaped.
func (c *Client) searchBody(relDir, query string, limit int) string {
	var q bytes.Buffer
	xml.EscapeText(&q, []byte(query))
	scope := "/files/" + url.PathEscape(c.user)
	for _, part := range strings.Split(normalizeDir(relDir), "/") {
		if part != "" {
			scope += "/" + url.PathEscape(part)
		}
	}
	if normalizeDir(relDir) == "/" {
		scope += "/"
	}
	var s bytes.Buffer
	xml.EscapeText(&s, []byte(scope))
	return `<?xml version="1.0" encoding="UTF-8"?>
<d:searchrequest xmlns:d="DAV:">
  <d:basicsearch>
    <d:select><d:prop>
      <d:displayname/><d:resourcetype/><d:getcontentlength/><d:getlastmodified/><d:getcontenttype/>
    </d:prop></d:select>
    <d:from><d:scope><d:href>` + s.String() + `</d:href><d:depth>infinity</d:depth></d:scope></d:from>
    <d:where><d:like><d:prop><d:displayname/></d:prop><d:literal>%` + q.String() + `%</d:literal></d:like></d:where>
    <d:limit><d:nresults>` + strconv.Itoa(limit) + `</d:nresults></d:limit>
  </d:basicsearch>
</d:searchrequest>`
}

// searchDAV runs the WebDAV SEARCH. ok=false means the server does not
// support it (or answered unparseably) and the caller should fall back.
func (c *Client) searchDAV(ctx context.Context, relDir, query string, maxDepth, limit int) (*ListResult, bool, error) {
	endpoint := c.base.Scheme + "://" + c.base.Host + strings.TrimSuffix(c.base.Path, "/") + "/remote.php/dav/"
	resp, err := c.do(ctx, "SEARCH", endpoint, http.Header{
		"Content-Type": {"text/xml; charset=utf-8"},
	}, strings.NewReader(c.searchBody(relDir, query, min(limit*4, 1000))))
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusMultiStatus, http.StatusOK:
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUnsupportedMediaType, http.StatusNotImplemented:
		return nil, false, nil
	default:
		return nil, false, ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
	}
	var pr propResponse
	if err := xml.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, false, nil
	}
	q := strings.ToLower(query)
	dir := normalizeDir(relDir)
	base := strings.TrimSuffix(dir, "/")
	out := &ListResult{}
	for i := range pr.Responses {
		r := &pr.Responses[i]
		rel, ok := toRel(decodeHref(r.Href), c.user)
		if !ok {
			continue
		}
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" || rel == base || !strings.HasPrefix(rel, base+"/") {
			continue
		}
		if strings.Count(strings.TrimPrefix(rel, base+"/"), "/")+1 > maxDepth {
			continue
		}
		en := r.entry(rel)
		// LIKE wildcards (% _) in the query widen server-side matches;
		// always re-check with a plain case-insensitive substring match.
		if !strings.Contains(strings.ToLower(en.Name), q) {
			continue
		}
		out.Entries = append(out.Entries, en)
		if len(out.Entries) >= limit {
			break
		}
	}
	return out, true, nil
}

// entry builds an Entry for a response at the given relative path.
func (e *propEntry) entry(rel string) Entry {
	en := Entry{Path: rel, Name: baseName(rel)}
	for _, p := range e.props() {
		switch p.Name {
		case "resourcetype":
			en.IsDir = strings.Contains(p.Body, "collection")
		case "getcontentlength":
			en.Size, _ = strconv.ParseInt(p.Body, 10, 64)
		case "getlastmodified":
			en.Modified = p.Body
		case "getcontenttype":
			en.ContentType = p.Body
		default:
			// Unknown properties are ignored.
		}
	}
	return en
}

// bfsItem is a directory queued for the breadth-first fallback search.
type bfsItem struct {
	path  string
	depth int
}

// searchBFS is the PROPFIND fallback: breadth-first, at most maxSearchDirs
// directory listings, paging through folders larger than one List page.
func (c *Client) searchBFS(ctx context.Context, relDir, query string, maxDepth, limit int) (*ListResult, error) {
	q := strings.ToLower(query)
	out := &ListResult{}
	queue := []bfsItem{{relDir, 0}}
	visited := 0

	for len(queue) > 0 && len(out.Entries) < limit && visited < maxSearchDirs {
		cur := queue[0]
		queue = queue[1:]
		visited++

		var err error
		queue, err = c.scanDir(ctx, cur, queue, q, maxDepth, limit, out)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scanDir pages through one directory, appending matches to out and
// subdirectories (within maxDepth) to queue. Missing or forbidden folders are
// skipped silently.
func (c *Client) scanDir(ctx context.Context, cur bfsItem, queue []bfsItem, q string, maxDepth, limit int, out *ListResult) ([]bfsItem, error) {
	offset := 0
	for len(out.Entries) < limit {
		res, err := c.List(ctx, cur.path, offset, 200)
		if err != nil {
			if ncerr.Is(err, ncerr.CodeNotFound) || ncerr.Is(err, ncerr.CodeForbidden) {
				return queue, nil
			}
			return queue, err
		}
		queue = collectMatches(res.Entries, cur, queue, q, maxDepth, limit, out)
		if res.Next == "" {
			break
		}
		offset += len(res.Entries)
	}
	return queue, nil
}

// collectMatches appends name matches among entries to out (up to limit) and
// queues subdirectories that are still within maxDepth.
func collectMatches(entries []Entry, cur bfsItem, queue []bfsItem, q string, maxDepth, limit int, out *ListResult) []bfsItem {
	for _, e := range entries {
		if len(out.Entries) < limit && strings.Contains(strings.ToLower(e.Name), q) {
			out.Entries = append(out.Entries, e)
		}
		if e.IsDir && cur.depth < maxDepth-1 {
			queue = append(queue, bfsItem{e.Path, cur.depth + 1})
		}
	}
	return queue
}
