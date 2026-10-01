// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	ncCacheTTL  = time.Minute
	ncCacheSize = 1024
)

// NextcloudVerifier validates opaque access tokens issued by Nextcloud's
// oauth2 app by asking the configured Nextcloud who the token belongs to.
// The host is fixed, so a token for any other server can never be accepted.
// Results are cached briefly; revocation takes effect within ncCacheTTL.
type NextcloudVerifier struct {
	host  string
	http  *http.Client
	cache *expirable.LRU[string, *auth.TokenInfo]
}

// NewNextcloudVerifier returns a verifier bound to a single Nextcloud host.
func NewNextcloudVerifier(host string, client *http.Client) *NextcloudVerifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &NextcloudVerifier{
		host:  strings.TrimRight(host, "/"),
		http:  client,
		cache: expirable.NewLRU[string, *auth.TokenInfo](ncCacheSize, nil, ncCacheTTL),
	}
}

// Verify implements auth.TokenVerifier. The returned TokenInfo carries the
// raw token in Extra["token"] so it can be used as the WebDAV App Password.
func (v *NextcloudVerifier) Verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	if ti, ok := v.cache.Get(key); ok {
		return ti, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.host+"/ocs/v2.php/cloud/user?format=json", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	resp, err := v.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nextcloud token check: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusFound:
		return nil, fmt.Errorf("%w: rejected by Nextcloud", auth.ErrInvalidToken)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("nextcloud token check: status %d", resp.StatusCode)
	}
	var body struct {
		OCS struct {
			Data struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBody)).Decode(&body); err != nil || body.OCS.Data.ID == "" {
		return nil, fmt.Errorf("%w: no user in Nextcloud response", auth.ErrInvalidToken)
	}
	ti := &auth.TokenInfo{
		UserID:     body.OCS.Data.ID,
		Expiration: time.Now().Add(ncCacheTTL),
		// Nextcloud's oauth2 has no scopes; NEXTCLOUD_MCP_PERMISSIONS is the ceiling.
		Extra: map[string]any{"token": token, "preferred_username": strings.ToLower(body.OCS.Data.ID), "email": strings.ToLower(body.OCS.Data.Email)},
	}
	v.cache.Add(key, ti)
	return ti, nil
}

// AuthServerMetadata serves RFC 8414 metadata for Nextcloud's oauth2 app, which
// publishes none itself. issuer is the origin of the public MCP URL; the
// endpoints point straight at Nextcloud. There is no registration_endpoint:
// the client must be created in Nextcloud and its ID/secret entered in Claude.
func AuthServerMetadata(issuer, ncHost string) http.Handler {
	ncHost = strings.TrimRight(ncHost, "/")
	doc, _ := json.Marshal(map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                ncHost + "/index.php/apps/oauth2/authorize",
		"token_endpoint":                        ncHost + "/index.php/apps/oauth2/api/v1/token",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic"},
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(doc)
	})
}

// Origin returns scheme://host of a URL.
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
