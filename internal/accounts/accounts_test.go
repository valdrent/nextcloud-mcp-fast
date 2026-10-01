// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package accounts

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

func cfgWith(passthrough bool) *config.Config {
	return &config.Config{
		Mode:             "http",
		Host:             "https://cloud.example.com",
		Username:         "default-user",
		Password:         "default-pass",
		AllowPassthrough: passthrough,
		HTTPTimeout:      30e9, // 30s in ns; NewRegistry only reads it for the client
	}
}

func TestResolveSingleUserIgnoresOverrides(t *testing.T) {
	r := NewRegistry(cfgWith(false))
	ctx := context.Background()

	c1, err := r.Resolve(ctx, "https://other.example.com", "intruder", "x")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Must use the default account, not the supplied overrides.
	if c1.URL("/f.txt") != "https://cloud.example.com/remote.php/dav/files/default-user/f.txt" {
		t.Errorf("single-user mode leaked per-request credentials: %s", c1.URL("/f.txt"))
	}
}

// mockDAV serves PROPFIND with 401 unless the Basic auth matches user:pass.
func mockDAV(user, pass string) *httptest.Server {
	want := "Basic " + basicAuthValue(user, pass)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" || r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		fmt.Fprint(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/remote.php/dav/files/`+user+`/</D:href>
    <D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:propstat>
  </D:response>
</D:multistatus>`)
		w.WriteHeader(http.StatusMultiStatus)
	}))
}

func basicAuthValue(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func TestResolvePassthroughIsolatesAccounts(t *testing.T) {
	aSrv := mockDAV("alice", "pa")
	defer aSrv.Close()
	bSrv := mockDAV("bob", "pb")
	defer bSrv.Close()

	cfg := cfgWith(true)
	cfg.Host = aSrv.URL // plaintext default host permits plaintext allowlist entries
	cfg.AllowedHosts = []string{aSrv.URL, bSrv.URL}
	r := NewRegistry(cfg)
	ctx := context.Background()

	a, err := r.Resolve(ctx, aSrv.URL, "alice", "pa")
	if err != nil {
		t.Fatalf("Resolve alice: %v", err)
	}
	b, err := r.Resolve(ctx, bSrv.URL, "bob", "pb")
	if err != nil {
		t.Fatalf("Resolve bob: %v", err)
	}
	if a == b {
		t.Errorf("distinct accounts resolved to the same client")
	}

	// Caching: same account returns the identical pointer.
	a2, err := r.Resolve(ctx, aSrv.URL, "alice", "pa")
	if err != nil {
		t.Fatalf("Resolve alice again: %v", err)
	}
	if a != a2 {
		t.Errorf("expected cached client for the same account ID")
	}
}

func TestResolvePassthroughWrongPasswordRejected(t *testing.T) {
	srv := mockDAV("alice", "correct")
	defer srv.Close()

	cfg := cfgWith(true)
	cfg.Host = srv.URL // plaintext default host permits plaintext allowlist entries
	cfg.AllowedHosts = []string{srv.URL}
	r := NewRegistry(cfg)
	ctx := context.Background()

	if _, err := r.Resolve(ctx, srv.URL, "alice", "wrong"); err == nil {
		t.Fatalf("expected error for wrong password, got a client")
	}
	// A subsequent correct resolve must succeed and be cached.
	c1, err := r.Resolve(ctx, srv.URL, "alice", "correct")
	if err != nil {
		t.Fatalf("Resolve with correct password: %v", err)
	}
	c2, err := r.Resolve(ctx, srv.URL, "alice", "correct")
	if err != nil {
		t.Fatalf("Resolve cached: %v", err)
	}
	if c1 != c2 {
		t.Errorf("expected the same cached client for identical credentials")
	}
	// The wrong password must not be served the authenticated client.
	if _, err := r.Resolve(ctx, srv.URL, "alice", "wrong"); err == nil {
		t.Errorf("wrong password was served a client after a correct login")
	}
}

func TestResolveMissingCreds(t *testing.T) {
	r := NewRegistry(cfgWith(true))
	if _, err := r.Resolve(context.Background(), "", "", ""); err == nil {
		t.Errorf("expected error when credentials are missing in passthrough mode")
	}
}

// countingDAV accepts any credentials unless wantPass is set, counting requests.
func countingDAV(wantPass string, delay map[string]time.Duration, n *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		u, p, _ := r.BasicAuth()
		if d := delay[u]; d > 0 {
			time.Sleep(d)
		}
		if wantPass != "" && p != wantPass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"></D:multistatus>`)
	}))
}

func passthroughCfg(host string) *config.Config {
	cfg := cfgWith(true)
	cfg.Host = host
	cfg.AllowedHosts = []string{host}
	return cfg
}

func TestSlowAuthDoesNotBlockCachedResolve(t *testing.T) {
	var n atomic.Int64
	srv := countingDAV("", map[string]time.Duration{"slow": 2 * time.Second}, &n)
	defer srv.Close()
	r := NewRegistry(passthroughCfg(srv.URL))
	ctx := context.Background()
	if _, err := r.Resolve(ctx, srv.URL, "fast", "pw"); err != nil {
		t.Fatal(err)
	}
	go r.Resolve(ctx, srv.URL, "slow", "pw")
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if _, err := r.Resolve(ctx, srv.URL, "fast", "pw"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("cached Resolve blocked for %s", d)
	}
}

func TestSingleflightOneAuthCheck(t *testing.T) {
	var n atomic.Int64
	srv := countingDAV("", map[string]time.Duration{"alice": 200 * time.Millisecond}, &n)
	defer srv.Close()
	r := NewRegistry(passthroughCfg(srv.URL))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Resolve(context.Background(), srv.URL, "alice", "pw"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := n.Load(); got != 1 {
		t.Errorf("AuthCheck requests = %d, want 1", got)
	}
}

func TestCacheBounded(t *testing.T) {
	var n atomic.Int64
	srv := countingDAV("", nil, &n)
	defer srv.Close()
	r := NewRegistry(passthroughCfg(srv.URL))
	for i := 0; i < authCacheSize+100; i++ {
		if _, err := r.Resolve(context.Background(), srv.URL, fmt.Sprintf("u%d", i), "pw"); err != nil {
			t.Fatal(err)
		}
	}
	if l := r.clients.Len(); l > authCacheSize {
		t.Errorf("cache Len = %d, want <= %d", l, authCacheSize)
	}
}

func TestNegativeCache(t *testing.T) {
	var n atomic.Int64
	srv := countingDAV("right", nil, &n)
	defer srv.Close()
	r := NewRegistry(passthroughCfg(srv.URL))
	for i := 0; i < 20; i++ {
		if _, err := r.Resolve(context.Background(), srv.URL, "alice", "wrong"); !ncerr.Is(err, ncerr.CodeUnauthorized) {
			t.Fatalf("resolve %d: err = %v, want unauthorized", i, err)
		}
	}
	if got := n.Load(); got != 1 {
		t.Errorf("server requests = %d, want 1", got)
	}
}
