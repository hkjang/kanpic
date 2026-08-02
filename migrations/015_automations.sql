CREATE TABLE IF NOT EXISTS automations (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    trigger_definition jsonb NOT NULL,
    action_definition jsonb NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    idempotency_key text NOT NULL,
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    UNIQUE (workbook_id, created_by, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS automations_workbook_name_unique
    ON automations (workbook_id, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS automations_workbook_updated_idx
    ON automations (workbook_id, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS automation_runs (
    id uuid PRIMARY KEY,
    automation_id uuid NOT NULL REFERENCES automations(id),
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    idempotency_key text NOT NULL,
    trigger_type text NOT NULL CHECK (trigger_type IN ('manual','cell_change')),
    trigger_operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    status text NOT NULL CHECK (status IN ('running','succeeded','failed','undoing','undone')),
    base_version bigint NOT NULL CHECK (base_version > 0),
    action_snapshot jsonb NOT NULL,
    cells_snapshot jsonb NOT NULL,
    expected_snapshot jsonb NOT NULL,
    operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    operation_result jsonb,
    undo_idempotency_key text NOT NULL DEFAULT '',
    undo_operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    undo_result jsonb,
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL,
    UNIQUE (automation_id, actor_id, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS automation_runs_trigger_operation_unique
    ON automation_runs (automation_id, trigger_operation_id)
    WHERE trigger_operation_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS automation_runs_automation_started_idx
    ON automation_runs (automation_id, started_at DESC, id);

CREATE INDEX IF NOT EXISTS automation_runs_workbook_started_idx
    ON automation_runs (workbook_id, started_at DESC, id);
