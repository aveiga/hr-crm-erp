-- Legal Entities now register customer/supplier companies too, with an
-- entity_type; seller and employer pickers only offer "Own" entities. Every
-- entity that existed before was one of ours, except contracting companies of
-- contractors that are not otherwise used as a seller/employer: those are
-- suppliers. Runs after EnsureSchema added the column.
UPDATE ent_legal_entities le SET entity_type = 'Supplier'
WHERE le.entity_type IS NULL
  AND EXISTS (SELECT 1 FROM ent_people p WHERE p.tenant_id = le.tenant_id AND p.contracting_company_id = le.id::text)
  AND NOT EXISTS (SELECT 1 FROM ent_people p WHERE p.tenant_id = le.tenant_id AND p.legal_employer_id = le.id::text)
  AND NOT EXISTS (SELECT 1 FROM ent_accounts a WHERE a.tenant_id = le.tenant_id AND a.legal_entity_id = le.id::text)
  AND NOT EXISTS (SELECT 1 FROM ent_quotes q WHERE q.tenant_id = le.tenant_id AND q.legal_entity_id = le.id::text)
  AND NOT EXISTS (SELECT 1 FROM ent_invoices i WHERE i.tenant_id = le.tenant_id AND i.legal_entity_id = le.id::text);

UPDATE ent_legal_entities SET entity_type = 'Own' WHERE entity_type IS NULL;
