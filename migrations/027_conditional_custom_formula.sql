-- A conditional format may now be driven by a formula written relative to the
-- top-left cell of its range, the way Sheets writes a custom rule.
ALTER TABLE conditional_formats ADD COLUMN IF NOT EXISTS formula text NOT NULL DEFAULT '';

DO $$
DECLARE constraint_name text;
BEGIN
  SELECT conname INTO constraint_name FROM pg_constraint
   WHERE conrelid='conditional_formats'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%rule_type%';
  IF constraint_name IS NOT NULL THEN
    EXECUTE format('ALTER TABLE conditional_formats DROP CONSTRAINT %I', constraint_name);
  END IF;
END $$;

ALTER TABLE conditional_formats
  ADD CONSTRAINT conditional_formats_rule_type_check
  CHECK (rule_type IN ('value','duplicate','color_scale','data_bar','custom_formula'));
