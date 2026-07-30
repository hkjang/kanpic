CREATE TABLE IF NOT EXISTS import_jobs (
    id uuid PRIMARY KEY,
    actor_id text NOT NULL,
    idempotency_key text NOT NULL,
    file_name text NOT NULL,
    format text NOT NULL,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    cell_count bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS import_jobs_workbook_idx ON import_jobs(workbook_id);

CREATE TABLE IF NOT EXISTS export_jobs (
    id uuid PRIMARY KEY,
    actor_id text NOT NULL,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    format text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
