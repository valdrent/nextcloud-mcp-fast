// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Command nextcloud-mcp-fast is a lightweight, multi-account MCP server that
// exposes Nextcloud files (WebDAV) to LLM clients. It runs over stdio (local)
// or streamable-HTTP (remote) and is tuned for low memory usage.
package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/valdrent/nextcloud-mcp-fast/internal/accounts"
	"github.com/valdrent/nextcloud-mcp-fast/internal/config"
	"github.com/valdrent/nextcloud-mcp-fast/internal/mcpsrv"
	"github.com/valdrent/nextcloud-mcp-fast/internal/oauth"
)

// version is overridden at build time with -ldflags "-X main.version=v1.0.0".
var version = "dev"

func main() {
	health := flag.Bool("healthcheck", false, "run a self-check and exit 0 on success (used by Docker HEALTHCHECK)")
	flag.Parse()

	if *health {
		os.Exit(runHealthcheck())
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Version = version

	reg := accounts.NewRegistry(cfg)
	srv, err := mcpsrv.New(cfg, reg)
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	if cfg.OAuth() && cfg.AccountsFile != "" {
		users, err := oauth.LoadUserMap(cfg.AccountsFile)
		if err != nil {
			log.Fatalf("accounts: %v", err)
		}
		srv.SetUserMap(users)
	}
	mcpServer := srv.BuildMCP()

	switch cfg.Mode {
	case "stdio":
		runStdio(mcpServer)
	case "http":
		runHTTP(mcpServer, cfg)
	default:
		log.Fatalf("unknown transport %q", cfg.Mode)
	}
}

func runStdio(s *mcp.Server) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Printf("stdio server stopped: %v", err)
	}
}

// buildHTTPHandler constructs the HTTP handler stack for the MCP server.
// It is separated from runHTTP for testability.
func buildHTTPHandler(s *mcp.Server, token string) http.Handler {
	return buildMux(s, requireBearer(token, mcpHandler(s)), nil)
}

// buildOAuthHandler is like buildHTTPHandler but authenticates with OAuth
// access tokens and serves the RFC 9728 protected-resource metadata.
func buildOAuthHandler(s *mcp.Server, cfg *config.Config, verify auth.TokenVerifier, authServers []string) http.Handler {
	mux := http.NewServeMux()
	protect := oauth.Protect(mux, cfg.PublicURL, authServers, verify)
	if cfg.AuthMode == config.AuthNextcloud {
		mux.Handle("/.well-known/oauth-authorization-server", oauth.AuthServerMetadata(authServers[0], cfg.Host))
	}
	return buildMux(s, protect(mcpHandler(s)), mux)
}

func mcpHandler(s *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
}

func buildMux(_ *mcp.Server, protected http.Handler, mux *http.ServeMux) http.Handler {
	if mux == nil {
		mux = http.NewServeMux()
	}
	mux.Handle("/", protected)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","version":"` + version + `"}`))
	})
	return mux
}

// buildHTTPServer constructs the http.Server with proper hardening timeouts
// and limits. It is separated from runHTTP for testability.
func buildHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

func runHTTP(s *mcp.Server, cfg *config.Config) {
	var handler http.Handler
	switch cfg.AuthMode {
	case config.AuthOIDC:
		v := oauth.NewOIDCVerifier(cfg.OIDCIssuer, cfg.PublicURL, nil)
		handler = buildOAuthHandler(s, cfg, v.Verify, []string{cfg.OIDCIssuer})
	case config.AuthNextcloud:
		v := oauth.NewNextcloudVerifier(cfg.Host, nil)
		issuer := oauth.Origin(cfg.PublicURL)
		handler = buildOAuthHandler(s, cfg, v.Verify, []string{issuer})
	default:
		handler = buildHTTPHandler(s, cfg.HTTPToken)
	}
	httpServer := buildHTTPServer(cfg.HTTPAddr, handler)

	go func() {
		log.Printf("nextcloud-mcp-fast %s listening on %s (streamable-http)", version, cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// requireBearer wraps an MCP handler with mandatory Bearer-token auth. The
// token is compared in constant time; missing or wrong tokens get 401 with the
// WWW-Authenticate hint, so clients can discover the scheme without leaking
// which token was expected.
func requireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nextcloud-mcp"`)
			http.Error(w, "unauthorized: missing Bearer token", http.StatusUnauthorized)
			return
		}
		got := auth[len(prefix):]
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nextcloud-mcp", error="invalid_token"`)
			http.Error(w, "unauthorized: invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// runHealthcheck performs a lightweight self-check: configuration must load.
// It returns 0 on success, 1 otherwise. No network I/O is performed so the
// check stays cheap and deterministic inside a memory-limited container.
func runHealthcheck() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck failed:", err)
		return 1
	}
	fmt.Printf("ok version=%s mode=%s permissions=%s\n", version, cfg.Mode, cfg.Permissions)
	return 0
}
