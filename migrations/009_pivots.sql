CREATE TABLE IF NOT EXISTS pivots (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    source_sheet_id uuid REFERENCES sheets(id) ON DELETE SET NULL,
    idempotency_key text NOT NULL,
    name text NOT NULL,
    source_range text NOT NULL,
    first_row_headers boolean NOT NULL DEFAULT true,
    row_dimensions jsonb NOT NULL DEFAULT '[]'::jsonb,
    column_dimensions jsonb NOT NULL DEFAULT '[]'::jsonb,
    value_fields jsonb NOT NULL DEFAULT '[]'::jsonb,
    filters jsonb NOT NULL DEFAULT '[]'::jsonb,
    calculated_fields jsonb NOT NULL DEFAULT '[]'::jsonb,
    refresh_mode text NOT NULL DEFAULT 'auto' CHECK (refresh_mode IN ('auto','manual')),
    source_version bigint NOT NULL DEFAULT 0,
    cached_result jsonb,
    refreshed_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workbook_id, created_by, idempotency_key)
);

CREATE INDEX IF NOT EXISTS pivots_workbook_sheet_idx ON pivots(workbook_id, sheet_id, created_at, id);
CREATE INDEX IF NOT EXISTS pivots_source_sheet_idx ON pivots(source_sheet_id) WHERE source_sheet_id IS NOT NULL;
