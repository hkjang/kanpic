CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS organizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspaces (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id),
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id text UNIQUE,
    email text UNIQUE,
    display_name text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workbooks (
    id uuid PRIMARY KEY,
    workspace_id text NOT NULL DEFAULT '',
    title text NOT NULL,
    owner_id text NOT NULL DEFAULT '',
    favorite boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS workbooks_workspace_updated_idx ON workbooks (workspace_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS sheets (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    name text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    properties jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL,
    UNIQUE (workbook_id, name)
);

-- A payload contains only non-empty cells in a 64x64 tile. This keeps the row
-- count bounded while allowing individual visible tiles to be loaded lazily.
CREATE TABLE IF NOT EXISTS cell_blocks (
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    block_row integer NOT NULL,
    block_column integer NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sheet_id, block_row, block_column)
);

CREATE TABLE IF NOT EXISTS cell_operations (
    operation_id uuid PRIMARY KEY,
    idempotency_key text NOT NULL,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid REFERENCES sheets(id) ON DELETE SET NULL,
    actor_id text NOT NULL,
    client_id text NOT NULL DEFAULT '',
    base_version bigint NOT NULL,
    server_version bigint NOT NULL,
    operation_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (workbook_id, actor_id, idempotency_key),
    UNIQUE (workbook_id, server_version)
);

CREATE INDEX IF NOT EXISTS cell_operations_replay_idx ON cell_operations (workbook_id, server_version);

CREATE TABLE IF NOT EXISTS workbook_versions (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    workbook_version bigint NOT NULL,
    name text NOT NULL DEFAULT '',
    actor_id text NOT NULL,
    snapshot jsonb NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS workbook_versions_list_idx ON workbook_versions (workbook_id, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    result text NOT NULL,
    trace_id text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
