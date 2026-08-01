CREATE TABLE IF NOT EXISTS conditional_formats (
    id uuid PRIMARY KEY,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    name text NOT NULL,
    cell_range text NOT NULL,
    rule_type text NOT NULL CHECK (rule_type IN ('value','duplicate','color_scale','data_bar')),
    operator text NOT NULL DEFAULT '',
    value jsonb,
    value2 jsonb,
    style jsonb,
    min_color text NOT NULL DEFAULT '',
    mid_color text NOT NULL DEFAULT '',
    max_color text NOT NULL DEFAULT '',
    bar_color text NOT NULL DEFAULT '',
    priority integer NOT NULL DEFAULT 1 CHECK (priority BETWEEN 1 AND 1000),
    stop_if_true boolean NOT NULL DEFAULT false,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (sheet_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS conditional_formats_sheet_priority_idx
    ON conditional_formats (sheet_id, priority, created_at, id);
