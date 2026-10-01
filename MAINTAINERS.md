# Maintainers and Governance

`nextcloud-mcp-fast` is an open-source project sponsored and maintained by
**[Valdrent](https://github.com/valdrent)**. Valdrent provides the maintainers
and release management; the roadmap is developed in the open and community
contributions are reviewed under the same rules as internal ones.

## Maintainers

<!-- Keep this list in sync with .github/CODEOWNERS. -->

| GitHub | Role |
|---|---|
| [@valdrent](https://github.com/valdrent) | Owner, maintainer, releases |
| [@fjdevel](https://github.com/fjdevel) | Maintainer |

Private contact for maintainers (Code of Conduct reports, licensing, and
partnership questions): **[support@valdrent.com](mailto:support@valdrent.com)**.
Security reports go through [SECURITY.md](SECURITY.md).

## Roles

- **Contributor** — anyone who opens an issue or pull request.
- **Maintainer** — can review, approve, and merge pull requests, triage issues,
  and cut releases. Maintainers are listed above.

The *trust boundary* of the project is the path jail (`internal/sanitize`),
the permission guard (`internal/perm`), the circuit breaker
(`internal/breaker`), and credential handling (`internal/accounts`,
`internal/mcpsrv`). Changes there get extra scrutiny (see below).

## Decision making

- Day-to-day decisions are made through pull request review; one maintainer
  approval is required.
- Changes to the trust boundary must include tests and must be reviewed by a
  maintainer other than the author.
- Significant changes (new tools, transport changes, breaking configuration
  changes, new dependencies) are proposed in an issue labeled `proposal` and
  remain open for comments for at least 7 days.
- When consensus cannot be reached, the maintainers make the
  final call and documents the reasoning in the issue.

## Becoming a maintainer

Contributors with a sustained record of high-quality contributions and reviews
may be nominated by an existing maintainer. Nominations are approved by the
current maintainers.

## Releases

- The project follows [Semantic Versioning](https://semver.org/). Before 1.0,
  minor versions may contain breaking changes, which are always called out in
  [CHANGELOG.md](CHANGELOG.md).
- Releases are tagged `vX.Y.Z` from `main`; container images are published to
  `ghcr.io/valdrent/nextcloud-mcp-fast`.
- After the first release, set the `nextcloud-mcp-fast` package visibility to
  **Public** in the GHCR package settings (new packages are private by default),
  otherwise `docker pull` fails for users and for Copilot.
