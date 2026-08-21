-- A protected range restricts who may write to part of a sheet. Sharing
-- decides who can open a workbook; this decides who can change the cells that
-- a model depends on.
CREATE TABLE IF NOT EXISTS protected_ranges (
    id uuid PRIMARY KEY,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    cell_range text NOT NULL,
    description text NOT NULL DEFAULT '',
    editors jsonb NOT NULL DEFAULT '[]'::jsonb,
    warning_only boolean NOT NULL DEFAULT false,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (sheet_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS protected_ranges_sheet_idx
    ON protected_ranges (sheet_id, updated_at DESC, id);
