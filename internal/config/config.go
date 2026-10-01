// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package config holds server configuration loaded from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Permission levels, in increasing order of capability.
const (
	PermRead        = "read"
	PermWrite       = "write"
	PermDestructive = "destructive"
)

// MinHTTPTokenLen is the minimum accepted length of NEXTCLOUD_MCP_HTTP_TOKEN.
const MinHTTPTokenLen = 32

// Config is the fully-resolved server configuration.
type Config struct {
	// Mode selects the transport: "stdio" or "http".
	Mode string
	// HTTP listen address, only used when Mode == "http". Defaults to loopback;
	// binding a public interface requires an explicit NEXTCLOUD_MCP_HTTP_ADDR.
	HTTPAddr string
	// HTTPToken is the required Bearer token for HTTP mode. It is mandatory:
	// http mode without a token refuses to start, so the endpoint can never be
	// left unauthenticated.
	HTTPToken string

	// Host is the Nextcloud base URL (e.g. https://cloud.example.com).
	Host string
	// Username is the default account for single-user mode. May be empty in
	// pass-through multi-account mode.
	Username string
	// Password is the default App Password. May be empty in pass-through mode.
	Password string

	// Permissions is one of read, write, destructive (or "full" as an alias
	// for destructive). It bounds what tools may do regardless of per-request
	// overrides.
	Permissions string

	// AllowPassthrough enables multi-account pass-through: clients may supply
	// their own credentials per request via headers/arguments. Only meaningful
	// in http mode.
	AllowPassthrough bool
	// AllowedHosts is the comma-separated allowlist of Nextcloud hosts
	// (scheme://host[:port], without trailing slash) that passthrough requests
	// may target. Empty means only the configured Host is reachable. Plaintext
	// http:// hosts are always rejected, regardless of this list.
	AllowedHosts []string

	// MaxReadBytes caps a single read_file call (default 1 MiB).
	MaxReadBytes int64
	// MaxListEntries caps entries returned per list_files page (default 50, max 200).
	MaxListEntries int
	// HTTPTimeout bounds individual WebDAV requests (default 30s).
	HTTPTimeout time.Duration
	// CircuitBreakerThreshold is the number of failed calls within the breaker
	// window before the account is locked out. Zero disables the breaker.
	CircuitBreakerThreshold int
	// CircuitBreakerWindow is the sliding window for the breaker.
	CircuitBreakerWindow time.Duration

	// Version is set by main from build info; used in logs and /healthz.
	Version string
}

// Load reads configuration from environment variables (with NEXTCLOUD_ prefix)
// and validates it. It returns an error describing the first problem found.
func Load() (*Config, error) {
	cfg := &Config{
		Mode:                    envStr("NEXTCLOUD_MCP_TRANSPORT", "stdio"),
		HTTPAddr:                envStr("NEXTCLOUD_MCP_HTTP_ADDR", "127.0.0.1:8000"),
		HTTPToken:               os.Getenv("NEXTCLOUD_MCP_HTTP_TOKEN"),
		Host:                    strings.TrimRight(envStr("NEXTCLOUD_HOST", ""), "/"),
		Username:                envStr("NEXTCLOUD_USERNAME", ""),
		Password:                envStr("NEXTCLOUD_PASSWORD", ""),
		Permissions:             strings.ToLower(envStr("NEXTCLOUD_MCP_PERMISSIONS", PermRead)),
		AllowPassthrough:        envBool("NEXTCLOUD_MCP_PASSTHROUGH", false),
		AllowedHosts:            envList("NEXTCLOUD_MCP_ALLOWED_HOSTS"),
		MaxReadBytes:            envInt64("NEXTCLOUD_MCP_MAX_READ_BYTES", 1<<20),
		MaxListEntries:          envInt("NEXTCLOUD_MCP_MAX_LIST_ENTRIES", 50),
		HTTPTimeout:             envDuration("NEXTCLOUD_MCP_HTTP_TIMEOUT", 30*time.Second),
		CircuitBreakerThreshold: envInt("NEXTCLOUD_MCP_CB_THRESHOLD", 10),
		CircuitBreakerWindow:    envDuration("NEXTCLOUD_MCP_CB_WINDOW", time.Minute),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Mode {
	case "stdio", "http":
	default:
		return fmt.Errorf("NEXTCLOUD_MCP_TRANSPORT must be 'stdio' or 'http', got %q", c.Mode)
	}

	if !strings.HasPrefix(c.Host, "http://") && !strings.HasPrefix(c.Host, "https://") {
		// Allow bare hostnames for convenience; we normalize to https.
		c.Host = "https://" + c.Host
	}

	switch c.Permissions {
	case PermRead, PermWrite, PermDestructive, "full":
		if c.Permissions == "full" {
			c.Permissions = PermDestructive
		}
	default:
		return fmt.Errorf("NEXTCLOUD_MCP_PERMISSIONS must be one of read, write, destructive (or full), got %q", c.Permissions)
	}

	if c.MaxListEntries < 1 || c.MaxListEntries > 200 {
		return fmt.Errorf("NEXTCLOUD_MCP_MAX_LIST_ENTRIES must be between 1 and 200, got %d", c.MaxListEntries)
	}
	if c.MaxReadBytes < 1024 {
		return fmt.Errorf("NEXTCLOUD_MCP_MAX_READ_BYTES must be at least 1024, got %d", c.MaxReadBytes)
	}

	// Single-user mode requires credentials unless passthrough is enabled.
	if !c.AllowPassthrough && (c.Username == "" || c.Password == "") {
		return fmt.Errorf("NEXTCLOUD_USERNAME and NEXTCLOUD_PASSWORD are required (use a Nextcloud App Password), or enable NEXTCLOUD_MCP_PASSTHROUGH=true for multi-account pass-through")
	}

	if c.Mode == "http" && c.HTTPToken == "" {
		return fmt.Errorf("NEXTCLOUD_MCP_HTTP_TOKEN is required when NEXTCLOUD_MCP_TRANSPORT=http (the endpoint must never run unauthenticated)")
	}
	if c.Mode == "http" && len(c.HTTPToken) < MinHTTPTokenLen {
		return fmt.Errorf("NEXTCLOUD_MCP_HTTP_TOKEN must be at least %d characters (generate one with: openssl rand -hex 32)", MinHTTPTokenLen)
	}
	if c.Mode == "stdio" && c.AllowPassthrough {
		return fmt.Errorf("NEXTCLOUD_MCP_PASSTHROUGH requires NEXTCLOUD_MCP_TRANSPORT=http (credentials only travel in HTTP headers)")
	}

	return nil
}

// HostAllowed reports whether host (already normalized to scheme://host[:port])
// is reachable in passthrough mode. The configured default host is always
// allowed; everything else must appear in AllowedHosts. Plaintext http://
// hosts are never allowed unless the server's own configured Host is plaintext
// (e.g. a local test or LAN deployment), in which case any plaintext host on
// the allowlist is also permitted.
func (c *Config) HostAllowed(host string) bool {
	if host == c.Host {
		return true
	}
	for _, h := range c.AllowedHosts {
		if h == host {
			if strings.HasPrefix(h, "http://") && !strings.HasPrefix(c.Host, "http://") {
				continue // plaintext allowlist entry only when the default host is plaintext too
			}
			return true
		}
	}
	return false
}

// HasPermission reports whether the configured permission level includes the
// requested level.
func (c *Config) HasPermission(level string) bool {
	order := map[string]int{PermRead: 0, PermWrite: 1, PermDestructive: 2}
	if order[c.Permissions] >= order[level] {
		return true
	}
	return false
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// envList reads a comma-separated list of values, trimming whitespace and
// dropping empty entries.
func envList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
