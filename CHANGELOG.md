# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **BREAKING:** HTTP mode now requires `NEXTCLOUD_MCP_HTTP_TOKEN` (min 32
  chars), sent as `Authorization: Bearer <token>`.
- **BREAKING:** the default HTTP bind address is now `127.0.0.1:8000`; the
  Docker image sets `NEXTCLOUD_MCP_HTTP_ADDR=:8000`.
- **BREAKING:** credentials are no longer accepted as tool arguments;
  passthrough reads them only from `X-Nextcloud-*` headers and requires http
  mode.

### Security

- Passthrough hosts must be on `NEXTCLOUD_MCP_ALLOWED_HOSTS` (the configured
  host is always allowed).
- Credential verification is coalesced (singleflight) and runs outside any
  registry-wide lock.
- Client and negative auth caches are bounded and expire; auth failures now
  count toward the circuit breaker; breaker key map is capped.

### Fixed

- `list_files` pagination no longer repeats pages and uses one PROPFIND.
- `read_file` offset handling; negative offset/length are rejected.
- Circuit breaker double-counted failures after window pruning.

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
