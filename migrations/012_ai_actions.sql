CREATE TABLE IF NOT EXISTS ai_actions (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    client_id text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('formula','explain','fix')),
    selected_range text NOT NULL,
    request text NOT NULL,
    status text NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned','completed','applying','applied','undoing','undone','failed')),
    base_version bigint NOT NULL CHECK (base_version > 0),
    model text NOT NULL,
    summary text NOT NULL DEFAULT '',
    explanation text NOT NULL DEFAULT '',
    changes jsonb NOT NULL DEFAULT '[]'::jsonb,
    input_cell_count integer NOT NULL DEFAULT 0 CHECK (input_cell_count >= 0),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    approval_idempotency_key text NOT NULL DEFAULT '',
    operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    operation_result jsonb,
    undo_idempotency_key text NOT NULL DEFAULT '',
    undo_operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    undo_result jsonb,
    error_message text NOT NULL DEFAULT '',
    approved_at timestamptz,
    undone_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (actor_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS ai_actions_actor_created_idx
    ON ai_actions (actor_id, created_at DESC, id);

CREATE INDEX IF NOT EXISTS ai_actions_workbook_created_idx
    ON ai_actions (workbook_id, created_at DESC, id);

CREATE TABLE IF NOT EXISTS ai_action_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action_id uuid NOT NULL REFERENCES ai_actions(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    event_type text NOT NULL,
    model text NOT NULL DEFAULT '',
    tool_name text NOT NULL DEFAULT '',
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ai_action_events_action_idx
    ON ai_action_events (action_id, created_at, id);
