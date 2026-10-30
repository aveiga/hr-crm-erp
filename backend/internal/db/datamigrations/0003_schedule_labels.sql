-- Installment labels ("31-03-2026 · 1000.00 · Q1") were computed on save but
-- never stored, so the Scheduled Payment picker on payments showed raw ids.
-- Backfill them in the same format as resource.scheduleLabel.
UPDATE ent_payment_schedule
SET label = concat_ws(' · ',
  to_char(due_date, 'DD-MM-YYYY'),
  to_char(round(COALESCE(amount, 0)::numeric, 2), 'FM999999999999990.00'),
  NULLIF(trim(description), ''))
WHERE label IS NULL OR label = '';
