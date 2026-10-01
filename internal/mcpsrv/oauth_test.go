// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package mcpsrv

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	"github.com/valdrent/nextcloud-mcp-fast/internal/perm"
)

func oauthReq(scopes ...string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "u", Scopes: scopes}}}
}

func TestOAuthScopeCapsPermission(t *testing.T) {
	cfg := &config.Config{AuthMode: config.AuthOIDC, Permissions: config.PermDestructive}
	g, _ := perm.New(cfg.Permissions)
	s := &Server{cfg: cfg, guard: g}

	if err := s.allow(oauthReq(), perm.Read); err != nil {
		t.Fatalf("read without scopes must work: %v", err)
	}
	if err := s.allow(oauthReq(), perm.Write); err == nil {
		t.Fatal("write without mcp:write must be denied")
	}
	if err := s.allow(oauthReq("mcp:write"), perm.Write); err != nil {
		t.Fatalf("mcp:write should allow write: %v", err)
	}
	if err := s.allow(oauthReq("mcp:write"), perm.Destructive); err == nil {
		t.Fatal("destructive with only mcp:write must be denied")
	}
	if err := s.allow(oauthReq("mcp:destructive"), perm.Destructive); err != nil {
		t.Fatalf("mcp:destructive should allow: %v", err)
	}
	// Scopes never raise the server-configured ceiling.
	cfg.Permissions = config.PermRead
	s.guard, _ = perm.New(cfg.Permissions)
	if err := s.allow(oauthReq("mcp:destructive"), perm.Write); err == nil {
		t.Fatal("scope must not exceed configured permissions")
	}
}

func TestOAuthUnmappedUserDenied(t *testing.T) {
	cfg := &config.Config{AuthMode: config.AuthOIDC, Host: "https://c"}
	s := &Server{cfg: cfg}
	if h, u, p := s.credsFor(oauthReq(), nil); h != "" || u != "" || p != "" {
		t.Fatal("unmapped user must get empty credentials")
	}
	if _, err := s.resolveClient(nil, oauthReq(), nil); err == nil {
		t.Fatal("expected forbidden for unmapped user")
	}
}

func TestNextcloudModeUsesTokenAsAppPassword(t *testing.T) {
	cfg := &config.Config{AuthMode: config.AuthNextcloud, Host: "https://cloud.example.com", Permissions: config.PermWrite}
	g, _ := perm.New(cfg.Permissions)
	s := &Server{cfg: cfg, guard: g}
	req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "alice", Extra: map[string]any{"token": "tok"}}}}
	if h, u, p := s.credsFor(req, nil); h != cfg.Host || u != "alice" || p != "tok" {
		t.Fatalf("got %q %q %q", h, u, p)
	}
	if err := s.allow(req, perm.Write); err != nil {
		t.Fatalf("scope cap must not apply in nextcloud mode: %v", err)
	}
	if err := s.allow(req, perm.Destructive); err == nil {
		t.Fatal("configured ceiling must still apply")
	}
}
