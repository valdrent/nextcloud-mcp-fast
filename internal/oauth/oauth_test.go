// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const aud = "https://mcp.example.com/mcp"

type idp struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": p.srv.URL, "jwks_uri": p.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": "AQAB",
		}}})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *idp) token(t *testing.T, alg, kid string, claims map[string]any, key *rsa.PrivateKey) string {
	t.Helper()
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	signing := enc(map[string]string{"alg": alg, "kid": kid}) + "." + enc(claims)
	sig := []byte("x")
	if alg == "RS256" {
		d := sha256.Sum256([]byte(signing))
		var err error
		if sig, err = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:]); err != nil {
			t.Fatal(err)
		}
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (p *idp) claims(over map[string]any) map[string]any {
	c := map[string]any{"iss": p.srv.URL, "sub": "u1", "aud": aud, "exp": time.Now().Add(time.Hour).Unix(), "scope": "mcp:write"}
	for k, v := range over {
		c[k] = v
	}
	return c
}

func TestOIDCVerify(t *testing.T) {
	p := newIDP(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := NewOIDCVerifier(p.srv.URL, aud, nil)

	ti, err := v.Verify(context.Background(), p.token(t, "RS256", "k1", p.claims(nil), p.key), nil)
	if err != nil || ti.UserID != "u1" || len(ti.Scopes) != 1 || ti.Scopes[0] != "mcp:write" {
		t.Fatalf("valid token rejected: %v %+v", err, ti)
	}
	if _, err := v.Verify(context.Background(), p.token(t, "RS256", "k1", p.claims(map[string]any{"aud": []string{"x", aud}}), p.key), nil); err != nil {
		t.Fatalf("aud array rejected: %v", err)
	}

	bad := map[string]string{
		"wrong aud":     p.token(t, "RS256", "k1", p.claims(map[string]any{"aud": "https://evil"}), p.key),
		"wrong iss":     p.token(t, "RS256", "k1", p.claims(map[string]any{"iss": "https://evil"}), p.key),
		"expired":       p.token(t, "RS256", "k1", p.claims(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}), p.key),
		"no exp":        p.token(t, "RS256", "k1", p.claims(map[string]any{"exp": 0}), p.key),
		"not yet valid": p.token(t, "RS256", "k1", p.claims(map[string]any{"nbf": time.Now().Add(time.Hour).Unix()}), p.key),
		"no sub":        p.token(t, "RS256", "k1", p.claims(map[string]any{"sub": ""}), p.key),
		"bad signature": p.token(t, "RS256", "k1", p.claims(nil), other),
		"alg none":      p.token(t, "none", "k1", p.claims(nil), p.key),
		"alg HS256":     p.token(t, "HS256", "k1", p.claims(nil), p.key),
		"unknown kid":   p.token(t, "RS256", "zzz", p.claims(nil), p.key),
		"not a jwt":     "abc",
	}
	for name, tok := range bad {
		if _, err := v.Verify(context.Background(), tok, nil); err == nil {
			t.Errorf("%s: token accepted", name)
		}
	}
}

func TestOIDCIssuerMismatch(t *testing.T) {
	p := newIDP(t)
	v := NewOIDCVerifier(p.srv.URL+"/other", aud, nil)
	if _, err := v.Verify(context.Background(), p.token(t, "RS256", "k1", p.claims(nil), p.key), nil); err == nil {
		t.Fatal("expected discovery failure")
	}
}

func TestUserMap(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.json")
	body := `{"u1":{"username":"alice","app_password":"pw"},"bob@x.org":{"host":"https://c","username":"bob","app_password":"pw2"}}`
	os.WriteFile(f, []byte(body), 0o644)
	if _, err := LoadUserMap(f); err == nil {
		t.Fatal("world-readable file must be rejected")
	}
	os.Chmod(f, 0o600)
	m, err := LoadUserMap(f)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := m.Lookup(&auth.TokenInfo{UserID: "u1"}); !ok || a.Username != "alice" {
		t.Fatal("sub lookup failed")
	}
	if a, ok := m.Lookup(&auth.TokenInfo{UserID: "zz", Extra: map[string]any{"email": "bob@x.org"}}); !ok || a.Username != "bob" {
		t.Fatal("email lookup failed")
	}
	if _, ok := m.Lookup(&auth.TokenInfo{UserID: "nobody"}); ok {
		t.Fatal("unmapped user must be denied")
	}
	if _, ok := m.Lookup(nil); ok {
		t.Fatal("nil token must be denied")
	}
	os.WriteFile(f, []byte(`{"u":{"username":"a"}}`), 0o600)
	if _, err := LoadUserMap(f); err == nil {
		t.Fatal("entry without app_password must be rejected")
	}
}

func TestProtect(t *testing.T) {
	mux := http.NewServeMux()
	verify := func(_ context.Context, tok string, _ *http.Request) (*auth.TokenInfo, error) {
		if tok != "good" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: "u", Expiration: time.Now().Add(time.Hour)}, nil
	}
	protect := Protect(mux, aud, []string{"https://idp"}, verify)
	mux.Handle("/", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })))

	do := func(path, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	w := do("/mcp", "")
	wa := w.Header().Get("WWW-Authenticate")
	if w.Code != 401 || !strings.Contains(wa, `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("got %d %q", w.Code, wa)
	}
	if do("/mcp", "bad").Code != 401 {
		t.Fatal("bad token must be 401")
	}
	if do("/mcp", "good").Code != 204 {
		t.Fatal("good token must pass")
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := do(p, "")
		var m map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil || m["resource"] != aud {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body)
		}
	}
}
