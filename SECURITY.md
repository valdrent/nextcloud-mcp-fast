# Security Policy

`nextcloud-mcp-fast` sits between an LLM (an untrusted input source) and your
Nextcloud data, so we treat security reports as our highest priority.

## Supported versions

The project is pre-1.0. Only the latest release and the `main` branch receive
security fixes.

| Version | Supported |
|---|---|
| `main` / latest release | ✅ |
| Older pre-releases | ❌ |

Once 1.0 ships, the latest minor release of the current major version will be
supported, and this table will be updated.

## Reporting a vulnerability

**Do not open a public issue, discussion, or pull request for security
problems.**

Report privately through GitHub's private vulnerability reporting:

👉 <https://github.com/valdrent/nextcloud-mcp-fast/security/advisories/new>

If you cannot use GitHub, email **support@valdrent.com** with the subject
`[SECURITY] nextcloud-mcp-fast` and we will move the report to a private
advisory.

Please include:

- The affected version or commit.
- Configuration relevant to the issue (transport, permission level,
  pass-through on/off) — **never include real credentials**.
- A description of the impact and, if possible, a minimal reproduction.

## What to expect

| Step | Target |
|---|---|
| Acknowledgement of your report | within 3 business days |
| Initial assessment and severity | within 10 business days |
| Fix or mitigation for critical/high issues | as fast as possible, coordinated with you |

We follow coordinated disclosure: we will agree on a disclosure date with you,
publish a GitHub Security Advisory (with a CVE when applicable), and credit you
unless you prefer to remain anonymous.

## Scope

In scope:

- Escaping the path jail (`internal/sanitize`) or reaching another account's files.
- Bypassing the permission guard (`internal/perm`) or the circuit breaker.
- Credential leakage or credential confusion between accounts.
- Server-side request forgery through configurable hosts.
- Vulnerabilities in the published container image.

Out of scope:

- Vulnerabilities in Nextcloud itself — report those to the
  [Nextcloud security program](https://hackerone.com/nextcloud).
- Issues that require an attacker who already controls the host, the
  environment variables, or the App Password.
- Actions an LLM performs *within* the permission level the operator configured.

## Verifying container images

Docker images published at `ghcr.io/valdrent/nextcloud-mcp-fast` are signed with
[cosign](https://docs.sigstore.dev/cosign/). Verify the signature before use:

```sh
cosign verify ghcr.io/valdrent/nextcloud-mcp-fast:<tag> \
  --certificate-identity-regexp 'https://github.com/valdrent/nextcloud-mcp-fast/.github/workflows/release.yml@refs/tags/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

This ensures the image was built by the release workflow and has not been tampered with. Images
include SBOMs (Software Bill of Materials) and provenance attestations for supply-chain transparency.

## Hardening guidance

See the *Security model* section of the [README](README.md#security-model)
for deployment recommendations.
