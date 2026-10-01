// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package oauth implements the OAuth 2.0 resource-server side of the MCP
// server: protected-resource metadata, access-token verification and the
// mapping from authenticated users to Nextcloud accounts.
package oauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	jwksTTL        = time.Hour
	jwksMinRefresh = time.Minute
	maxJSONBody    = 1 << 20
	clockSkew      = 30 * time.Second
)

// OIDCVerifier validates JWT access tokens issued by an OIDC provider. Only
// RS256 and ES256 are accepted; "none" and HMAC algorithms are rejected so a
// token can never be forged with a public key.
type OIDCVerifier struct {
	issuer   string
	audience string
	http     *http.Client

	mu        sync.Mutex
	jwksURI   string
	keys      map[string]crypto.PublicKey
	fetchedAt time.Time
	lastTry   time.Time
}

// NewOIDCVerifier returns a verifier for tokens from issuer whose audience
// claim must contain audience.
func NewOIDCVerifier(issuer, audience string, client *http.Client) *OIDCVerifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &OIDCVerifier{issuer: strings.TrimRight(issuer, "/"), audience: audience, http: client}
}

// Verify implements auth.TokenVerifier.
func (v *OIDCVerifier) Verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: not a JWT", auth.ErrInvalidToken)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return nil, fmt.Errorf("%w: bad header", auth.ErrInvalidToken)
	}
	if hdr.Alg != "RS256" && hdr.Alg != "ES256" {
		return nil, fmt.Errorf("%w: unsupported alg", auth.ErrInvalidToken)
	}
	key, err := v.key(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: bad signature encoding", auth.ErrInvalidToken)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := verifySig(hdr.Alg, key, digest[:], sig); err != nil {
		return nil, fmt.Errorf("%w: signature mismatch", auth.ErrInvalidToken)
	}

	c, err := v.parseClaims(parts[1])
	if err != nil {
		return nil, err
	}
	exp := time.Unix(int64(c.Exp), 0)
	scopes := strings.Fields(c.Scope)
	if len(c.Scp) > 0 {
		var arr []string
		if json.Unmarshal(c.Scp, &arr) == nil {
			scopes = append(scopes, arr...)
		}
	}
	return &auth.TokenInfo{
		UserID:     c.Sub,
		Scopes:     scopes,
		Expiration: exp,
		Extra:      map[string]any{"email": c.Email, "preferred_username": c.Pref},
	}, nil
}

type jwtClaims struct {
	Iss   string          `json:"iss"`
	Sub   string          `json:"sub"`
	Aud   json.RawMessage `json:"aud"`
	Exp   float64         `json:"exp"`
	Nbf   float64         `json:"nbf"`
	Scope string          `json:"scope"`
	Scp   json.RawMessage `json:"scp"`
	Email string          `json:"email"`
	Pref  string          `json:"preferred_username"`
}

// parseClaims decodes the payload segment and validates issuer, audience,
// time bounds and subject.
func (v *OIDCVerifier) parseClaims(seg string) (*jwtClaims, error) {
	var c jwtClaims
	if err := decodeSegment(seg, &c); err != nil {
		return nil, fmt.Errorf("%w: bad claims", auth.ErrInvalidToken)
	}
	if c.Iss != v.issuer {
		return nil, fmt.Errorf("%w: wrong issuer", auth.ErrInvalidToken)
	}
	if !audContains(c.Aud, v.audience) {
		return nil, fmt.Errorf("%w: wrong audience", auth.ErrInvalidToken)
	}
	if c.Exp == 0 {
		return nil, fmt.Errorf("%w: missing exp", auth.ErrInvalidToken)
	}
	now := time.Now()
	if now.After(time.Unix(int64(c.Exp), 0).Add(clockSkew)) {
		return nil, fmt.Errorf("%w: token expired", auth.ErrInvalidToken)
	}
	if c.Nbf != 0 && now.Add(clockSkew).Before(time.Unix(int64(c.Nbf), 0)) {
		return nil, fmt.Errorf("%w: token not yet valid", auth.ErrInvalidToken)
	}
	if c.Sub == "" {
		return nil, fmt.Errorf("%w: missing sub", auth.ErrInvalidToken)
	}
	return &c, nil
}

func decodeSegment(seg string, out any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func audContains(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == want {
				return true
			}
		}
	}
	return false
}

func verifySig(alg string, key crypto.PublicKey, digest, sig []byte) error {
	switch alg {
	case "RS256":
		pk, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("key type mismatch")
		}
		return rsa.VerifyPKCS1v15(pk, crypto.SHA256, digest, sig)
	case "ES256":
		pk, ok := key.(*ecdsa.PublicKey)
		if !ok || len(sig) != 64 {
			return errors.New("key type mismatch")
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(pk, digest, r, s) {
			return errors.New("bad signature")
		}
		return nil
	default:
		return errors.New("unsupported alg")
	}
}

// key returns the signing key for kid, refreshing the JWKS when the cache is
// stale or the kid is unknown (rate-limited to one refresh per minute so a
// stream of bogus kids cannot be used to hammer the identity provider).
func (v *OIDCVerifier) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.lookup(kid); ok && time.Since(v.fetchedAt) < jwksTTL {
		return k, nil
	}
	if time.Since(v.lastTry) >= jwksMinRefresh || v.keys == nil {
		v.lastTry = time.Now()
		if err := v.refresh(ctx); err != nil && v.keys == nil {
			return nil, fmt.Errorf("oidc: %w", err)
		}
	}
	if k, ok := v.lookup(kid); ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown signing key", auth.ErrInvalidToken)
}

func (v *OIDCVerifier) lookup(kid string) (crypto.PublicKey, bool) {
	if k, ok := v.keys[kid]; ok {
		return k, true
	}
	if kid == "" && len(v.keys) == 1 {
		for _, k := range v.keys {
			return k, true
		}
	}
	return nil, false
}

func (v *OIDCVerifier) refresh(ctx context.Context) error {
	if v.jwksURI == "" {
		var meta struct {
			Issuer  string `json:"issuer"`
			JWKSURI string `json:"jwks_uri"`
		}
		if err := v.getJSON(ctx, v.issuer+"/.well-known/openid-configuration", &meta); err != nil {
			return err
		}
		if strings.TrimRight(meta.Issuer, "/") != v.issuer || meta.JWKSURI == "" {
			return errors.New("discovery document does not match configured issuer")
		}
		v.jwksURI = meta.JWKSURI
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := v.getJSON(ctx, v.jwksURI, &set); err != nil {
		return err
	}
	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if pub := parseJWK(k.Kty, k.N, k.E, k.Crv, k.X, k.Y); pub != nil {
			keys[k.Kid] = pub
		}
	}
	if len(keys) == 0 {
		return errors.New("jwks contains no usable signing keys")
	}
	v.keys, v.fetchedAt = keys, time.Now()
	return nil
}

// parseJWK builds a public key from JWK fields, or returns nil when the key is
// of an unsupported type or fails the minimum-strength checks.
func parseJWK(kty, n, e, crv, x, y string) crypto.PublicKey {
	switch kty {
	case "RSA":
		return parseRSAJWK(n, e)
	case "EC":
		return parseECJWK(crv, x, y)
	default:
		return nil
	}
}

func parseRSAJWK(n, e string) crypto.PublicKey {
	bn, be := b64Int(n), b64Int(e)
	if bn == nil || be == nil || !be.IsInt64() || bn.BitLen() < 2048 {
		return nil
	}
	return &rsa.PublicKey{N: bn, E: int(be.Int64())}
}

func parseECJWK(crv, x, y string) crypto.PublicKey {
	bx, by := b64Int(x), b64Int(y)
	if crv != "P-256" || bx == nil || by == nil {
		return nil
	}
	if bx.BitLen() > 256 || by.BitLen() > 256 {
		return nil
	}
	point := make([]byte, 65)
	point[0] = 0x04
	bx.FillBytes(point[1:33])
	by.FillBytes(point[33:])
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		return nil
	}
	return pub
}

func b64Int(s string) *big.Int {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil
	}
	return new(big.Int).SetBytes(b)
}

func (v *OIDCVerifier) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxJSONBody)).Decode(out)
}
