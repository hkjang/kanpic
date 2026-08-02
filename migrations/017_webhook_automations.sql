ALTER TABLE automation_runs
    ADD COLUMN IF NOT EXISTS trigger_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL;

ALTER TABLE automation_runs
    ADD COLUMN IF NOT EXISTS payload_digest text NOT NULL DEFAULT '';

ALTER TABLE automation_runs
    ADD COLUMN IF NOT EXISTS payload_bytes integer NOT NULL DEFAULT 0;

ALTER TABLE automation_runs
    DROP CONSTRAINT IF EXISTS automation_runs_payload_bytes_check;

ALTER TABLE automation_runs
    ADD CONSTRAINT automation_runs_payload_bytes_check
    CHECK (payload_bytes >= 0 AND payload_bytes <= 1048576);

ALTER TABLE automation_runs
    DROP CONSTRAINT IF EXISTS automation_runs_trigger_type_check;

ALTER TABLE automation_runs
    ADD CONSTRAINT automation_runs_trigger_type_check
    CHECK (trigger_type IN ('manual','cell_change','schedule','webhook'));

CREATE INDEX IF NOT EXISTS automation_runs_trigger_key_idx
    ON automation_runs (trigger_key_id, started_at DESC)
    WHERE trigger_key_id IS NOT NULL;
