// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package accounts manages per-account WebDAV clients. All accounts share a
// single *http.Client (one connection pool) to keep memory and sockets low,
// while each account gets its own webdav.Client for credential isolation.
package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
	"github.com/valdrent/nextcloud-mcp-fast/internal/webdav"
)

// Registry resolves credentials to clients, caching them per account. The
// cache key includes a hash of the password: a wrong password must never be
// served an already-authenticated client for the same host|user.
type Registry struct {
	cfg  *config.Config
	http *http.Client

	// clients holds verified clients keyed by host|user|passHash. It is
	// bounded (authCacheSize) and entries expire after authOKTTL, so memory
	// stays flat no matter how many distinct credentials are presented.
	clients *expirable.LRU[string, *webdav.Client]
	// failed is a short-lived negative cache of genuine auth failures, so a
	// wrong password is not re-checked against Nextcloud on every call.
	failed *expirable.LRU[string, error]

	// inFlight coalesces concurrent first-time resolutions of the same
	// credential set into a single network check (singleflight).
	inFlightMu sync.Mutex
	inFlight   map[string]*call
}

type call struct {
	wg     sync.WaitGroup
	err    error
	client *webdav.Client
}

const (
	authCacheSize = 1024
	authOKTTL     = 5 * time.Minute
	authFailTTL   = 30 * time.Second
)

// passHash returns a truncated SHA-256 of the password for use in cache keys.
func passHash(pass string) string {
	sum := sha256.Sum256([]byte(pass))
	return hex.EncodeToString(sum[:8])
}

// NewRegistry builds a Registry with a shared, pooled http client.
func NewRegistry(cfg *config.Config) *Registry {
	// Clone DefaultTransport to preserve proxy settings from env and TLS/dial timeouts
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second

	return &Registry{
		cfg: cfg,
		http: &http.Client{
			Timeout:   cfg.HTTPTimeout,
			Transport: transport,
			// Disable automatic redirect following to prevent SSRF via 3xx responses
			// to internal addresses from a compromised Nextcloud server
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		clients:  expirable.NewLRU[string, *webdav.Client](authCacheSize, nil, authOKTTL),
		failed:   expirable.NewLRU[string, error](authCacheSize, nil, authFailTTL),
		inFlight: make(map[string]*call),
	}
}

// Resolve returns the client for the given credentials, creating and caching
// it on first use. In single-user mode (no passthrough) the default account is
// always used regardless of input. A cached client is only reused when it
// authenticates with the supplied password; a wrong password therefore never
// receives another caller's authenticated client.
func (r *Registry) Resolve(ctx context.Context, host, user, pass string) (*webdav.Client, error) {
	cfg := r.cfg

	// Single-user mode: ignore per-request credentials.
	if !cfg.PerRequestCreds() {
		host, user, pass = cfg.Host, cfg.Username, cfg.Password
	}

	if host == "" || user == "" || pass == "" {
		return nil, fmt.Errorf("missing credentials: set NEXTCLOUD_HOST/USERNAME/PASSWORD or enable passthrough and supply them per request")
	}
	host = trimHost(host)

	if cfg.PerRequestCreds() && !cfg.HostAllowed(host) {
		return nil, ncerr.New(ncerr.CodeForbidden, "host %s is not in the allowed list (NEXTCLOUD_MCP_ALLOWED_HOSTS)", host)
	}

	id := (&webdav.Credentials{Host: host, Username: user}).ID() + "|" + passHash(pass)

	if c, ok := r.clients.Get(id); ok {
		return c, nil
	}
	if err, ok := r.failed.Get(id); ok {
		return nil, err
	}

	return r.resolveOnce(ctx, id, host, user, pass)
}

// resolveOnce creates the client and verifies credentials exactly once per
// concurrent set of callers (singleflight). The network AuthCheck runs outside
// any registry-wide lock.
func (r *Registry) resolveOnce(ctx context.Context, id, host, user, pass string) (*webdav.Client, error) {
	r.inFlightMu.Lock()
	c, started := r.newCall(id)
	if !started {
		r.inFlightMu.Unlock()
		c.wg.Wait()
		return c.client, c.err
	}
	r.inFlightMu.Unlock()

	defer func() {
		r.inFlightMu.Lock()
		delete(r.inFlight, id)
		r.inFlightMu.Unlock()
	}()

	client, err := webdav.NewClient(&webdav.Credentials{Host: host, Username: user, Password: pass}, r.http)
	if err != nil {
		c.err = err
		c.wg.Done()
		return nil, err
	}

	if r.cfg.PerRequestCreds() {
		if err := client.AuthCheck(ctx); err != nil {
			if ncerr.Is(err, ncerr.CodeUnauthorized) {
				r.failed.Add(id, err)
			}
			c.err = err
			c.wg.Done()
			return nil, err
		}
	}

	r.clients.Add(id, client)

	c.client = client
	c.wg.Done()
	return client, nil
}

func (r *Registry) newCall(id string) (*call, bool) {
	if c, ok := r.inFlight[id]; ok {
		return c, false
	}
	c := &call{}
	c.wg.Add(1)
	r.inFlight[id] = c
	return c, true
}

func trimHost(h string) string {
	for len(h) > 0 && h[len(h)-1] == '/' {
		h = h[:len(h)-1]
	}
	if h != "" && !hasScheme(h) {
		h = "https://" + h
	}
	return h
}

func hasScheme(h string) bool {
	for i := 0; i+3 <= len(h); i++ {
		if h[i] == ':' && h[i+1] == '/' && h[i+2] == '/' {
			return true
		}
	}
	return false
}
