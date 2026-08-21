-- A protection may now cover a whole sheet, with named ranges left editable so
-- an input form stays usable while the rest of the sheet is locked.
ALTER TABLE protected_ranges ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT 'range';
ALTER TABLE protected_ranges ADD COLUMN IF NOT EXISTS exceptions jsonb NOT NULL DEFAULT '[]'::jsonb;

DO $$
DECLARE constraint_name text;
BEGIN
  SELECT conname INTO constraint_name FROM pg_constraint
   WHERE conrelid='protected_ranges'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%scope%';
  IF constraint_name IS NULL THEN
    ALTER TABLE protected_ranges ADD CONSTRAINT protected_ranges_scope_check CHECK (scope IN ('range','sheet'));
  END IF;
END $$;
