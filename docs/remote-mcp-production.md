[← Back to README](../README.md)

# Remote MCP Production Guide

## Audit (2026-05-20)

### Current transports
- `stdio`: supported via `ohara mcp` (local/trusted, unchanged).
- `streamable-http`: supported at `/mcp` when remote mode is enabled.
- `sse`: supported at `/mcp/sse` with message endpoint `/mcp/message` when transport is `sse`.
- `http`: accepted as alias for streamable HTTP.

### MCP compatibility status
- Remote MCP endpoint is MCP JSON-RPC compatible for `initialize`, `tools/list`, and `tools/call`.
- Streamable HTTP is stateless in Ohara’s remote configuration for compatibility with modern MCP clients.
- Health and readiness endpoints are available at `GET /health` and `GET /ready`.

### Authentication and authorization
- Remote auth modes:
  - `bearer`: implemented.
  - `off`: supported for local/dev opt-out.
  - `oauth`: **not implemented** (startup returns explicit error).
- Bearer token sources:
  - `OHARA_MCP_BEARER_TOKEN_FILE` (preferred)
  - `OHARA_MCP_BEARER_TOKEN`
- Token comparison is constant-time (`crypto/subtle.ConstantTimeCompare`).
- Missing/invalid token returns `401` with generic error text.
- `/health` and `/ready` bypass auth intentionally.
- `off` mode is unsafe for public exposure and should only be used for tightly controlled local/private readonly scenarios.

### Access-mode safety
- `readonly` mode default remote allowlist includes only:
  - `search`, `fetch`
  - `mem_search`, `mem_context`, `mem_pack`, `mem_pack_explain`, `mem_file_history`, `mem_file_context`, `mem_stats`
- Write/admin tools (for example `mem_save`, `mem_update`, `mem_delete`, jobs/mutations) are not listed in readonly mode.
- `full` mode exposes all tools and requires authenticated trusted use.

### Low-trust handling
- Readonly remote callers default to `low` trust (`OHARA_MCP_TRUST_LEVEL=low`).
- Low-trust responses apply existing redaction/filtering:
  - memory body truncation
  - `evidence_json`, `related_json`, `applies_to_json` cleared
  - trust-level visibility filtering
- `<private>...</private>` content stripping remains enforced before persistence.

## Mode Model

### 1) Local MCP stdio
- Command: `ohara mcp`
- Intended for local MCP clients (OpenCode, Claude Code, Gemini CLI, Codex).
- Not exposed over network by default.

### 2) Remote MCP readonly (recommended first)
- Intended for ChatGPT Web and low-trust integrations.
- Exposes safe read tools only.
- Auth required by default.

### 3) Remote MCP full-access
- Intended for trusted/private environments only.
- Includes write/admin tools.
- Disabled by default unless explicitly configured.

## Environment Variables

- `OHARA_MCP_TRANSPORT=stdio|http|sse|streamable-http`
- `OHARA_MCP_REMOTE_ENABLE=0|1`
- `OHARA_MCP_BIND_ADDR=127.0.0.1:7331`
- `OHARA_MCP_PUBLIC_URL=https://example.com/mcp` (reserved for external URL publishing)
- `OHARA_MCP_AUTH_MODE=off|bearer|oauth`
- `OHARA_MCP_REQUIRE_AUTH=1|0`
- `OHARA_MCP_ACCESS_MODE=readonly|full`
- `OHARA_MCP_BEARER_TOKEN_FILE=/path/to/token`
- `OHARA_MCP_BEARER_TOKEN=...`
- `OHARA_MCP_ALLOWED_ORIGINS=https://chatgpt.com,https://example.com`
- `OHARA_MCP_TRUST_LEVEL=low|trusted`

Legacy compatibility still recognized:
- `OHARA_MCP_HTTP=true` implies remote MCP enable.
- `OHARA_AUTH_ENABLED` / `OHARA_AUTH_TOKEN` are treated as legacy auth inputs.

## ChatGPT-Compatible Tools

### `search`
Use this when you need relevant memory results for a query.

Input:
- `query` (required)
- `project` (optional)
- `limit` (optional)
- `path` (optional)
- `domain` (optional)

Output:
- `structuredContent.results[]` with:
  - `id`, `title`, `url`, `text`, `project`, `kind`, `updated_at`
- `content[]` includes JSON mirror text for compatibility.

### `fetch`
Use this when you need one specific memory by ID.

Input:
- `id` (required)

Output:
- `structuredContent.item`
- `content[]` includes JSON mirror text.
- Applies low-trust filtering/redaction rules.

## Recommended ChatGPT Web Setup

1. Deploy Ohara behind HTTPS with remote MCP readonly mode first.
2. Configure bearer auth using token file/secret manager.
3. In ChatGPT Developer Mode, add your remote MCP endpoint.
4. In prompts, be explicit:
   - “Use the Ohara app search tool to find memory about X.”
   - “Use only the Ohara app. Do not use web browsing.”

## Security Guidance

- Do not expose `full` mode publicly.
- Do not expose no-auth mode publicly.
- Use HTTPS termination (reverse proxy/load balancer).
- Use long random bearer tokens and rotate regularly.
- Prefer token file or secrets manager over plain env var.
- Keep bind address local (`127.0.0.1`) and proxy externally when possible.
- Review remote call logs/audit records.

## Reverse Proxy Example (nginx)

```nginx
server {
  listen 443 ssl;
  server_name mcp.example.com;

  ssl_certificate /etc/letsencrypt/live/mcp.example.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/mcp.example.com/privkey.pem;

  client_max_body_size 1m;

  location /mcp {
    proxy_pass http://127.0.0.1:7331/mcp;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
    proxy_read_timeout 120s;
    proxy_send_timeout 120s;
  }

  location /mcp/sse {
    proxy_pass http://127.0.0.1:7331/mcp/sse;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
    proxy_buffering off;
    proxy_read_timeout 120s;
  }

  location /mcp/message {
    proxy_pass http://127.0.0.1:7331/mcp/message;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
    proxy_read_timeout 120s;
  }
}
```

## Environment Examples

### Readonly remote

```bash
export OHARA_MCP_REMOTE_ENABLE=1
export OHARA_MCP_TRANSPORT=streamable-http
export OHARA_MCP_BIND_ADDR=127.0.0.1:7331
export OHARA_MCP_AUTH_MODE=bearer
export OHARA_MCP_REQUIRE_AUTH=1
export OHARA_MCP_ACCESS_MODE=readonly
export OHARA_MCP_BEARER_TOKEN_FILE=/etc/ohara/mcp-token
export OHARA_MCP_TRUST_LEVEL=low
```

### Full local trusted

```bash
export OHARA_MCP_REMOTE_ENABLE=1
export OHARA_MCP_TRANSPORT=streamable-http
export OHARA_MCP_BIND_ADDR=127.0.0.1:7331
export OHARA_MCP_AUTH_MODE=bearer
export OHARA_MCP_REQUIRE_AUTH=1
export OHARA_MCP_ACCESS_MODE=full
export OHARA_MCP_BEARER_TOKEN_FILE=/etc/ohara/mcp-token
export OHARA_MCP_TRUST_LEVEL=trusted
```

## Transport Smoke Commands

```bash
# health + readiness
curl http://127.0.0.1:7331/health
curl http://127.0.0.1:7331/ready

# initialize
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"1.0"}}}' \
  http://127.0.0.1:7331/mcp

# list tools
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  http://127.0.0.1:7331/mcp

# search + fetch
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"auth","project":"ohara"}}}' \
  http://127.0.0.1:7331/mcp

curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fetch","arguments":{"id":1}}}' \
  http://127.0.0.1:7331/mcp
```

## ChatGPT Limitations

- ChatGPT Web requires remote MCP endpoints; it cannot attach directly to local stdio.
- This pass validated remote MCP protocol behavior locally; ChatGPT Web itself was not directly tested in automation.
- Developer Mode/custom app behavior is web-side and may require app refresh/republication after tool-surface changes.
- Read/write tool availability in ChatGPT depends on plan/workspace capabilities.
- ChatGPT Web secure auth flows commonly expect OAuth-capable integrations.

## OAuth Status

- `OHARA_MCP_AUTH_MODE=oauth` is intentionally not implemented in this pass.
- Bearer mode is production-ready for first deployment.
- OAuth/JWKS support can be added via the current `auth.Authenticator` interface without transport redesign.
- For secure ChatGPT Web deployments today, use one of:
  1. OAuth-capable proxy/front-end in front of Ohara.
  2. Future native OAuth/JWKS support in Ohara.
  3. Strictly private/protected network with readonly/no-auth only when risk is accepted.
