-- Sales Orders were retired: quotes now convert straight into service
-- agreements. Agreements created from a sales order keep the order id (as
-- legacy sales_order_id) and get a direct link to the order's quote and that
-- quote's opportunity, so the chain stays navigable. The ent_sales_orders /
-- ent_order_lines tables are left in place, untouched. No-op when they never
-- existed (fresh databases).
DO $$
BEGIN
  IF to_regclass('ent_sales_orders') IS NOT NULL THEN
    UPDATE ent_invoices i SET quote_id = so.quote_id
    FROM ent_sales_orders so
    WHERE so.tenant_id = i.tenant_id AND so.id::text = i.sales_order_id
      AND (i.quote_id IS NULL OR i.quote_id = '') AND so.quote_id IS NOT NULL;
  END IF;
END $$;

UPDATE ent_invoices i SET opportunity_id = q.opportunity_id
FROM ent_quotes q
WHERE q.tenant_id = i.tenant_id AND q.id::text = i.quote_id
  AND (i.opportunity_id IS NULL OR i.opportunity_id = '') AND q.opportunity_id IS NOT NULL;

-- Quotes show the status of their latest agreement.
UPDATE ent_quotes q SET agreement_status = (
  SELECT a.status FROM ent_invoices a WHERE a.tenant_id = q.tenant_id AND a.quote_id = q.id::text
  ORDER BY a.created_at DESC LIMIT 1);

-- Agreements without a schedule start as "Unscheduled".
UPDATE ent_invoices SET payment_status = 'Unscheduled' WHERE payment_status IS NULL;
