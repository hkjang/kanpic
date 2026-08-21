-- A dropdown may now take its options from a range, which is how a shared code
-- list stays in one place instead of being retyped into every rule.
ALTER TABLE data_validations ADD COLUMN IF NOT EXISTS source_range text NOT NULL DEFAULT '';
