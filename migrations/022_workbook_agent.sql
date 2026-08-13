CREATE TABLE IF NOT EXISTS ai_conversations (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    title text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ai_conversations_workbook_actor_idx
    ON ai_conversations (workbook_id, actor_id, updated_at DESC);

ALTER TABLE ai_actions
    DROP CONSTRAINT IF EXISTS ai_actions_mode_check;

ALTER TABLE ai_actions
    ADD CONSTRAINT ai_actions_mode_check
    CHECK (mode IN ('formula','explain','fix','summarize','anomaly','clean','format','chart','agent'));

ALTER TABLE ai_actions
    DROP CONSTRAINT IF EXISTS ai_actions_status_check;

ALTER TABLE ai_actions
    ADD CONSTRAINT ai_actions_status_check
    CHECK (status IN ('planned','completed','applying','applied','undoing','undone','failed','cancelled'));

ALTER TABLE ai_actions
    ADD COLUMN IF NOT EXISTS conversation_id uuid REFERENCES ai_conversations(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS risk text NOT NULL DEFAULT 'READ',
    ADD COLUMN IF NOT EXISTS plan jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS tool_calls jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS validation jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS agent_runs (
    id uuid PRIMARY KEY REFERENCES ai_actions(id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    selected_range text NOT NULL,
    intent text NOT NULL,
    state text NOT NULL,
    goal text NOT NULL DEFAULT '',
    risk text NOT NULL,
    context jsonb NOT NULL DEFAULT '{}'::jsonb,
    validation jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS agent_runs_conversation_idx
    ON agent_runs (conversation_id, started_at, id);

CREATE INDEX IF NOT EXISTS agent_runs_workbook_actor_idx
    ON agent_runs (workbook_id, actor_id, started_at DESC);

CREATE TABLE IF NOT EXISTS ai_messages (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    agent_run_id uuid REFERENCES agent_runs(id) ON DELETE SET NULL,
    actor_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('user','assistant')),
    content text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ai_messages_conversation_idx
    ON ai_messages (conversation_id, created_at, id);

CREATE TABLE IF NOT EXISTS agent_plans (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
    goal text NOT NULL,
    risk text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_steps (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES agent_plans(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position > 0),
    tool_name text NOT NULL,
    description text NOT NULL,
    status text NOT NULL,
    risk text NOT NULL,
    arguments jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, position)
);

CREATE TABLE IF NOT EXISTS agent_tool_calls (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_id uuid REFERENCES agent_steps(id) ON DELETE SET NULL,
    tool_name text NOT NULL,
    arguments jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL,
    duration_ms bigint NOT NULL DEFAULT 0,
    idempotency_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS agent_tool_calls_run_idx
    ON agent_tool_calls (run_id, created_at, id);

CREATE TABLE IF NOT EXISTS workbook_contexts (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid REFERENCES sheets(id) ON DELETE CASCADE,
    workbook_version bigint NOT NULL,
    selected_range text NOT NULL DEFAULT '',
    context jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS workbook_contexts_workbook_idx
    ON workbook_contexts (workbook_id, workbook_version DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS workbook_memories (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid REFERENCES sheets(id) ON DELETE CASCADE,
    scope text NOT NULL,
    memory_key text NOT NULL,
    content text NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workbook_id, sheet_id, scope, memory_key)
);

CREATE TABLE IF NOT EXISTS workbook_glossary (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    term text NOT NULL,
    object_reference text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workbook_id, term)
);

CREATE TABLE IF NOT EXISTS change_sets (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    status text NOT NULL,
    risk text NOT NULL,
    base_version bigint NOT NULL,
    operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    undo_operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    applied_at timestamptz,
    rolled_back_at timestamptz
);

CREATE INDEX IF NOT EXISTS change_sets_workbook_idx
    ON change_sets (workbook_id, created_at DESC);

CREATE TABLE IF NOT EXISTS change_operations (
    id uuid PRIMARY KEY,
    change_set_id uuid NOT NULL REFERENCES change_sets(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position > 0),
    operation_type text NOT NULL,
    sheet_id uuid REFERENCES sheets(id) ON DELETE SET NULL,
    selected_range text NOT NULL DEFAULT '',
    before_value jsonb,
    after_value jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (change_set_id, position)
);

CREATE TABLE IF NOT EXISTS agent_audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS agent_audit_logs_run_idx
    ON agent_audit_logs (run_id, created_at, id);
