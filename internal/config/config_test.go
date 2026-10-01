// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

const testHost = "https://c.example.com"

// withEnv sets env vars for the duration of a test (auto-cleanup).
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadDefaults(t *testing.T) {
	withEnv(t, map[string]string{
		"NEXTCLOUD_HOST":     "https://cloud.example.com",
		"NEXTCLOUD_USERNAME": "alice",
		"NEXTCLOUD_PASSWORD": "pw",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode != "stdio" {
		t.Errorf("Mode = %q, want stdio", cfg.Mode)
	}
	if cfg.Permissions != PermRead {
		t.Errorf("Permissions = %q, want read", cfg.Permissions)
	}
	if cfg.MaxListEntries != 50 {
		t.Errorf("MaxListEntries = %d, want 50", cfg.MaxListEntries)
	}
	if cfg.MaxReadBytes != 128<<10 {
		t.Errorf("MaxReadBytes = %d, want 131072", cfg.MaxReadBytes)
	}
	if cfg.HTTPTimeout != 30*time.Second {
		t.Errorf("HTTPTimeout = %v, want 30s", cfg.HTTPTimeout)
	}
}

func TestLoadHostNormalization(t *testing.T) {
	withEnv(t, map[string]string{
		"NEXTCLOUD_HOST":     "cloud.example.com/",
		"NEXTCLOUD_USERNAME": "alice",
		"NEXTCLOUD_PASSWORD": "pw",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "https://cloud.example.com" {
		t.Errorf("Host = %q, want https://cloud.example.com", cfg.Host)
	}
}

func TestLoadFullAlias(t *testing.T) {
	withEnv(t, map[string]string{
		"NEXTCLOUD_HOST":            "https://cloud.example.com",
		"NEXTCLOUD_USERNAME":        "alice",
		"NEXTCLOUD_PASSWORD":        "pw",
		"NEXTCLOUD_MCP_PERMISSIONS": "full",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Permissions != PermDestructive {
		t.Errorf("Permissions = %q, want destructive", cfg.Permissions)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"bad transport", map[string]string{
			"NEXTCLOUD_HOST": "https://x", "NEXTCLOUD_USERNAME": "a", "NEXTCLOUD_PASSWORD": "b",
			"NEXTCLOUD_MCP_TRANSPORT": "carrier-pigeon",
		}},
		{"bad permission", map[string]string{
			"NEXTCLOUD_HOST": "https://x", "NEXTCLOUD_USERNAME": "a", "NEXTCLOUD_PASSWORD": "b",
			"NEXTCLOUD_MCP_PERMISSIONS": "sudo",
		}},
		{"list entries too high", map[string]string{
			"NEXTCLOUD_HOST": "https://x", "NEXTCLOUD_USERNAME": "a", "NEXTCLOUD_PASSWORD": "b",
			"NEXTCLOUD_MCP_MAX_LIST_ENTRIES": "9999",
		}},
		{"read bytes too low", map[string]string{
			"NEXTCLOUD_HOST": "https://x", "NEXTCLOUD_USERNAME": "a", "NEXTCLOUD_PASSWORD": "b",
			"NEXTCLOUD_MCP_MAX_READ_BYTES": "10",
		}},
		{"missing password", map[string]string{
			"NEXTCLOUD_HOST": "https://x", "NEXTCLOUD_USERNAME": "a",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, tc.env)
			if _, err := Load(); err == nil {
				t.Fatalf("Load: expected error for %s", tc.name)
			}
		})
	}
}

func TestLoadPassthroughRelaxesCreds(t *testing.T) {
	withEnv(t, map[string]string{
		"NEXTCLOUD_HOST":            "https://cloud.example.com",
		"NEXTCLOUD_MCP_PASSTHROUGH": "true",
		"NEXTCLOUD_MCP_TRANSPORT":   "http",
		"NEXTCLOUD_MCP_HTTP_TOKEN":  strings.Repeat("t", 32),
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AllowPassthrough {
		t.Errorf("AllowPassthrough = false, want true")
	}
}

func TestHostAllowed(t *testing.T) {
	c := &Config{Host: "https://cloud.example.com", AllowedHosts: []string{"https://other.example.com", "http://plain.example.com"}}
	cases := []struct {
		host string
		want bool
	}{
		{"https://cloud.example.com", true},
		{"https://other.example.com", true},
		{"https://evil.tld", false},
		{"http://plain.example.com", false}, // plaintext entry, https default
		{"https://cloud.example.com@evil.tld", false},
		{"https://CLOUD.example.com", false},
		{"https://cloud.example.com:443", false},
	}
	for _, tc := range cases {
		if got := c.HostAllowed(tc.host); got != tc.want {
			t.Errorf("HostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestValidateTokenAndPassthrough(t *testing.T) {
	base := func() *Config {
		return &Config{Mode: "http", Host: testHost, Username: "u", Password: "p",
			Permissions: PermRead, MaxListEntries: 50, MaxReadBytes: 1 << 20, HTTPToken: strings.Repeat("x", 32)}
	}
	cases := []struct {
		name    string
		mut     func(*Config)
		wantErr bool
	}{
		{"ok", func(*Config) {}, false},
		{"short token", func(c *Config) { c.HTTPToken = strings.Repeat("x", 31) }, true},
		{"empty token", func(c *Config) { c.HTTPToken = "" }, true},
		{"passthrough stdio", func(c *Config) { c.Mode = "stdio"; c.AllowPassthrough = true }, true},
		{"passthrough http", func(c *Config) { c.AllowPassthrough = true }, false},
	}
	for _, tc := range cases {
		c := base()
		tc.mut(c)
		if err := c.validate(); (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestValidateBreakerAndLogLevel(t *testing.T) {
	base := func() *Config {
		return &Config{Mode: "stdio", Host: testHost, Username: "u", Password: "p",
			Permissions: PermRead, MaxListEntries: 50, MaxReadBytes: 1 << 20,
			CircuitBreakerThreshold: 5, CircuitBreakerWindow: time.Minute, LogLevel: "info"}
	}
	cases := []struct {
		name    string
		mut     func(*Config)
		wantErr bool
	}{
		{"ok", func(*Config) {}, false},
		{"disabled", func(c *Config) { c.CircuitBreakerThreshold = 0; c.CircuitBreakerWindow = 0 }, false},
		{"negative threshold", func(c *Config) { c.CircuitBreakerThreshold = -1 }, true},
		{"zero window enabled", func(c *Config) { c.CircuitBreakerWindow = 0 }, true},
		{"debug", func(c *Config) { c.LogLevel = "debug" }, false},
		{"bad level", func(c *Config) { c.LogLevel = "loud" }, true},
	}
	for _, tc := range cases {
		c := base()
		tc.mut(c)
		if err := c.validate(); (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestLoadParseErrors(t *testing.T) {
	for _, tc := range []struct{ key, val string }{
		{"NEXTCLOUD_MCP_CB_THRESHOLD", "abc"},
		{"NEXTCLOUD_MCP_PASSTHROUGH", "yes"},
		{"NEXTCLOUD_MCP_MAX_READ_BYTES", "1MB"},
		{"NEXTCLOUD_MCP_HTTP_TIMEOUT", "soon"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			withEnv(t, map[string]string{
				"NEXTCLOUD_HOST": testHost, "NEXTCLOUD_USERNAME": "a", "NEXTCLOUD_PASSWORD": "p",
				tc.key: tc.val,
			})
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), tc.val) {
				t.Errorf("err = %v, want mention of %s and %q", err, tc.key, tc.val)
			}
		})
	}
}

func TestLoadPlaintextWarning(t *testing.T) {
	for host, wantWarn := range map[string]bool{
		"http://cloud.example.com":  true,
		"http://localhost:8080":     false,
		"http://127.0.0.2":          false,
		"http://[::1]:8080":         false,
		"https://cloud.example.com": false,
	} {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		withEnv(t, map[string]string{"NEXTCLOUD_HOST": host, "NEXTCLOUD_USERNAME": "a", "NEXTCLOUD_PASSWORD": "p"})
		if _, err := Load(); err != nil {
			t.Fatal(err)
		}
		log.SetOutput(os.Stderr)
		if got := strings.Contains(buf.String(), "plain http://"); got != wantWarn {
			t.Errorf("%s: warned=%v, want %v (%q)", host, got, wantWarn, buf.String())
		}
	}
}

func TestValidateAuthModes(t *testing.T) {
	base := func() *Config {
		return &Config{Mode: "http", Host: "https://c", Permissions: PermRead, MaxListEntries: 50, MaxReadBytes: 4096,
			AuthMode: AuthOIDC, PublicURL: "https://mcp.example.com/mcp", AccountsFile: "/x", OIDCIssuer: "https://idp.example.com"}
	}
	cases := map[string]struct {
		mut     func(*Config)
		wantErr bool
	}{
		"ok oidc, no static token needed": {func(*Config) {}, false},
		"unknown mode":                    {func(c *Config) { c.AuthMode = "x" }, true},
		"stdio rejected":                  {func(c *Config) { c.Mode = "stdio" }, true},
		"http public url":                 {func(c *Config) { c.PublicURL = "http://mcp.example.com" }, true},
		"loopback http ok":                {func(c *Config) { c.PublicURL = "http://localhost:8000" }, false},
		"missing accounts file":           {func(c *Config) { c.AccountsFile = "" }, true},
		"missing issuer":                  {func(c *Config) { c.OIDCIssuer = "" }, true},
		"plain http issuer":               {func(c *Config) { c.OIDCIssuer = "http://idp.example.com" }, true},
	}
	for name, tc := range cases {
		c := base()
		tc.mut(c)
		if err := c.validate(); (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", name, err, tc.wantErr)
		}
	}
}
