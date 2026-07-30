CREATE TABLE IF NOT EXISTS filter_views (
    id uuid PRIMARY KEY,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    idempotency_key text NOT NULL,
    name text NOT NULL,
    cell_range text NOT NULL,
    header_rows integer NOT NULL DEFAULT 1 CHECK (header_rows >= 0),
    criteria jsonb NOT NULL DEFAULT '[]'::jsonb,
    active boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS filter_views_actor_name_unique
    ON filter_views (sheet_id, actor_id, lower(name));

CREATE UNIQUE INDEX IF NOT EXISTS filter_views_actor_idempotency_unique
    ON filter_views (sheet_id, actor_id, idempotency_key);

CREATE UNIQUE INDEX IF NOT EXISTS filter_views_one_active_per_actor
    ON filter_views (sheet_id, actor_id) WHERE active;

CREATE INDEX IF NOT EXISTS filter_views_actor_list
    ON filter_views (sheet_id, actor_id, active DESC, updated_at DESC);
