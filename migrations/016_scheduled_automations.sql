ALTER TABLE automations
    ADD COLUMN IF NOT EXISTS next_run_at timestamptz;

ALTER TABLE automation_runs
    ADD COLUMN IF NOT EXISTS scheduled_for timestamptz;

ALTER TABLE automation_runs
    DROP CONSTRAINT IF EXISTS automation_runs_trigger_type_check;

ALTER TABLE automation_runs
    ADD CONSTRAINT automation_runs_trigger_type_check
    CHECK (trigger_type IN ('manual','cell_change','schedule'));

ALTER TABLE automation_runs
    DROP CONSTRAINT IF EXISTS automation_runs_status_check;

ALTER TABLE automation_runs
    ADD CONSTRAINT automation_runs_status_check
    CHECK (status IN ('running','succeeded','skipped','failed','undoing','undone'));

CREATE INDEX IF NOT EXISTS automations_schedule_due_idx
    ON automations (next_run_at, id)
    WHERE enabled AND deleted_at IS NULL AND next_run_at IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS automation_runs_schedule_unique
    ON automation_runs (automation_id, scheduled_for)
    WHERE scheduled_for IS NOT NULL;
