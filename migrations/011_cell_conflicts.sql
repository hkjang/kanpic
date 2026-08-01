CREATE TABLE IF NOT EXISTS cell_conflicts (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL REFERENCES cell_operations(operation_id) ON DELETE CASCADE,
    row_number integer NOT NULL CHECK (row_number > 0),
    column_number integer NOT NULL CHECK (column_number > 0),
    base_version bigint NOT NULL,
    changed_at_version bigint NOT NULL,
    server_version bigint NOT NULL,
    actor_id text NOT NULL,
    client_id text NOT NULL DEFAULT '',
    conflicting_actor_id text NOT NULL DEFAULT '',
    base_cell jsonb NOT NULL DEFAULT '{}'::jsonb,
    conflicting_cell jsonb NOT NULL DEFAULT '{}'::jsonb,
    submitted_cell jsonb NOT NULL DEFAULT '{}'::jsonb,
    applied_cell jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
    resolution text NOT NULL DEFAULT '' CHECK (resolution IN ('','keep_current','restore_previous')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    resolved_by text NOT NULL DEFAULT '',
    resolution_operation_id uuid REFERENCES cell_operations(operation_id) ON DELETE SET NULL,
    resolution_server_version bigint NOT NULL DEFAULT 0,
    resolved_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (operation_id, row_number, column_number)
);

CREATE INDEX IF NOT EXISTS cell_conflicts_workbook_status_idx
    ON cell_conflicts (workbook_id, status, created_at DESC, id);

CREATE INDEX IF NOT EXISTS cell_conflicts_sheet_coordinate_idx
    ON cell_conflicts (sheet_id, row_number, column_number, created_at DESC);
