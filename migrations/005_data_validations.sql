CREATE TABLE IF NOT EXISTS data_validations (
    id uuid PRIMARY KEY,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    cell_range text NOT NULL,
    rule_type text NOT NULL,
    operator text NOT NULL,
    options jsonb NOT NULL DEFAULT '[]'::jsonb,
    value jsonb,
    value2 jsonb,
    formula text NOT NULL DEFAULT '',
    allow_blank boolean NOT NULL DEFAULT true,
    reject_input boolean NOT NULL DEFAULT true,
    show_dropdown boolean NOT NULL DEFAULT true,
    display_style text NOT NULL DEFAULT 'chip',
    help_text text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (sheet_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS data_validations_sheet_range_idx
    ON data_validations (sheet_id, updated_at DESC, id);
