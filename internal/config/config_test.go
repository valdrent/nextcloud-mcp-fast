// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
	"time"
)

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

func TestHasPermission(t *testing.T) {
	cases := []struct {
		level string
		op    string
		want  bool
	}{
		{"read", "read", true},
		{"read", "write", false},
		{"write", "read", true},
		{"write", "write", true},
		{"write", "destructive", false},
		{"destructive", "destructive", true},
	}
	for _, tc := range cases {
		t.Run(tc.level+"->"+tc.op, func(t *testing.T) {
			c := &Config{Permissions: tc.level}
			if got := c.HasPermission(tc.op); got != tc.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tc.op, got, tc.want)
			}
		})
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
		return &Config{Mode: "http", Host: "https://c.example.com", Username: "u", Password: "p",
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
