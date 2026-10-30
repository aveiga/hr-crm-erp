-- Azure usage is read per subscription and date range (sync, invoices,
-- dashboard); the generic tenant_id index alone doesn't cover that.
CREATE INDEX IF NOT EXISTS ent_azure_usage_sub_date
    ON ent_azure_usage (tenant_id, azure_subscription_id, usage_date);
