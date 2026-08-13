ALTER TABLE change_sets
    ADD COLUMN IF NOT EXISTS applied_version bigint;
