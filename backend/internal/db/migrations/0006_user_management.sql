-- User management: invite section access + lifecycle tracking, last-login
-- tracking, and one-time password reset links. Purely additive: every new
-- column is NULL (or a zero default) so existing rows keep today's behaviour —
-- in particular existing invites keep expires_at NULL (never expire) and
-- allowed_groups NULL (full access, as before).

-- When the user last signed in (NULL = never / before tracking existed).
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;

-- Section access granted on acceptance (NULL = full access, mirrors
-- memberships.allowed_groups).
ALTER TABLE invites ADD COLUMN IF NOT EXISTS allowed_groups TEXT[];
-- Lifecycle. The application sets expires_at (14 days) on new invites; there
-- is deliberately no column default so existing rows are not backfilled.
ALTER TABLE invites ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
ALTER TABLE invites ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;
-- Failed acceptance attempts ("failed account creation requests").
ALTER TABLE invites ADD COLUMN IF NOT EXISTS last_error TEXT;
ALTER TABLE invites ADD COLUMN IF NOT EXISTS failed_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE invites ADD COLUMN IF NOT EXISTS last_attempt_at TIMESTAMPTZ;

-- One-time password reset links issued by a tenant admin. Only the SHA-256 of
-- the token is stored; the plaintext is returned once to the admin.
CREATE TABLE IF NOT EXISTS password_resets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_password_resets_user ON password_resets(tenant_id, user_id);
