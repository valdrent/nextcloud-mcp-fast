# Testing Strategy

This document describes how `nextcloud-mcp-fast` is tested. The goal is a
fast, hermetic test suite that runs in CI without network access or a live
Nextcloud instance, plus an optional end-to-end tier against a real server for
releases.

## Philosophy

- **Hermetic by default.** Every test must pass offline. WebDAV is faked with
  `net/http/httptest` in-process servers; no external services are required.
- **Layered.** Each package is tested at its own boundary so a regression is
  localized to the smallest possible unit.
- **Security-critical paths get extra coverage.** The path jail, permission
  guard, and circuit breaker are the trust boundary between an LLM (an
  untrusted input source) and your Nextcloud. They are tested more exhaustively
  than convenience code.
- **Deterministic.** Time is injected where it matters (breaker), and all mocks
  return fixed data. No sleeps, no flaky ordering.

## Test tiers

| Tier | Scope | Runs in | Trigger |
|---|---|---|---|
| **Unit** | Single package, pure logic | `go test ./...` | Every commit (CI) |
| **Integration** | Multiple packages, in-process WebDAV mock | `go test ./...` (no build tag; lives in `internal/mcpsrv`) | Every commit (CI) |
| **E2E** | Real compiled binary driven over stdio + in-process WebDAV mock | `make e2e` / GitHub Actions | Every PR (CI), on demand locally |

The unit and integration tiers together run in well under 5 seconds and require
no network. The E2E tier is the only one that starts a real process; it still
uses an in-process WebDAV mock rather than a live server, so it stays fast and
hermetic while validating process startup, env-var config parsing, stdio
framing, and the healthcheck flag — the things mocks cannot.

## Tier 1: Unit tests

Pure functions with no I/O. Fast, table-driven, exhaustive on edge cases.

### `internal/sanitize` — the path jail

The single most important security component. Tests cover:
- Normalization (leading `/`, `..` resolution, duplicate slashes, `.`).
- Traversal rejection (`../`, encoded `%2e%2e`, double-encoded `%252e%252e`).
- NUL bytes and other control characters.
- Unicode / NFC normalization (e.g. `café` vs decomposed form).
- Paths that *legitimately* resolve inside the jail after `..` (must be allowed).

> Rule: any new decoding or normalization step added to `Sanitize` must ship
> with a test case proving it cannot escape the jail.

### `internal/breaker` — circuit breaker

Time is injected via an overridable `now` function so tests are deterministic:
- Tripping after N failures within the window.
- Per-account isolation (one account tripping must not affect another).
- Window expiry clearing the state.
- `Forget` resetting a streak on success.
- Disabled mode (`limit=0`) never trips.

### `internal/perm` — permission guard

Table-driven over every (level, operation) pair:
- `read` allows only read ops.
- `write` allows read + write, rejects destructive.
- `destructive` allows everything.
- Unknown level string returns an error at construction.
- Denied operations return the correct semantic error code (`permission_denied`).

### `internal/errors` — semantic errors

- `FromStatus` maps every relevant HTTP status to the right code.
- `Is` correctly identifies wrapped semantic errors.
- `Error()` formatting includes the HTTP status when present.

### `internal/config` — configuration

Uses `t.Setenv` (auto-cleanup) to test:
- Defaults applied when variables are absent.
- Host normalization (bare hostname → `https://`).
- Permission alias (`full` → `destructive`).
- Validation failures: bad transport, bad permission, out-of-range limits,
  missing credentials in single-user mode.
- Passthrough mode relaxing the credential requirement.

### `internal/webdav` — client helpers (no network)

Pure functions only:
- `toRel` converting absolute WebDAV hrefs to jail-relative paths (including
  rejecting hrefs for *other* users).
- `baseName`, `normalizeDir`.
- `URL` building the correct endpoint.
- `Credentials.ID()` stability and low-cardinality property.

### `internal/accounts` — registry

- Single-user mode ignores per-request credentials.
- Passthrough mode resolves distinct accounts to distinct clients.
- Caching: same account ID returns the same client pointer.
- Concurrent `Resolve` calls are safe (run with `-race`).

## Tier 2: Integration tests

Cross-package behavior using an in-process WebDAV mock (`httptest.Server`).
These live in `internal/mcpsrv/*_test.go` and exercise the full request path:
**MCP client → tool handler → guard/breaker/sanitize → WebDAV client → mock server**.

### The mock

`mockWebDAV` (in `server_test.go`) is a minimal but protocol-accurate WebDAV
server that handles `PROPFIND`, `GET`, `PUT`, `MKCOL`, `MOVE`, and `DELETE`
against an in-memory file tree. It returns correct status codes (207, 201,
204, 404, 409) so the client's error mapping is exercised for real.

> As features grow, extend the mock rather than adding new mocks. One canonical
> fake keeps behavior consistent across tests.

### Scenarios covered today

- **Tool discovery**: all 8 tools are registered.
- **Happy paths**: `list_files` and `read_file` (text).
- **Permission guard**: a `read`-only server rejects `write_file` with
  `permission_denied`.
- **Path jail at the tool boundary**: a traversal argument to `read_file` is
  rejected with `path_forbidden`.

### Scenarios still to be added

- **Tool annotations**: read-only/destructive hints are correct for each tool.
- **Happy paths** for the remaining tools: `read_file` (base64), `write_file`,
  `create_folder`, `move_file`, `delete`, `search_files`, `stat`.
- **Permission guard**: `delete` is rejected below `destructive`; a
  `destructive` server allows it.
- **Path jail**: assert the mock was not hit when a traversal is rejected; cover
  the `to` argument of `move_file`.
- **Circuit breaker through the stack**: force N failures, then verify the next
  call returns `circuit_open` without reaching the mock.
- **Passthrough mode**: two different accounts in one server instance resolve to
  isolated clients; a failure on one does not trip the other.
- **Pagination**: `list_files` with `limit`/`offset` slices results correctly and
  emits `next_offset`.
- **Size caps**: reading a file larger than `MaxReadBytes` is truncated and
  flagged `truncated: true`.

## Tier 3: End-to-end (gated by build tag)

Validates the *real* binary as a child process. This catches what in-process
mocks cannot: process startup, environment-variable config parsing, stdio JSON-RPC
framing, and the `--healthcheck` flag. It lives in `test/e2e/` behind the `e2e`
build tag so it never runs in the default suite.

- Builds the binary (`make build`) then drives it with an MCP client over stdio
  via the SDK's `CommandTransport`, against an in-process WebDAV mock.
- Asserts: all 8 tools are discovered, a `read_file` round-trip succeeds, and a
  traversal attempt is rejected with `path_forbidden`.
- Also runs the binary's `--healthcheck` flag directly and checks for `ok`.

This tier is **not** part of the default `go test ./...`; it is invoked by CI on
every PR and locally with `make e2e`. A future, heavier variant against a live
Nextcloud in Docker can be added as a release-only job without affecting this one.

## Conventions

- **Table-driven tests** for anything with more than two cases.
- **Subtests** (`t.Run`) named after the scenario, not the input value.
- **`t.Helper()`** in every helper that calls `t.Fatal*`.
- **No global state.** Each test builds its own config/server/mock.
- **Race detector on by default** in CI: `go test -race ./...`.
- **Coverage target.** We aim for ≥70% overall and ≥90% for `sanitize`,
  `perm`, and `breaker`. This is not yet enforced in CI; at the time of
  writing `sanitize` (~82%), `mcpsrv` (~46%), and `webdav` (~15%) are below
  target and are tracked as known gaps.

## Running the tests

```sh
# Everything that runs in CI (unit + integration), with race detector:
go test -race ./...

# Coverage report:
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Security-critical packages only:
go test -race ./internal/sanitize/ ./internal/perm/ ./internal/breaker/

# End-to-end (requires the binary to be built first; no network needed):
make e2e
```

## CI integration

GitHub Actions runs, on every push and PR:

1. `gofmt` / `go vet` — style and static analysis.
2. `go test -race ./...` — unit + integration tiers.
3. Overall coverage printed in the job log (see the target above).
4. A separate `e2e` job runs `make e2e`, driving the real binary over stdio.
5. A `vulncheck` job runs `govulncheck` against the module and its
   dependencies.

## What we deliberately do NOT test

- **Nextcloud server behavior.** We trust its WebDAV implementation; we test our
  client's interpretation of it, not the server itself.
- **Performance benchmarks** as pass/fail gates. We keep `-bench` available for
  manual memory/latency checks (the project's whole point is low RAM) but do not
  gate merges on them.
