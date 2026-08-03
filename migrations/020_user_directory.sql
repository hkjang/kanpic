-- User directory and kanpic-local roles.
--
-- Identity still comes from the identity provider or the bootstrap login, but
-- kanpic needs a place to suspend an account, grant roles that role-based
-- sharing can target, and show administrators who has been active.

CREATE TABLE IF NOT EXISTS directory_users (
    user_id text PRIMARY KEY CHECK (btrim(user_id) <> ''),
    display_name text NOT NULL DEFAULT '',
    email text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    note text NOT NULL DEFAULT '',
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz
);

CREATE INDEX IF NOT EXISTS directory_users_email_idx ON directory_users (lower(email));
CREATE INDEX IF NOT EXISTS directory_users_status_idx ON directory_users (status);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id text NOT NULL CHECK (btrim(user_id) <> ''),
    role text NOT NULL CHECK (btrim(role) <> ''),
    granted_by text NOT NULL DEFAULT '',
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role)
);

CREATE INDEX IF NOT EXISTS user_roles_role_idx ON user_roles (lower(role));

-- Seed the directory with everybody the system already knows about so the first
-- administrator sees a populated list instead of an empty page.
INSERT INTO directory_users (user_id)
SELECT DISTINCT owner_id FROM workbooks WHERE btrim(owner_id) <> ''
ON CONFLICT DO NOTHING;

INSERT INTO directory_users (user_id)
SELECT DISTINCT user_id FROM department_members
ON CONFLICT DO NOTHING;

INSERT INTO directory_users (user_id, email, display_name, last_seen_at)
SELECT DISTINCT ON (user_id) user_id, email, display_name, last_seen_at
FROM user_sessions WHERE btrim(user_id) <> ''
ORDER BY user_id, last_seen_at DESC
ON CONFLICT (user_id) DO UPDATE
SET email = CASE WHEN directory_users.email = '' THEN EXCLUDED.email ELSE directory_users.email END,
    display_name = CASE WHEN directory_users.display_name = '' THEN EXCLUDED.display_name ELSE directory_users.display_name END,
    last_seen_at = GREATEST(directory_users.last_seen_at, EXCLUDED.last_seen_at);
