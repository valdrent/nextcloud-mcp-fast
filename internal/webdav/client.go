// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package webdav implements a minimal WebDAV client for Nextcloud, tuned for
// low memory: one shared *http.Client with connection pooling, streaming
// reads via Range requests, and compact PROPFIND responses.
package webdav

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

// maxXMLResponseBytes is the maximum size for PROPFIND and SEARCH responses.
// Exceeding this limit returns an error instead of silently truncating data.
// This is a package variable so tests can override it with a smaller cap.
var maxXMLResponseBytes int64 = 32 << 20 // 32 MiB

// limitedReader wraps an io.ReadCloser and enforces a maximum byte limit.
// Reading beyond the limit returns an error instead of truncating.
type limitedReader struct {
	rc    io.ReadCloser
	limit int64
	read  int64
}

func (lr *limitedReader) Read(p []byte) (int, error) {
	// Allow one byte beyond the limit so a body of exactly limit bytes
	// succeeds; only a byte past the limit is an error.
	if lr.read > lr.limit {
		lr.rc.Close()
		return 0, ncerr.New(ncerr.CodeTooLarge, "folder listing response exceeds %d bytes", lr.limit)
	}
	if int64(len(p)) > lr.limit+1-lr.read {
		p = p[:lr.limit+1-lr.read]
	}
	n, err := lr.rc.Read(p)
	lr.read += int64(n)
	if lr.read > lr.limit {
		lr.rc.Close()
		return 0, ncerr.New(ncerr.CodeTooLarge, "folder listing response exceeds %d bytes", lr.limit)
	}
	return n, err
}

func (lr *limitedReader) Close() error {
	return lr.rc.Close()
}

// Credentials identify one Nextcloud account.
type Credentials struct {
	Host     string // e.g. https://cloud.example.com (no trailing slash)
	Username string
	Password string // App Password
}

// ID is a stable, low-cardinality key for per-account state (breaker, etc.).
func (c *Credentials) ID() string {
	return c.Host + "|" + c.Username
}

// Client performs WebDAV operations against one Nextcloud instance. It is
// safe for concurrent use.
type Client struct {
	base *url.URL
	user string
	pass string
	http *http.Client
}

// NewClient builds a Client sharing the provided http client (for pooling).
func NewClient(creds *Credentials, hc *http.Client) (*Client, error) {
	u, err := url.Parse(creds.Host)
	if err != nil {
		return nil, fmt.Errorf("invalid host %q: %w", creds.Host, err)
	}
	return &Client{base: u, user: creds.Username, pass: creds.Password, http: hc}, nil
}

// URL joins a jail-relative path (starting with "/") to the WebDAV root. Each
// segment is percent-encoded so that names containing '#', '?' or '%' produce
// valid request URLs; the username is escaped as a whole.
func (c *Client) URL(relPath string) string {
	seg := make([]string, 0, 8)
	for _, part := range strings.Split(strings.TrimPrefix(relPath, "/"), "/") {
		if part != "" {
			seg = append(seg, url.PathEscape(part))
		}
	}
	p := strings.TrimSuffix(c.base.Path, "/") + "/remote.php/dav/files/" + url.PathEscape(c.user)
	if len(seg) > 0 {
		p += "/" + strings.Join(seg, "/")
	}
	return c.base.Scheme + "://" + c.base.Host + p
}

// decodeHref percent-decodes a WebDAV href so that names containing spaces or
// other reserved characters come back in their raw form. Invalid encodings are
// returned unchanged rather than as errors: the server may also send raw
// (unescaped) hrefs, which pass through untouched.
func decodeHref(href string) string {
	if !strings.Contains(href, "%") {
		return href
	}
	if d, err := url.PathUnescape(href); err == nil {
		return d
	}
	return href
}

func (c *Client) basicAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.user+":"+c.pass))
}

// AuthCheck verifies that the client's credentials authenticate against the
// server by issuing a PROPFIND on the user's root. It is used at cache-fill
// time so that a wrong password can never be served an authenticated client.
func (c *Client) AuthCheck(ctx context.Context) error {
	resp, err := c.do(ctx, "PROPFIND", c.URL("/"), http.Header{
		"Depth":        {"0"},
		"Content-Type": {"application/xml; charset=utf-8"},
	}, strings.NewReader(propfindBody))
	if err != nil {
		return ncerr.New(ncerr.CodeServerError, "authentication check failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusMultiStatus, http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return ncerr.New(ncerr.CodeUnauthorized, "authentication failed; check the App Password")
	default:
		return ncerr.FromStatus(resp.StatusCode, statusText(resp, 512))
	}
}

// do executes a request and returns the response. Callers must close the body.
// Non-2xx responses are returned as-is so callers can map them to semantic
// errors with detail. For PROPFIND and SEARCH methods, the response body is
// wrapped to enforce a maximum size limit.
func (c *Client) do(ctx context.Context, method, url string, header http.Header, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.basicAuth())
	for k, v := range header {
		req.Header.Set(k, v[0])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || netErrTimeout(err) {
			return nil, ncerr.New(ncerr.CodeTimeout, "request timed out: %v", err)
		}
		return nil, err
	}

	// Wrap the response body for PROPFIND and SEARCH to enforce size limits
	if method == "PROPFIND" || method == "SEARCH" {
		resp.Body = &limitedReader{rc: resp.Body, limit: maxXMLResponseBytes}
	}

	return resp, nil
}

// netErrTimeout reports whether err is a network timeout (as opposed to, e.g.,
// a refused connection or DNS failure).
func netErrTimeout(err error) bool {
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

// statusText extracts a short detail string from an error response body
// (bounded read) for use in semantic errors.
func statusText(resp *http.Response, limit int64) string {
	if resp.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
