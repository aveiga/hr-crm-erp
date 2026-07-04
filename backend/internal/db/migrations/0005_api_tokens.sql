-- Per-agent API tokens: long-lived, revocable credentials that let external
-- agents (e.g. Claude Code / Copilot CLI via the MCP endpoint) authenticate
-- without a user's short-lived login JWT. A token is bound to one membership
-- (tenant + user); the middleware re-checks that membership live on every
-- request, so the token grants exactly the member's current access. Only the
-- SHA-256 hash of the token is stored — the plaintext is shown once on creation.
CREATE TABLE IF NOT EXISTS api_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    read_only    BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

-- Lookup by hash on every authenticated request.
CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash);
-- List a member's own tokens.
CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(tenant_id, user_id);
