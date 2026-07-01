-- Convert SAR internal_validation from free-text to a boolean flag.
-- Guarded so it is a no-op on a fresh DB (table not yet created by
-- EnsureSchema) and idempotent on re-run (already boolean).
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'ent_sar_tracking'
      AND column_name = 'internal_validation'
      AND data_type <> 'boolean'
  ) THEN
    ALTER TABLE ent_sar_tracking
      ALTER COLUMN internal_validation DROP DEFAULT,
      ALTER COLUMN internal_validation TYPE boolean
        USING (internal_validation IS NOT NULL AND internal_validation <> ''),
      ALTER COLUMN internal_validation SET DEFAULT false;
  END IF;
END $$;
