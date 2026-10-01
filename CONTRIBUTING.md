# Contributing to nextcloud-mcp-fast

Thanks for your interest in contributing! `nextcloud-mcp-fast` is an open-source
project maintained by [Valdrent](https://github.com/valdrent) together with the
community. Contributions of every size are welcome: bug reports, docs fixes,
tests, and features.

By participating you agree to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Before you start

- **Security issues** must be reported privately — see [SECURITY.md](SECURITY.md).
- **Bugs and small fixes:** open an issue or go straight to a pull request.
- **New features or behavior changes:** open an issue first so maintainers can
  agree on the approach before you invest time. This is especially important
  for anything that touches the security boundary or adds dependencies.

## Development setup

Requirements: Go (version in [`go.mod`](go.mod)), `make`, and optionally Docker.

```sh
git clone https://github.com/valdrent/nextcloud-mcp-fast.git
cd nextcloud-mcp-fast
make build      # static binary in bin/
make test       # unit + integration tests
make e2e        # end-to-end tests against the real binary
make lint       # go vet + gofmt
```

No live Nextcloud instance is needed; the suites are hermetic. See
[`docs/TESTING.md`](docs/TESTING.md).

## Ground rules

1. **Security changes need tests.** Any change to `sanitize`, `perm`,
   `breaker`, or credential handling (`accounts`, `mcpsrv`) must ship with tests
   that prove the new behavior — especially that paths still cannot escape the
   jail and that accounts stay isolated.
2. **Keep it hermetic.** Tests in the default suite must not use the network or
   a live server; put those in the E2E tier.
3. **No heavy dependencies.** The project's value is its small footprint. If a
   change adds a dependency, justify it in the pull request.
4. **Follow existing conventions.** Table-driven tests, `t.Helper()` in helpers,
   no global state, `gofmt` / `go vet` clean.
5. **Document user-facing changes** in the README (both `README.md` and
   `README.es.md`) and add an entry under `Unreleased` in
   [CHANGELOG.md](CHANGELOG.md).

## Pull request process

1. Fork the repository and create a topic branch from `main`.
2. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit
   messages (`feat:`, `fix:`, `docs:`, `test:`, `chore:`, `refactor:`,
   `security:`).
3. Sign off every commit to certify the
   [Developer Certificate of Origin](https://developercertificate.org/):

   ```sh
   git commit -s -m "fix: reject backslashes in paths"
   ```

4. Make sure CI passes (`gofmt`, `go vet`, tests with `-race`, E2E,
   `govulncheck`).
5. Fill in the pull request template. At least one approval from a maintainer
   listed in [CODEOWNERS](.github/CODEOWNERS) is required to merge; changes to
   the trust boundary must be reviewed by a maintainer other than the author
   (see [MAINTAINERS.md](MAINTAINERS.md)).

## Licensing of contributions

This project is licensed under the [Apache License, Version 2.0](LICENSE).
Per section 5 of the license, any contribution you intentionally submit is
licensed under the same terms (inbound = outbound), including the patent grant
in section 3. The DCO sign-off certifies you have the right to submit it.

New Go source files should start with the SPDX header:

```go
// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0
```

## Questions

See [SUPPORT.md](SUPPORT.md) for where to ask questions and how to get help.
