-- Contacts carry their account's buying entity so a Lead can offer the
-- contacts of its company (kept in step on account saves from now on).
UPDATE ent_contacts c SET buying_entity_id = a.buying_entity_id
FROM ent_accounts a
WHERE a.tenant_id = c.tenant_id AND a.id::text = c.account_id
  AND c.buying_entity_id IS DISTINCT FROM a.buying_entity_id;
