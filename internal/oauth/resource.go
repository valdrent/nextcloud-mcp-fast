// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package oauth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// Scopes understood by the server; they only ever lower the configured
// permission level, never raise it.
var Scopes = []string{"mcp:read", "mcp:write", "mcp:destructive"}

// Protect mounts the RFC 9728 metadata endpoints on mux and returns a
// middleware that requires a valid access token. publicURL is the resource
// identifier (and expected audience); authServers lists the issuers clients
// should use.
func Protect(mux *http.ServeMux, publicURL string, authServers []string, verify auth.TokenVerifier) func(http.Handler) http.Handler {
	meta := &oauthex.ProtectedResourceMetadata{
		Resource:               publicURL,
		AuthorizationServers:   authServers,
		ScopesSupported:        Scopes,
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Nextcloud MCP",
	}
	h := auth.ProtectedResourceMetadataHandler(meta)
	path := "/.well-known/oauth-protected-resource"
	mux.Handle(path, h)
	if u, err := url.Parse(publicURL); err == nil && strings.Trim(u.Path, "/") != "" {
		path += "/" + strings.Trim(u.Path, "/")
		mux.Handle(path, h)
	}
	u, _ := url.Parse(publicURL)
	metaURL := u.Scheme + "://" + u.Host + path
	return auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{ResourceMetadataURL: metaURL})
}
