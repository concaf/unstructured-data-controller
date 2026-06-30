# MCP Server

An OAuth-secured [Model Context Protocol](https://modelcontextprotocol.io) server that exposes the Unstructured Data Controller's capabilities as tools for LLM agents. Built with the [official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) using the Streamable HTTP transport.

## Architecture

```
MCP Client (Cursor, Claude Code, custom agent)
    │
    ├─ GET  /.well-known/oauth-protected-resource   ← discover auth requirements
    ├─ GET  /.well-known/oauth-authorization-server  ← discover auth endpoints
    ├─ POST /auth/register                           ← dynamic client registration
    ├─ GET  /auth/authorize                          ← start OAuth flow → redirects to SSO
    ├─ GET  /auth/callback/oidc                      ← SSO callback → redirects to client
    ├─ POST /auth/token                              ← exchange code / refresh token
    │
    └─ POST /mcp  ←── Bearer token ──→  MCP protocol (tools, resources, prompts)
```

The server acts as an **OAuth Authorization Server proxy**: clients register and authorize through the MCP server, which redirects to the external SSO for actual user authentication, then proxies tokens back.

## Prerequisites

- Go 1.25+
- An OAuth 2.0 / OIDC identity provider (Keycloak, Red Hat SSO, Okta, Azure AD, etc.)

## Configuration

All configuration is via environment variables.

### Required

| Variable                | Description                                                                                                  |
| ----------------------- | ------------------------------------------------------------------------------------------------------------ |
| `SSO_CLIENT_ID`         | OAuth client ID registered with your SSO provider                                                            |
| `SSO_CLIENT_SECRET`     | OAuth client secret                                                                                          |
| `SSO_AUTHORIZATION_URL` | SSO authorization endpoint (e.g. `https://sso.example.com/auth/realms/myrealm/protocol/openid-connect/auth`) |
| `SSO_TOKEN_URL`         | SSO token endpoint (e.g. `https://sso.example.com/auth/realms/myrealm/protocol/openid-connect/token`)        |
| `SSO_INTROSPECTION_URL` | SSO token introspection endpoint (RFC 7662)                                                                  |
| `SSO_CALLBACK_URL`      | Callback URL pointing to this server's `/auth/callback/oidc` endpoint                                        |
| `EMBEDDING_ENDPOINT`   | URL of the embedding service (e.g. `https://host/v1/embeddings`)                                             |
| `EMBEDDING_API_KEY`    | API key for the embedding service                                                                            |
| `EMBEDDING_MODEL_NAME` | Model name to use for generating embeddings                                                                  |
| `SNOWFLAKE_ACCOUNT`    | Snowflake account identifier                                                                                 |

### Optional

| Variable              | Default                              | Description                                             |
| --------------------- | ------------------------------------ | ------------------------------------------------------- |
| `MCP_SERVER_PORT`     | `8080`                               | Port the server listens on                              |
| `PIPELINE_NAMESPACE`  | `unstructured-controller-namespace`  | Kubernetes namespace to list pipelines from              |
| `KUBECONFIG`          | `~/.kube/config`                     | Path to kubeconfig file (only used outside a cluster)   |

The Snowflake connection uses the `PUBLIC` role by default to avoid defaulting to an overprivileged role.

### Example `.env`

```bash
SSO_CLIENT_ID=mcp-server
SSO_CLIENT_SECRET=your-client-secret
SSO_AUTHORIZATION_URL=https://sso.example.com/auth/realms/myrealm/protocol/openid-connect/auth
SSO_TOKEN_URL=https://sso.example.com/auth/realms/myrealm/protocol/openid-connect/token
SSO_INTROSPECTION_URL=https://sso.example.com/auth/realms/myrealm/protocol/openid-connect/token/introspect
SSO_CALLBACK_URL=http://localhost:8080/auth/callback/oidc
SNOWFLAKE_ACCOUNT=your-account
EMBEDDING_ENDPOINT=https://your-embedding-service/v1/embeddings
EMBEDDING_API_KEY=your-api-key
EMBEDDING_MODEL_NAME=your-model-name
```

## Running

### In-cluster

When deployed inside Kubernetes, the server automatically uses the pod's service account for cluster access. No `KUBECONFIG` is needed.

### Local development

The server detects it is running outside a cluster and falls back to your local kubeconfig (`KUBECONFIG` env var or `~/.kube/config`). Point your context at a Kind or Minikube cluster with the CRDs installed.

```bash
# Build
go build -o mcp-server ./cmd/mcp-server/

# Run (ensure .env is sourced or variables are exported)
./mcp-server
```

The server starts on `:8080` (or `MCP_SERVER_PORT`) and logs JSON to stdout.

## Client Configuration

### Cursor

Add to `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "unstructured-data-controller": {
      "url": "http://localhost:8080/mcp/"
    }
  }
}
```

Cursor will automatically discover the OAuth flow via the well-known endpoints, prompt you to log in via your SSO provider, and manage tokens.

### Claude Code

Add to your Claude Code MCP settings:

```json
{
  "mcpServers": {
    "unstructured-data-controller": {
      "url": "http://localhost:8080/mcp/"
    }
  }
}
```

### Static Token (any client)

If your client does not support OAuth discovery, you can manually obtain a token and pass it as a header:

```json
{
  "mcpServers": {
    "unstructured-data-controller": {
      "url": "http://localhost:8080/mcp/",
      "headers": {
        "Authorization": "Bearer <your-access-token>"
      }
    }
  }
}
```

## OAuth Flow

The full authorization flow follows the [MCP Authorization specification (2025-11-25)](https://modelcontextprotocol.io/specification/2025-11-25):

1. **Client → `GET /.well-known/oauth-protected-resource`** — discovers that auth is required and which authorization server to use (RFC 9728)
2. **Client → `GET /.well-known/oauth-authorization-server`** — discovers available endpoints: authorize, token, register (RFC 8414)
3. **Client → `POST /auth/register`** — dynamically registers itself, receives `client_id` and `client_secret` (RFC 7591)
4. **Client → `GET /auth/authorize`** — starts authorization code flow with PKCE (S256)
5. **Server → redirects to SSO** — user authenticates with the external identity provider
6. **SSO → `GET /auth/callback/oidc`** — server receives SSO callback, exchanges code for token
7. **Server → redirects to client** — passes authorization code back to client
8. **Client → `POST /auth/token`** — exchanges authorization code + PKCE verifier for access token
9. **Client → `POST /mcp`** — uses Bearer token for all subsequent MCP requests
10. **Token refresh** — client sends `grant_type=refresh_token` to `/auth/token` when the access token expires

## Endpoints

| Method                  | Path                                      | Auth         | Description                              |
| ----------------------- | ----------------------------------------- | ------------ | ---------------------------------------- |
| `POST`, `GET`, `DELETE` | `/mcp`                                    | Bearer token | MCP Streamable HTTP protocol             |
| `GET`                   | `/.well-known/oauth-protected-resource`   | None         | Protected resource metadata (RFC 9728)   |
| `GET`                   | `/.well-known/oauth-authorization-server` | None         | Authorization server metadata (RFC 8414) |
| `POST`                  | `/auth/register`                          | None         | Dynamic client registration (RFC 7591)   |
| `GET`                   | `/auth/authorize`                         | None         | Authorization endpoint (RFC 6749)        |
| `GET`                   | `/auth/callback/oidc`                     | None         | SSO callback handler                     |
| `POST`                  | `/auth/token`                             | None         | Token endpoint (code exchange, refresh)  |
| `GET`                   | `/healthz`                                | None         | Liveness probe                           |
| `GET`                   | `/readyz`                                 | None         | Readiness probe                          |

## Package Structure

```
cmd/mcp-server/
  main.go                  ← entry point, wiring

pkg/auth/
  provider.go              ← Provider interface (extensible)
  provider_generic.go      ← Generic OIDC provider (Keycloak, Okta, Azure AD, etc.)
  oauth.go                 ← HTTP middleware (token validation + caching), metadata handlers
  server.go                ← OAuth AS proxy endpoints (register, authorize, callback, token)
  store.go                 ← In-memory OAuth client and authorization code storage
  pkce.go                  ← PKCE S256 validation (RFC 7636)
```

## Adding a New OAuth Provider

The auth package is extensible via the `Provider` interface. To add a new provider (e.g., Google):

1. Create `pkg/auth/provider_google.go`
2. Implement the four methods:

```go
type GoogleProvider struct { ... }

func (p *GoogleProvider) BuildAuthURL(callbackURL, state string) (string, error) { ... }
func (p *GoogleProvider) ExchangeCode(ctx context.Context, code, callbackURL string) (*ExternalToken, error) { ... }
func (p *GoogleProvider) RefreshToken(ctx context.Context, refreshToken string) (*ExternalToken, error) { ... }
func (p *GoogleProvider) IntrospectToken(ctx context.Context, token string) (*IntrospectionResponse, error) { ... }
```

3. Select the provider in `main.go` based on an environment variable (e.g., `OAUTH_PROVIDER=google`)

No changes needed to the middleware, OAuth server endpoints, or MCP layer.

## SSO Provider Setup

When registering the MCP server as a client in your SSO provider, configure:

- **Valid redirect URI**: Your `SSO_CALLBACK_URL` value (e.g., `http://localhost:8080/auth/callback/oidc`)
- **Client authentication**: Client ID and Secret (confidential client)
- **Grant types**: Authorization Code, Refresh Token
- **Scopes**: `openid` (minimum)
