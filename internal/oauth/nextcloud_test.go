// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func TestNextcloudVerifier(t *testing.T) {
	var calls atomic.Int32
	nc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.Header.Get("Authorization") {
		case "Bearer good":
			w.Write([]byte(`{"ocs":{"data":{"id":"Alice","email":"A@x.org"}}}`))
		case "Bearer down":
			w.WriteHeader(500)
		default:
			w.WriteHeader(401)
		}
	}))
	defer nc.Close()
	v := NewNextcloudVerifier(nc.URL, nil)

	ti, err := v.Verify(context.Background(), "good", nil)
	if err != nil || ti.UserID != "Alice" || ti.Extra["token"] != "good" || ti.Extra["email"] != "a@x.org" {
		t.Fatalf("got %+v %v", ti, err)
	}
	v.Verify(context.Background(), "good", nil)
	if calls.Load() != 1 {
		t.Fatalf("expected cached second call, got %d requests", calls.Load())
	}
	if _, err := v.Verify(context.Background(), "bad", nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("bad token: %v", err)
	}
	if _, err := v.Verify(context.Background(), "down", nil); err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("upstream failure must not be reported as invalid token: %v", err)
	}
}

func TestAuthServerMetadata(t *testing.T) {
	w := httptest.NewRecorder()
	AuthServerMetadata(testMCPOrigin, "https://cloud.example.com/").ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["issuer"] != testMCPOrigin ||
		m["authorization_endpoint"] != "https://cloud.example.com/index.php/apps/oauth2/authorize" ||
		m["token_endpoint"] != "https://cloud.example.com/index.php/apps/oauth2/api/v1/token" {
		t.Fatalf("unexpected metadata: %v", m)
	}
	if _, ok := m["registration_endpoint"]; ok {
		t.Fatal("must not advertise DCR")
	}
	if Origin("https://mcp.example.com/mcp") != testMCPOrigin {
		t.Fatal("origin")
	}
}

const testMCPOrigin = "https://mcp.example.com"
