-- AgentHub MCP Client Runtime — Tenant Schema (ah_{tenantId})
-- Stores per-tenant MCP server configurations.
-- No tenant_id column — isolation is via schema.

CREATE TABLE IF NOT EXISTS mcp_server_config (
    id                  UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    name                VARCHAR(200) NOT NULL UNIQUE,
    transport_type      VARCHAR(20)  NOT NULL,           -- stdio | http
    http_base_url       TEXT,
    command             TEXT,
    args                JSONB,
    env                 JSONB,
    oauth_token_url     TEXT,
    oauth_client_id     VARCHAR(200),
    oauth_client_secret TEXT,                            -- plain text; use Vault in prod
    oauth_scopes        JSONB,
    auto_start          BOOLEAN     NOT NULL DEFAULT FALSE,
    enabled             BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mcp_server_config_name      ON mcp_server_config (name);
CREATE INDEX IF NOT EXISTS idx_mcp_server_config_enabled   ON mcp_server_config (enabled);
CREATE INDEX IF NOT EXISTS idx_mcp_server_config_auto_start ON mcp_server_config (auto_start);
