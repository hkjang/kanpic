CREATE TABLE IF NOT EXISTS named_ranges (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    name text NOT NULL,
    cell_range text NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workbook_id, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS named_ranges_workbook_name_unique
    ON named_ranges (workbook_id, lower(name));

CREATE INDEX IF NOT EXISTS named_ranges_workbook_list_idx
    ON named_ranges (workbook_id, lower(name), id);
