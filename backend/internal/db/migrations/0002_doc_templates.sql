-- Per-tenant uploadable Word templates for Offers and IGTRs. When none is
-- selected, the app falls back to the template embedded in the binary.
CREATE TABLE IF NOT EXISTS doc_templates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,          -- 'offer' | 'igtr'
    name       TEXT NOT NULL,
    filename   TEXT NOT NULL,
    content    BYTEA NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_doc_templates_tenant ON doc_templates(tenant_id, kind);
