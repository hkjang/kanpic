-- Per-user, per-department and per-role workbook sharing.
--
-- Workbooks created before this migration were visible to every authenticated
-- user, so they are migrated to organization-wide editing to keep existing work
-- reachable. New workbooks default to restricted, owner-only access.

ALTER TABLE workbooks
    ADD COLUMN IF NOT EXISTS link_access text NOT NULL DEFAULT 'restricted';

ALTER TABLE workbooks
    ADD COLUMN IF NOT EXISTS link_role text NOT NULL DEFAULT 'viewer';

ALTER TABLE workbooks
    ADD COLUMN IF NOT EXISTS sharing_locked boolean NOT NULL DEFAULT false;

ALTER TABLE workbooks
    ADD COLUMN IF NOT EXISTS viewer_can_copy boolean NOT NULL DEFAULT true;

ALTER TABLE workbooks
    ADD COLUMN IF NOT EXISTS sharing_migrated_at timestamptz;

UPDATE workbooks
SET link_access = 'organization', link_role = 'editor', sharing_migrated_at = now()
WHERE sharing_migrated_at IS NULL AND link_access = 'restricted';

ALTER TABLE workbooks
    DROP CONSTRAINT IF EXISTS workbooks_link_access_check;

ALTER TABLE workbooks
    ADD CONSTRAINT workbooks_link_access_check
    CHECK (link_access IN ('restricted', 'organization', 'anyone'));

ALTER TABLE workbooks
    DROP CONSTRAINT IF EXISTS workbooks_link_role_check;

ALTER TABLE workbooks
    ADD CONSTRAINT workbooks_link_role_check
    CHECK (link_role IN ('viewer', 'commenter', 'editor'));

CREATE INDEX IF NOT EXISTS workbooks_owner_idx ON workbooks (lower(owner_id), updated_at DESC) WHERE deleted_at IS NULL;

-- Departments form a tree so a share on a parent reaches every descendant.
CREATE TABLE IF NOT EXISTS departments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id uuid REFERENCES departments(id) ON DELETE RESTRICT,
    name text NOT NULL CHECK (btrim(name) <> ''),
    description text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS departments_sibling_name_idx
    ON departments (coalesce(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name));

CREATE TABLE IF NOT EXISTS department_members (
    department_id uuid NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    user_id text NOT NULL CHECK (btrim(user_id) <> ''),
    added_by text NOT NULL DEFAULT '',
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (department_id, user_id)
);

CREATE INDEX IF NOT EXISTS department_members_user_idx ON department_members (lower(user_id));

CREATE TABLE IF NOT EXISTS workbook_shares (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    principal_type text NOT NULL CHECK (principal_type IN ('user', 'department', 'role')),
    principal_id text NOT NULL CHECK (btrim(principal_id) <> ''),
    principal_label text NOT NULL DEFAULT '',
    role text NOT NULL CHECK (role IN ('viewer', 'commenter', 'editor')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS workbook_shares_principal_idx
    ON workbook_shares (workbook_id, principal_type, lower(principal_id));

CREATE INDEX IF NOT EXISTS workbook_shares_principal_lookup_idx
    ON workbook_shares (principal_type, lower(principal_id));

CREATE TABLE IF NOT EXISTS workbook_access_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    requester_id text NOT NULL CHECK (btrim(requester_id) <> ''),
    requester_email text NOT NULL DEFAULT '',
    requester_name text NOT NULL DEFAULT '',
    requested_role text NOT NULL CHECK (requested_role IN ('viewer', 'commenter', 'editor')),
    message text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied')),
    decided_by text NOT NULL DEFAULT '',
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS workbook_access_requests_pending_idx
    ON workbook_access_requests (workbook_id, lower(requester_id)) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS workbook_access_requests_workbook_idx
    ON workbook_access_requests (workbook_id, created_at DESC);
