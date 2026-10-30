-- Client secrets of the service principals that read Azure Cost Management,
-- one per Azure tenant record (ent_azure_tenants). Stored AES-GCM encrypted
-- with the server's SECRETS_KEY and never returned by the API.
CREATE TABLE IF NOT EXISTS azure_credentials (
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    azure_tenant_id UUID NOT NULL,
    secret          BYTEA NOT NULL,
    expires_on      DATE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, azure_tenant_id)
);
