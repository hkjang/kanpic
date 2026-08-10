CREATE TABLE IF NOT EXISTS mail_deliveries (
    id uuid PRIMARY KEY,
    event text NOT NULL,
    recipient text NOT NULL,
    subject text NOT NULL,
    workbook_id uuid REFERENCES workbooks(id) ON DELETE SET NULL,
    actor_id text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','sent','failed','skipped')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    error_message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS mail_deliveries_created_idx
    ON mail_deliveries (created_at DESC, id);

CREATE INDEX IF NOT EXISTS mail_deliveries_status_idx
    ON mail_deliveries (status, created_at DESC);
