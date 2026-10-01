# nextcloud-mcp-fast

[![CI](https://github.com/valdrent/nextcloud-mcp-fast/actions/workflows/ci.yml/badge.svg)](https://github.com/valdrent/nextcloud-mcp-fast/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/valdrent/nextcloud-mcp-fast.svg)](https://pkg.go.dev/github.com/valdrent/nextcloud-mcp-fast)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

[English](README.md) | [Español](README.es.md)

A lightweight, open-source **MCP (Model Context Protocol) server** that gives
LLM clients access to [Nextcloud](https://nextcloud.com) files over WebDAV.
Written in Go for a tiny memory footprint (~20–50 MB idle) and a single static
binary you can run anywhere. Maintained by [Valdrent](https://github.com/valdrent)
and the community.

It is built to be **multi-tenant**: one process can serve many Nextcloud
accounts, resolving per-account credentials on demand (pass-through), sharing a
single HTTP connection pool, and keeping no heavy per-user state.

> **Status:** early development (pre-1.0) — the API surface is stable for local
> use but may still evolve; breaking changes are listed in the
> [CHANGELOG](CHANGELOG.md). Multi-account pass-through is **experimental** and
> not recommended for production yet (see [Security model](#security-model)).
> Contributions are welcome; see [Contributing](#contributing).

## Why this exists

Nextcloud already exposes files over WebDAV, but there was no small, safe way to
hand that to an LLM agent. Existing options were either heavyweight, single-user,
or gave the model unrestricted access to a server. This project aims to be:

- **Small** — one static binary, low RAM, trivially containerized.
- **Safe by default** — read-only unless you opt in; every path is jailed;
  runaway loops are cut off by a circuit breaker.
- **Honest about security** — it does *not* replace Nextcloud's own ACLs or
  authentication; it adds a local guardrail on top of an App Password.

## Features

- **8 MCP tools**: `list_files`, `read_file`, `write_file`, `create_folder`,
  `move_file`, `delete`, `search_files`, `stat`.
- **Two transports**: `stdio` (local clients like Claude Desktop) and
  `streamable-http` (remote deployments), with three auth modes for HTTP: a
  shared Bearer token (`static`, default) or OAuth 2.0 access tokens
  (`oidc` / `nextcloud`) for cloud clients such as Claude Cowork.
- **Security by default**:
  - *Path jail* — recursive percent-decoding + NFC normalization + lexical
    containment check. Traversal (`..`, encoded or double-encoded `%2e%2e`,
    NUL bytes, invalid UTF-8) is rejected before any request leaves the process.
  - *Permission guard* — `read` / `write` / `destructive` levels; destructive
    ops are off unless explicitly enabled.
  - *Circuit breaker* — sliding-window rate limit per account to stop runaway
    LLM loops from hammering your Nextcloud.
- **Low RAM**: one shared `http.Client` across all accounts, bounded connection
  pool, no per-user goroutine storms.
- **Docker-ready**: multi-stage build on a distroless base image with a built-in
  `--healthcheck`.

## Quick start

### Install

```sh
go install github.com/valdrent/nextcloud-mcp-fast@latest
# or pull the container image
docker pull ghcr.io/valdrent/nextcloud-mcp-fast:latest
```

### Build & run (stdio)

```sh
git clone https://github.com/valdrent/nextcloud-mcp-fast.git && cd nextcloud-mcp-fast
make build
export NEXTCLOUD_HOST=https://cloud.example.com
export NEXTCLOUD_USERNAME=you
export NEXTCLOUD_PASSWORD=<App Password>   # Settings → Security → App passwords
./bin/nextcloud-mcp-fast
```

### Docker

```sh
docker compose up -d
# or
make docker VERSION=v1.0.0
```

See [`docker-compose.yml`](docker-compose.yml) for a memory-bounded, read-only
container example.

### Verifying the image

Docker images are signed with [cosign](https://docs.sigstore.dev/cosign/). Verify the signature before using:

```sh
cosign verify ghcr.io/valdrent/nextcloud-mcp-fast:v1.0.0 \
  --certificate-identity-regexp 'https://github.com/valdrent/nextcloud-mcp-fast/.github/workflows/release.yml@refs/tags/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

This ensures the image was built by the release workflow and has not been tampered with. Keyless signing
uses GitHub's OIDC token, so no keys are stored or rotated.

## Configuration (environment variables)

| Variable | Required | Default | Description |
|---|---|---|---|
| `NEXTCLOUD_HOST` | yes | — | Nextcloud base URL, e.g. `https://cloud.example.com` |
| `NEXTCLOUD_USERNAME` | * | — | Account username (omit if pass-through is on) |
| `NEXTCLOUD_PASSWORD` | * | — | **App Password** (not your login password) |
| `NEXTCLOUD_MCP_TRANSPORT` | no | `stdio` | `stdio` or `http` |
| `NEXTCLOUD_MCP_HTTP_ADDR` | no | `127.0.0.1:8000` | Listen address for HTTP mode (the Docker image sets `:8000`) |
| `NEXTCLOUD_MCP_HTTP_TOKEN` | http mode, `static` auth | — | Bearer token, min 32 chars (e.g. `openssl rand -hex 32`); clients send `Authorization: Bearer <token>`. Not used in OAuth modes |
| `NEXTCLOUD_MCP_ALLOWED_HOSTS` | no | — | Comma-separated `scheme://host[:port]` allowlist for passthrough; the configured `NEXTCLOUD_HOST` is always allowed; `http://` entries only if the default host is `http://` |
| `NEXTCLOUD_MCP_PERMISSIONS` | no | `read` | `read`, `write`, or `destructive` (alias `full`) |
| `NEXTCLOUD_MCP_PASSTHROUGH` | no | `false` | Allow per-request credentials (multi-account); requires `http` mode. Credentials come only from the `X-Nextcloud-Host`/`X-Nextcloud-Username`/`X-Nextcloud-Password` headers |
| `NEXTCLOUD_MCP_MAX_READ_BYTES` | no | `131072` | Cap for a single `read_file` call |
| `NEXTCLOUD_MCP_MAX_LIST_ENTRIES` | no | `50` | Entries per `list_files` page (max 200) |
| `NEXTCLOUD_MCP_HTTP_TIMEOUT` | no | `30s` | Per-request WebDAV timeout |
| `NEXTCLOUD_MCP_CB_THRESHOLD` | no | `10` | Failed calls within window before the breaker trips (`0` disables) |
| `NEXTCLOUD_MCP_CB_WINDOW` | no | `1m` | Sliding-window duration for the breaker |
| `NEXTCLOUD_MCP_LOG_LEVEL` | no | `info` | JSON audit log verbosity: `debug`, `info`, `warn`, or `error`; logs every tool call (tool, account, paths, outcome, duration) to stderr |
| `NEXTCLOUD_MCP_AUTH_MODE` | no | `static` | HTTP auth mode: `static` (shared Bearer token), `oidc` or `nextcloud` (OAuth 2.0 access tokens; required by Claude Cowork/claude.ai). OAuth modes need `http` transport and `NEXTCLOUD_MCP_PUBLIC_URL`. See [docs/oauth-oidc.md](docs/oauth-oidc.md) and [docs/oauth-nextcloud.md](docs/oauth-nextcloud.md) |
| `NEXTCLOUD_MCP_PUBLIC_URL` | oauth modes | — | Public https URL of the MCP endpoint (OAuth resource identifier and required token audience; http allowed on loopback only) |
| `NEXTCLOUD_MCP_OIDC_ISSUER` | oidc | — | Issuer URL of the OIDC provider (https; http allowed on loopback only) |
| `NEXTCLOUD_MCP_ACCOUNTS_FILE` | oidc | — | JSON file (mode 0600) mapping OAuth users to Nextcloud App Passwords; optional override in `nextcloud` mode |

\* Required unless `NEXTCLOUD_MCP_PASSTHROUGH=true` or an OAuth auth mode is used.

## MCP client configuration

Claude Desktop (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "nextcloud": {
      "command": "/path/to/nextcloud-mcp-fast",
      "env": {
        "NEXTCLOUD_HOST": "https://cloud.example.com",
        "NEXTCLOUD_USERNAME": "you",
        "NEXTCLOUD_PASSWORD": "<App Password>",
        "NEXTCLOUD_MCP_PERMISSIONS": "read"
      }
    }
  }
}
```

For remote/streamable-HTTP clients, point them at `http://host:8000/mcp`.

### Claude Cowork / claude.ai (custom connector)

Claude connects from Anthropic's cloud and only supports OAuth 2.0 for remote
servers, so a static Bearer token will not work. You need a public **https** URL
and one of two OAuth modes:

| Mode | Authorization server | User → Nextcloud account | Guide |
|---|---|---|---|
| `NEXTCLOUD_MCP_AUTH_MODE=oidc` | Your own IdP (Keycloak, Authentik, Auth0, Zitadel…), supports DCR | `NEXTCLOUD_MCP_ACCOUNTS_FILE` (required) | [docs/oauth-oidc.md](docs/oauth-oidc.md) |
| `NEXTCLOUD_MCP_AUTH_MODE=nextcloud` *(experimental)* | Nextcloud's `oauth2` app; client ID/secret entered manually in Claude | The verified user + token act as WebDAV credentials directly (accounts file optional) | [docs/oauth-nextcloud.md](docs/oauth-nextcloud.md) |

In both modes the server is only the *resource server*: it publishes RFC 9728
protected-resource metadata, verifies each access token (`oidc`: local JWT
verification against the issuer's JWKS; `nextcloud`: a check against
Nextcloud's OCS API, cached 60 s), and maps the authenticated user to a
Nextcloud account. Tokens are checked on every request, so revocation takes
effect immediately (within the 60 s cache in `nextcloud` mode).

Then add the connector in *Settings → Connectors → Add custom connector* with
the URL `https://your-domain/mcp`. The OAuth callback to allow is
`https://claude.ai/api/mcp/auth_callback`.

### Real-world footprint

On a production deployment replacing a Python-based Nextcloud MCP server, idle
memory dropped from ~369 MB to ~7 MB (~98 % less), with no token store, data
volume or secrets on the server. The trade-off is scope: 8 file tools only.
Step-by-step setup, a hardened Compose file and troubleshooting are in
[docs/oauth-nextcloud.md](docs/oauth-nextcloud.md).

### GitHub Copilot cloud agent and code review

In the repository, go to **Settings → Copilot → MCP servers** and paste:

```json
{
  "mcpServers": {
    "nextcloud": {
      "type": "local",
      "command": "docker",
      "args": [
        "run", "--rm", "-i", "--read-only", "--memory=256m",
        "-e", "NEXTCLOUD_HOST",
        "-e", "NEXTCLOUD_USERNAME",
        "-e", "NEXTCLOUD_PASSWORD",
        "-e", "NEXTCLOUD_MCP_PERMISSIONS",
        "ghcr.io/valdrent/nextcloud-mcp-fast:latest"
      ],
      "env": {
        "NEXTCLOUD_HOST": "$COPILOT_MCP_NEXTCLOUD_HOST",
        "NEXTCLOUD_USERNAME": "$COPILOT_MCP_NEXTCLOUD_USERNAME",
        "NEXTCLOUD_PASSWORD": "$COPILOT_MCP_NEXTCLOUD_PASSWORD",
        "NEXTCLOUD_MCP_PERMISSIONS": "read"
      },
      "tools": ["list_files", "read_file", "search_files", "stat"]
    }
  }
}
```

Then create these under **Settings → Secrets and variables → Agents** (only
names starting with `COPILOT_MCP_` are visible to MCP servers):

| Name | Kind | Value |
|---|---|---|
| `COPILOT_MCP_NEXTCLOUD_HOST` | variable | `https://cloud.example.com` |
| `COPILOT_MCP_NEXTCLOUD_USERNAME` | secret | Nextcloud username |
| `COPILOT_MCP_NEXTCLOUD_PASSWORD` | secret | A dedicated **App Password** |

Notes:

- The `tools` allowlist exposes only read-only tools, and the server itself
  runs with `read` permissions. Copilot code review only uses read-only tools
  in any case.
- Use a dedicated Nextcloud account or App Password for Copilot so you can
  revoke it independently.
- The configuration uses the published, signed container image. For
  reproducible setups, pin a release tag (e.g. `:v1.0.0`) instead of `:latest`.
- To verify, open a Copilot session's logs and expand **Start MCP Servers**.

## Tools

| Tool | Permission | Description |
|---|---|---|
| `list_files` | read | List a directory (paginated via `limit`/`offset`) |
| `read_file` | read | Read a file (text or base64 with `encoding=base64`); returns `unsupported_type` error for binary content without `encoding=base64`; partial reads with `offset`/`length`; returns `server_error` if the server ignores a Range request; results marked `"trust":"untrusted"` |
| `write_file` | write | Create a file; parent folders are created automatically if missing; `overwrite` (default false) enables replacing existing files; overwrites require `destructive` permission |
| `create_folder` | write | Create a directory |
| `move_file` | write | Rename/move a path; `overwrite` (default false) to replace an existing destination; overwrites require `destructive` permission |
| `delete` | destructive | Delete a file or folder — **Nextcloud deletes folders recursively** (items go to the Nextcloud trash bin if it is enabled) |
| `search_files` | read | Case-insensitive name search using server-side WebDAV SEARCH (indexed), with fallback to bounded directory walk (max 500 folders, 60s budget) on servers without SEARCH support |
| `stat` | read | Metadata for a single path |

## Security model

Read this before running with writes enabled.

- **Use an App Password**, never your main account password. You can revoke it
  independently at any time.
- **The path jail is lexical and in-process.** The WebDAV client only ever sees
  sanitized paths under the account's files root. It is *not* a substitute for
  Nextcloud's server-side authorization — if your App Password can reach a file,
  so can this server (within the configured permission level).
- **Permission levels are a ceiling, not a floor.** `read` cannot write no matter
  what an LLM asks. Keep it at `read` unless you specifically need writes.
- **The circuit breaker protects your server**, not your data: it throttles a
  misbehaving account after repeated failures.
- **HTTP mode always requires authentication**: a shared Bearer token
  (`static`) or OAuth access tokens (`oidc` / `nextcloud`). Anyone holding a
  valid credential can act with the mapped account, so bind to `127.0.0.1` or
  put the server behind a reverse proxy that terminates TLS.
- **OAuth modes verify every token locally or against Nextcloud**: `oidc`
  checks signature (RS256/ES256 only), issuer, expiry and that the audience is
  your `NEXTCLOUD_MCP_PUBLIC_URL`; users not present in the accounts file are
  denied, and `mcp:*` scopes can only lower the permission level. The accounts
  file holds App Passwords and must be `chmod 600`. In `nextcloud` mode the
  token is validated against Nextcloud's OCS API (cached 60 s) and a revoked
  token stops working within a minute.
- **Always use `https://` for `NEXTCLOUD_HOST`.** With `http://`, the App
  Password travels in clear text.
- **Pass-through mode is experimental.** It lets clients choose the Nextcloud
  host and credentials per request (including through tool arguments, which the
  LLM can see). Do not enable it on a server that also has default credentials
  configured, and do not expose it to untrusted networks until it reaches
  stable status.
- **Run containers hardened**: `read_only`, bounded memory, non-root user (the
  distroless image already runs as `nonroot`). See the compose file.

Found a vulnerability? Please report it privately — see [SECURITY.md](SECURITY.md).

## Development

```sh
make build     # static binary to bin/nextcloud-mcp-fast
make test      # go test ./...
make lint      # go vet + gofmt
make run       # go run .
make docker    # build the container image
```

### Project layout

```
main.go                  entrypoint: config → registry → MCP server → transport
internal/config/         env loading, validation, permission parsing
internal/errors/         semantic error codes for LLM-friendly responses
internal/sanitize/       path jail (decode, NFC, containment)
internal/perm/           read/write/destructive guard
internal/breaker/        sliding-window circuit breaker per account
internal/webdav/         WebDAV client + operations (PROPFIND/GET/PUT/…)
internal/accounts/       multi-account registry with shared HTTP pool
internal/oauth/          OAuth resource server: OIDC/Nextcloud token verification, user mapping
internal/mcpsrv/         MCP server wiring, tool registration, handlers
docs/oauth-*.md          OAuth setup guides (OIDC provider, Nextcloud)
docs/TESTING.md          testing strategy and conventions
test/e2e/                end-to-end tests (build tag `e2e`)
```

### Testing

The suite is hermetic (no network, no live Nextcloud) and runs in seconds:

```sh
go test -race ./...
```

Coverage on the security-critical packages is high by design — see
[`docs/TESTING.md`](docs/TESTING.md) for the full strategy, tiers, and the
conventions we follow.

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for
the development workflow, ground rules, and pull request process. All
participants are expected to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

### Good first issues

- Add a `list_files` pagination test asserting `next_offset`.
- Document a real-world deployment (reverse proxy + streamable-http) in the README.
- Extend the full lifecycle test to cover `search_files` with nested folders
  and multi-level depth.

## Security

Please do **not** open public issues for security vulnerabilities. Follow the
private reporting process in [SECURITY.md](SECURITY.md).

## Governance and support

- [MAINTAINERS.md](MAINTAINERS.md) — maintainers, roles, decision making, and
  release policy.
- [SUPPORT.md](SUPPORT.md) — community and commercial support options.
- [CHANGELOG.md](CHANGELOG.md) — release notes.

## License

Copyright © 2026 Valdrent and the nextcloud-mcp-fast contributors.

Licensed under the [Apache License, Version 2.0](LICENSE). See [NOTICE](NOTICE)
for attribution.

Nextcloud is a trademark of Nextcloud GmbH. This project is not affiliated with
or endorsed by Nextcloud GmbH.
