// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package config holds server configuration loaded from environment variables.
package config

import (
	"fmt"
	"log"
	"net"
	"net/url"
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

	// MaxReadBytes caps a single read_file call (default 128 KiB).
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
	// LogLevel is the audit-log verbosity: debug, info, warn or error
	// (default info). Writes are logged at info, reads at debug.
	LogLevel string

	// AuthMode selects how HTTP clients authenticate: "static" (shared Bearer
	// token, default), "oidc" (JWT access tokens from an external OIDC
	// provider) or "nextcloud" (Nextcloud as OAuth authorization server).
	AuthMode string
	// PublicURL is the externally reachable https URL of the MCP endpoint. It
	// is the OAuth resource identifier and the expected token audience.
	PublicURL string
	// AccountsFile maps OAuth users (token subject) to Nextcloud credentials.
	AccountsFile string
	// OIDCIssuer is the issuer URL of the OIDC provider (oidc mode).
	OIDCIssuer string

	// Version is set by main from build info; used in logs and /healthz.
	Version string
}

// Auth modes for HTTP transport.
const (
	AuthStatic    = "static"
	AuthOIDC      = "oidc"
	AuthNextcloud = "nextcloud"
)

// OAuth reports whether HTTP clients authenticate with OAuth access tokens.
func (c *Config) OAuth() bool { return c.AuthMode == AuthOIDC || c.AuthMode == AuthNextcloud }

// PerRequestCreds reports whether Nextcloud credentials are chosen per request
// (header pass-through or OAuth user mapping) instead of the configured default.
func (c *Config) PerRequestCreds() bool { return c.AllowPassthrough || c.OAuth() }

// Load reads configuration from environment variables (with NEXTCLOUD_ prefix)
// and validates it. It returns an error describing the first problem found.
func Load() (*Config, error) {
	ep := &envParser{}
	cfg := &Config{
		Mode:                    envStr("NEXTCLOUD_MCP_TRANSPORT", "stdio"),
		HTTPAddr:                envStr("NEXTCLOUD_MCP_HTTP_ADDR", "127.0.0.1:8000"),
		HTTPToken:               os.Getenv("NEXTCLOUD_MCP_HTTP_TOKEN"),
		Host:                    strings.TrimRight(envStr("NEXTCLOUD_HOST", ""), "/"),
		Username:                envStr("NEXTCLOUD_USERNAME", ""),
		Password:                envStr("NEXTCLOUD_PASSWORD", ""),
		Permissions:             strings.ToLower(envStr("NEXTCLOUD_MCP_PERMISSIONS", PermRead)),
		AllowPassthrough:        ep.envBool("NEXTCLOUD_MCP_PASSTHROUGH", false),
		AllowedHosts:            envList("NEXTCLOUD_MCP_ALLOWED_HOSTS"),
		MaxReadBytes:            ep.envInt64("NEXTCLOUD_MCP_MAX_READ_BYTES", 128<<10),
		MaxListEntries:          ep.envInt("NEXTCLOUD_MCP_MAX_LIST_ENTRIES", 50),
		HTTPTimeout:             ep.envDuration("NEXTCLOUD_MCP_HTTP_TIMEOUT", 30*time.Second),
		CircuitBreakerThreshold: ep.envInt("NEXTCLOUD_MCP_CB_THRESHOLD", 10),
		CircuitBreakerWindow:    ep.envDuration("NEXTCLOUD_MCP_CB_WINDOW", time.Minute),
		LogLevel:                strings.ToLower(envStr("NEXTCLOUD_MCP_LOG_LEVEL", "info")),
		AuthMode:                strings.ToLower(envStr("NEXTCLOUD_MCP_AUTH_MODE", AuthStatic)),
		PublicURL:               strings.TrimRight(envStr("NEXTCLOUD_MCP_PUBLIC_URL", ""), "/"),
		AccountsFile:            envStr("NEXTCLOUD_MCP_ACCOUNTS_FILE", ""),
		OIDCIssuer:              strings.TrimRight(envStr("NEXTCLOUD_MCP_OIDC_ISSUER", ""), "/"),
	}

	if ep.err != nil {
		return nil, ep.err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.warnPlaintext()
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

	if c.CircuitBreakerThreshold < 0 {
		return fmt.Errorf("NEXTCLOUD_MCP_CB_THRESHOLD must not be negative (0 disables the breaker), got %d", c.CircuitBreakerThreshold)
	}
	if c.CircuitBreakerThreshold > 0 && c.CircuitBreakerWindow <= 0 {
		return fmt.Errorf("NEXTCLOUD_MCP_CB_WINDOW must be positive when the breaker is enabled, got %s", c.CircuitBreakerWindow)
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("NEXTCLOUD_MCP_LOG_LEVEL must be one of debug, info, warn, error, got %q", c.LogLevel)
	}

	if err := c.validateAuth(); err != nil {
		return err
	}

	// Single-user mode requires credentials unless passthrough or OAuth user mapping is enabled.
	if !c.PerRequestCreds() && (c.Username == "" || c.Password == "") {
		return fmt.Errorf("NEXTCLOUD_USERNAME and NEXTCLOUD_PASSWORD are required (use a Nextcloud App Password), or enable NEXTCLOUD_MCP_PASSTHROUGH=true for multi-account pass-through")
	}

	if c.Mode == "http" && c.AuthMode == AuthStatic && c.HTTPToken == "" {
		return fmt.Errorf("NEXTCLOUD_MCP_HTTP_TOKEN is required when NEXTCLOUD_MCP_TRANSPORT=http (the endpoint must never run unauthenticated)")
	}
	if c.Mode == "http" && c.AuthMode == AuthStatic && len(c.HTTPToken) < MinHTTPTokenLen {
		return fmt.Errorf("NEXTCLOUD_MCP_HTTP_TOKEN must be at least %d characters (generate one with: openssl rand -hex 32)", MinHTTPTokenLen)
	}
	if c.Mode == "stdio" && c.AllowPassthrough {
		return fmt.Errorf("NEXTCLOUD_MCP_PASSTHROUGH requires NEXTCLOUD_MCP_TRANSPORT=http (credentials only travel in HTTP headers)")
	}

	return nil
}

func (c *Config) validateAuth() error {
	if c.AuthMode == "" {
		c.AuthMode = AuthStatic
	}
	switch c.AuthMode {
	case AuthStatic:
		return nil
	case AuthOIDC, AuthNextcloud:
	default:
		return fmt.Errorf("NEXTCLOUD_MCP_AUTH_MODE must be one of static, oidc, nextcloud, got %q", c.AuthMode)
	}
	if c.Mode != "http" {
		return fmt.Errorf("NEXTCLOUD_MCP_AUTH_MODE=%s requires NEXTCLOUD_MCP_TRANSPORT=http", c.AuthMode)
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || u.Fragment != "" || u.RawQuery != "" {
		return fmt.Errorf("NEXTCLOUD_MCP_PUBLIC_URL must be an absolute URL without query or fragment, got %q", c.PublicURL)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return fmt.Errorf("NEXTCLOUD_MCP_PUBLIC_URL must be https (http only on loopback), got %q", c.PublicURL)
	}
	if c.AccountsFile == "" && c.AuthMode == AuthOIDC {
		return fmt.Errorf("NEXTCLOUD_MCP_ACCOUNTS_FILE is required when NEXTCLOUD_MCP_AUTH_MODE=%s", c.AuthMode)
	}
	if c.AuthMode == AuthNextcloud && strings.HasPrefix(c.Host, "http://") && !isLoopback(hostOf(c.Host)) {
		return fmt.Errorf("NEXTCLOUD_HOST must be https when NEXTCLOUD_MCP_AUTH_MODE=nextcloud")
	}
	if c.AuthMode == AuthOIDC {
		iu, err := url.Parse(c.OIDCIssuer)
		if err != nil || iu.Host == "" || (iu.Scheme != "https" && !(iu.Scheme == "http" && isLoopback(iu.Hostname()))) {
			return fmt.Errorf("NEXTCLOUD_MCP_OIDC_ISSUER must be an https URL (http only on loopback), got %q", c.OIDCIssuer)
		}
	}
	return nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func isLoopback(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
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

// warnPlaintext logs a warning when the Host is plain http:// on a
// non-loopback address, since Basic auth would travel in clear text.
func (c *Config) warnPlaintext() {
	u, err := url.Parse(c.Host)
	if err != nil || u.Scheme != "http" {
		return
	}
	h := u.Hostname()
	if h == "localhost" {
		return
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return
	}
	log.Printf("WARNING: NEXTCLOUD_HOST %s uses plain http://; credentials (HTTP Basic auth) travel in clear text. Use https:// outside of loopback.", c.Host)
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envParser reads typed env vars, remembering the first unparsable value.
type envParser struct{ err error }

func (p *envParser) fail(key, v string, err error) {
	if p.err == nil {
		p.err = fmt.Errorf("%s: invalid value %q: %v", key, v, err)
	}
}

func (p *envParser) envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.fail(key, v, err)
		return def
	}
	return b
}

func (p *envParser) envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.fail(key, v, err)
		return def
	}
	return n
}

func (p *envParser) envInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		p.fail(key, v, err)
		return def
	}
	return n
}

func (p *envParser) envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.fail(key, v, err)
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
