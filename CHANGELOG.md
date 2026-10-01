# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-10-01

### Added

- **OAuth 2.0 resource-server mode** (`NEXTCLOUD_MCP_AUTH_MODE=oidc`) so Claude
  Cowork / claude.ai custom connectors can authenticate: RFC 9728
  protected-resource metadata, local JWT verification (RS256/ES256, issuer,
  audience, expiry), per-user Nextcloud App Password mapping and optional
  `mcp:read|write|destructive` scopes that can only lower the permission level.
  The default `static` Bearer mode is unchanged. See `docs/oauth-oidc.md`.
- **`NEXTCLOUD_MCP_AUTH_MODE=nextcloud`** (experimental): Nextcloud's `oauth2` app
  as authorization server (RFC 8414 metadata facade, token validation via OCS,
  token used as WebDAV credential). See `docs/oauth-nextcloud.md`.

### Changed (BREAKING)

- **HTTP mode:** `NEXTCLOUD_MCP_HTTP_TOKEN` (min 32 chars) now required, sent
  as `Authorization: Bearer <token>`.
- **HTTP mode:** default bind address is now `127.0.0.1:8000`; the Docker
  image sets `NEXTCLOUD_MCP_HTTP_ADDR=:8000`.
- **Credentials:** no longer accepted as tool arguments; passthrough reads them
  only from `X-Nextcloud-*` headers and requires http mode.
- **write_file:** no longer silently overwrites existing files; `overwrite`
  argument (default false) must be explicitly set to true; overwrite operations
  require `destructive` permission.

### Changed

- **Default `NEXTCLOUD_MCP_MAX_READ_BYTES` lowered from 1 MiB to 128 KiB (131072)** to save LLM context.
- **read_file with binary content**: without `encoding=base64` now returns `unsupported_type` error with a hint instead of base64 content; `encoding=base64` still returns base64.
- **read_file output**: never splits a UTF-8 character at the truncation point; returns `next_offset` when truncated; text results carry `"trust":"untrusted"`.
- **Startup validation**: invalid values in numeric/boolean/duration env vars (e.g. `NEXTCLOUD_MCP_CB_THRESHOLD=abc`, `NEXTCLOUD_MCP_PASSTHROUGH=yes`) now fail startup instead of silently using the default.
- Startup warning when `NEXTCLOUD_HOST` uses plain `http://` on a non-loopback host.
- `search_files`: SQL wildcard characters (% and _) in the query are matched literally; usernames/paths with special characters work in SEARCH; results are no longer cut short by depth filtering.
- Fixed: a PROPFIND/SEARCH response of exactly the 32 MiB cap no longer fails.
- Circuit breaker now only counts server-side / unhealthy failures (5xx, timeouts, 429, 401, network errors), not 4xx client errors (404, 409, 403, 400);
  `NEXTCLOUD_MCP_CB_THRESHOLD=0` now truly disables the breaker.
- `read_file` now returns `server_error` if the server ignores a Range request
  instead of silently returning wrong data.
- `search_files` now uses Nextcloud server-side WebDAV SEARCH for efficient
  indexed queries (one request), with automatic fallback to bounded directory
  walk (max 500 folders, 60s budget) on servers without SEARCH support.
- Outbound HTTP client now clones DefaultTransport to preserve proxy settings
  from environment and TLS/dial timeouts; MaxIdleConnsPerHost increased from 8
  to 32.
- HTTP server: stateless mode (no sticky sessions, scales horizontally behind any load balancer; GET/DELETE return 405).

### Security

- **read_file output is now marked untrusted**: text results carry `"trust":"untrusted"` to signal to the LLM that file contents/names are untrusted data and must not be followed as instructions (prompt-injection defense in depth).
- **Server instructions added**: tell the model that file contents, names, and search results are untrusted data (prompt-injection defense).
- Outbound redirect following is disabled; any 3xx response from Nextcloud is
  returned as an error, preventing SSRF attacks via compromised servers.
- PROPFIND and SEARCH responses are capped at 32 MiB to prevent unbounded memory
  consumption from extremely large folder listings.
- HTTP server has explicit timeouts on all paths (header read, request read,
  response write, idle) and enforces a 64 KiB header size limit; enables safe
  horizontal scaling without sticky sessions (GET and DELETE return 405 to prevent
  accidental state corruption).
- Passthrough hosts must be on `NEXTCLOUD_MCP_ALLOWED_HOSTS` (the configured
  host is always allowed).
- Credential verification is coalesced (singleflight) and runs outside any
  registry-wide lock.
- Client and negative auth caches are bounded and expire; auth failures now
  count toward the circuit breaker; breaker key map is capped.
- **CI supply-chain hardening**: GitHub Actions pinned by commit SHA (with tag comments), base images pinned by digest, multi-arch Docker images (linux/amd64, linux/arm64), SBOM and provenance attestations, keyless cosign signing, static analysis (staticcheck) and fuzzing in CI.

### Added

- `NEXTCLOUD_MCP_LOG_LEVEL` environment variable (debug/info/warn/error, default
  info): JSON audit log on stderr of every tool call (tool, account host|user,
  paths, outcome, duration; never passwords). Write/destructive calls logged at
  info, reads at debug.
- `overwrite` argument for `write_file` and `move_file` tools to explicitly
  control whether existing destinations are replaced; operations with overwrite
  require `destructive` permission.
- `write_file` and `move_file` now advertise `destructiveHint=true` in schema
  hints.

### Fixed

- `list_files` pagination no longer repeats pages and uses one PROPFIND.
- `read_file` offset handling; negative offset/length are rejected.
- Circuit breaker double-counted failures after window pruning.
- Path sanitizer accepted percent-encoded invalid UTF-8 or NUL (e.g. `%80`,
  `%00`); the decoded path is now validated too (found by fuzzing).
- Build toolchain pinned to Go 1.26.8 (`toolchain` in go.mod) so CI and local
  builds include the standard-library security fixes reported by govulncheck.

### Added

- Initial MCP server with 8 tools: `list_files`, `read_file`, `write_file`,
  `create_folder`, `move_file`, `delete`, `search_files`, `stat`.
- `stdio` and streamable-HTTP transports.
- Path jail, permission guard (`read` / `write` / `destructive`), and
  per-account circuit breaker.
- Multi-account pass-through mode (experimental).
- Distroless container image with `--healthcheck`.
- Project governance: `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `MAINTAINERS.md`, `SUPPORT.md`, issue and pull request templates.
- Apache-2.0 license with `NOTICE` file and SPDX headers in all Go sources.
- `write_file` now auto-creates missing parent folders (idempotent MKCOL)
  before uploading, so nested paths work without a prior `create_folder`.
- Config defaults for embedded use: `MaxReadBytes`, `MaxListEntries`,
  `CircuitBreakerThreshold`, and `CircuitBreakerWindow` are filled in
  automatically when the server is constructed programmatically.

### Fixed

- PROPFIND XML unmarshaling: property values (`resourcetype`,
  `getcontentlength`, `getlastmodified`, `getcontenttype`) were silently
  dropped due to a Go `encoding/xml` limitation with anonymous nested structs.
  Replaced with named types and a custom `UnmarshalXML` for `resourcetype`
  collection detection. This affects `list_files`, `stat`, and `search_files`.
- Error mapping: HTTP 409 Conflict and 412 PreconditionFailed are now both
  mapped to the `conflict` semantic code (Nextcloud uses 412 for MOVE with
  `Overwrite: F` on an existing destination).
- MKCOL on an existing folder returns 405 MethodNotAllowed from Nextcloud;
  this is now correctly mapped to `conflict` instead of a generic error.

[Unreleased]: https://github.com/valdrent/nextcloud-mcp-fast/commits/main
