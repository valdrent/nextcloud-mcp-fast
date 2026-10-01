# OAuth for Claude Cowork / claude.ai (mode B: Nextcloud as authorization server)

No external identity provider: Nextcloud's `oauth2` app issues the tokens and
this server validates them by asking Nextcloud who they belong to.

> The default auth mode is `static`: a single shared Bearer token
> (`NEXTCLOUD_MCP_HTTP_TOKEN`) for one configured account — no identity
> provider needed, but it cannot be used from Claude Cowork / claude.ai, which
> only supports OAuth 2.0. See [docs/oauth-oidc.md](docs/oauth-oidc.md) for the
> other OAuth mode (external IdP).

> **Experimental.** Two behaviours depend on your Nextcloud version and must be
> verified once: (1) the `oauth2` app must support PKCE (S256), and (2) tokens
> must be accepted as `Authorization: Bearer` on `/ocs/v2.php/cloud/user`.
> If either fails, use mode A (`docs/oauth-oidc.md`).

## 1. Server configuration

```sh
NEXTCLOUD_MCP_TRANSPORT=http
NEXTCLOUD_MCP_AUTH_MODE=nextcloud
NEXTCLOUD_MCP_PUBLIC_URL=https://mcp.example.com/mcp
NEXTCLOUD_HOST=https://cloud.example.com        # fixed; tokens for other hosts are never accepted
NEXTCLOUD_MCP_PERMISSIONS=read                  # ceiling (Nextcloud tokens carry no scopes)
# optional: NEXTCLOUD_MCP_ACCOUNTS_FILE=…       # explicit user -> App Password override
```

No App Passwords are needed: the verified Nextcloud user and the access token
(an app token) are used as the WebDAV Basic credentials. If an accounts file is
given, a matching user uses that entry instead.

## 2. Register the client in Nextcloud

*Administration settings → Security → OAuth 2.0 clients → Add client*:

- Name: `Claude`
- Redirection URI: `https://claude.ai/api/mcp/auth_callback`
  (add `https://claude.com/api/mcp/auth_callback` as a second client if needed;
  Nextcloud accepts one URI per client)

Copy the generated **Client ID** and **Secret**. Nextcloud has no Dynamic Client
Registration, so these are entered manually in Claude.

## 3. What the server publishes

- `/.well-known/oauth-protected-resource[/mcp]` pointing at the origin of `PUBLIC_URL` as authorization server.
- `/.well-known/oauth-authorization-server` (RFC 8414) whose `authorization_endpoint`
  and `token_endpoint` point straight at Nextcloud (`/index.php/apps/oauth2/…`).
- Tokens are checked against `GET {NEXTCLOUD_HOST}/ocs/v2.php/cloud/user` and
  cached for 60 s, so a revoked token stops working within a minute.

## 4. Add the connector

Cowork / claude.ai → *Settings → Connectors → Add custom connector* → URL
`https://mcp.example.com/mcp` → *Advanced settings*: paste the Client ID and Secret.

## 5. Example deployment (Docker Compose)

A hardened setup validated in production, behind a reverse proxy that
terminates TLS and forwards `https://mcp.example.com/mcp` to `127.0.0.1:8086`:

```yaml
services:
  nextcloud-mcp-fast:
    image: ghcr.io/valdrent/nextcloud-mcp-fast:v1.0.0   # pin a version, not :latest
    container_name: nextcloud-mcp-fast
    restart: unless-stopped
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    mem_limit: 128m
    cpus: 0.5
    ports:
      - "127.0.0.1:8086:8000"
    environment:
      NEXTCLOUD_HOST: https://cloud.example.com
      NEXTCLOUD_MCP_TRANSPORT: http
      NEXTCLOUD_MCP_HTTP_ADDR: ":8000"
      NEXTCLOUD_MCP_AUTH_MODE: nextcloud
      NEXTCLOUD_MCP_PUBLIC_URL: https://mcp.example.com/mcp
      NEXTCLOUD_MCP_PERMISSIONS: write
      NEXTCLOUD_MCP_LOG_LEVEL: info
```

There are no secrets in this file: the server is stateless and only validates
tokens that Nextcloud issued.

## 6. Verify the deployment

```sh
# Discovery metadata must be public (200)
curl -s -o /dev/null -w '%{http_code}\n' https://mcp.example.com/.well-known/oauth-authorization-server
# The MCP endpoint must reject unauthenticated calls (401)
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://mcp.example.com/mcp
```

## Troubleshooting

- **"Permission denied" / authorization fails in Claude**: the Client ID/Secret
  must come from *Security → OAuth 2.0 clients* of the `oauth2` app. Clients
  created by other apps (e.g. `oidc`) are not known to `oauth2`.
- **Claude rejects the redirect URI**: create a second client with
  `https://claude.com/api/mcp/auth_callback`.
- **Need fewer privileges**: lower `NEXTCLOUD_MCP_PERMISSIONS` to `read` or
  `write` and run `docker compose up -d`. Deleted files go to Nextcloud's trash.

## Resource usage

Measured on a real deployment replacing a Python-based Nextcloud MCP server:

| | Previous MCP (Python) | nextcloud-mcp-fast (Go) |
|---|---|---|
| Idle RAM | ~369 MB | ~7 MB (limit 128 MB) |
| Reduction | — | ~98 % (~360 MB) |
| Tools | Broad suite (files, calendar, notes…) | 8, files only |
| Authentication | Own OAuth facade + `oidc` app + encrypted token store | Native Nextcloud `oauth2`, stateless |
| Extra components | Data volume, encryption key, secrets in env | None |

The trade-off is scope: this server only handles files.

## Limits

- Single Nextcloud host per deployment.
- No per-token scopes: `NEXTCLOUD_MCP_PERMISSIONS` applies to every user.
