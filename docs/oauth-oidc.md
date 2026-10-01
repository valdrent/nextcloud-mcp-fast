# OAuth for Claude Cowork / claude.ai (mode A: external OIDC provider)

Claude connects to remote MCP servers from Anthropic's cloud, so the server
needs a public **https** URL and OAuth 2.0 (a static Bearer token is not
supported by Claude's custom connectors). In `oidc` mode this server is only
the *resource server*: your identity provider (Keycloak, Authentik, Auth0,
Zitadel, …) issues the tokens.

> The default auth mode is `static`: a single shared Bearer token
> (`NEXTCLOUD_MCP_HTTP_TOKEN`) for one configured account. That is enough for
> local or self-hosted clients (Claude Desktop over a tunnel, your own scripts)
> and requires no identity provider at all. Use an OAuth mode only when the
> client cannot send a static token — today, that means Claude Cowork /
> claude.ai custom connectors — or when you need per-user accounts on one
> endpoint. The other OAuth mode (Nextcloud as authorization server) is
> documented in [docs/oauth-nextcloud.md](docs/oauth-nextcloud.md).

## 1. Server configuration

```sh
NEXTCLOUD_MCP_TRANSPORT=http
NEXTCLOUD_MCP_AUTH_MODE=oidc
NEXTCLOUD_MCP_PUBLIC_URL=https://mcp.example.com/mcp     # resource id + required token audience
NEXTCLOUD_MCP_OIDC_ISSUER=https://idp.example.com/realms/main
NEXTCLOUD_MCP_ACCOUNTS_FILE=/run/secrets/nc-accounts.json  # chmod 600
NEXTCLOUD_HOST=https://cloud.example.com
NEXTCLOUD_MCP_PERMISSIONS=read
```

`NEXTCLOUD_MCP_HTTP_TOKEN`, `NEXTCLOUD_USERNAME` and `NEXTCLOUD_PASSWORD` are
not used in this mode.

Accounts file (token `sub`, or as fallback lower-case `email` /
`preferred_username`, → Nextcloud App Password). `host` is optional and defaults
to `NEXTCLOUD_HOST`; any other host must be in `NEXTCLOUD_MCP_ALLOWED_HOSTS`.
Users that are not listed are denied.

```json
{
  "3f1c…-sub-of-alice": { "username": "alice", "app_password": "xxxxx-xxxxx-xxxxx-xxxxx-xxxxx" },
  "bob@example.com":    { "host": "https://cloud.example.com", "username": "bob", "app_password": "…" }
}
```

## 2. What the server publishes

- `GET /.well-known/oauth-protected-resource[/mcp]` (RFC 9728), listing the issuer as authorization server.
- Unauthenticated requests get `401` with
  `WWW-Authenticate: Bearer resource_metadata="…"`, which starts Claude's OAuth discovery.
- Tokens are verified locally: RS256/ES256 signature against the issuer's JWKS,
  `iss`, `exp`/`nbf`, and **`aud` must contain `NEXTCLOUD_MCP_PUBLIC_URL`**.

## 3. Identity provider requirements

- Authorization code flow with **PKCE (S256)**.
- Allow redirect URIs `https://claude.ai/api/mcp/auth_callback` and `https://claude.com/api/mcp/auth_callback`.
- Dynamic Client Registration (RFC 7591), **or** register a client manually and
  paste its ID/secret into *Advanced settings* when adding the connector.
- Access tokens must be JWTs whose `aud` is the public MCP URL (Keycloak: add an
  *Audience* mapper; Auth0/Zitadel: set the API identifier to the URL).
- Optional scopes `mcp:read`, `mcp:write`, `mcp:destructive` cap what a token may
  do; they can only *lower* `NEXTCLOUD_MCP_PERMISSIONS`. A token without any
  `mcp:*` scope is read-only.

## 4. Add the connector

Cowork / claude.ai → *Settings → Connectors → Add custom connector* → URL
`https://mcp.example.com/mcp` (add client ID/secret under *Advanced* if the IdP
has no DCR). On Team/Enterprise plans an Owner adds it first.
