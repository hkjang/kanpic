ALTER TABLE automation_runs
    ADD COLUMN IF NOT EXISTS counts_toward_rate boolean NOT NULL DEFAULT true;

CREATE INDEX IF NOT EXISTS automation_runs_rate_window_idx
    ON automation_runs (workbook_id, started_at DESC)
    WHERE counts_toward_rate;
