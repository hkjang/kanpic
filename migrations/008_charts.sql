CREATE TABLE IF NOT EXISTS charts (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    source_sheet_id uuid REFERENCES sheets(id) ON DELETE SET NULL,
    idempotency_key text NOT NULL,
    chart_type text NOT NULL CHECK (chart_type IN ('bar','line','area','pie','scatter','histogram')),
    title text NOT NULL DEFAULT '',
    source_range text NOT NULL,
    first_row_headers boolean NOT NULL DEFAULT true,
    first_column_labels boolean NOT NULL DEFAULT true,
    legend_position text NOT NULL DEFAULT 'right' CHECK (legend_position IN ('none','top','right','bottom','left')),
    x_axis_title text NOT NULL DEFAULT '',
    y_axis_title text NOT NULL DEFAULT '',
    position_x integer NOT NULL DEFAULT 24 CHECK (position_x >= 0),
    position_y integer NOT NULL DEFAULT 24 CHECK (position_y >= 0),
    width integer NOT NULL DEFAULT 560 CHECK (width BETWEEN 240 AND 1600),
    height integer NOT NULL DEFAULT 320 CHECK (height BETWEEN 160 AND 1200),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workbook_id, created_by, idempotency_key)
);

CREATE INDEX IF NOT EXISTS charts_workbook_sheet_idx ON charts(workbook_id, sheet_id, created_at, id);
CREATE INDEX IF NOT EXISTS charts_source_sheet_idx ON charts(source_sheet_id) WHERE source_sheet_id IS NOT NULL;
