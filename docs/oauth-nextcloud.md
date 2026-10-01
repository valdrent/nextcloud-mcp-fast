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

## Limits

- Single Nextcloud host per deployment.
- No per-token scopes: `NEXTCLOUD_MCP_PERMISSIONS` applies to every user.
