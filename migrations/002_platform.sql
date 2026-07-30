CREATE TABLE IF NOT EXISTS system_settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    value_type text NOT NULL CHECK (value_type IN ('string','number','boolean','string_list','object')),
    description text NOT NULL DEFAULT '',
    secret boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text NOT NULL DEFAULT 'system'
);

CREATE TABLE IF NOT EXISTS settings_versions (
    revision bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    snapshot jsonb NOT NULL,
    change_summary text NOT NULL,
    actor_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS personal_preferences (
    user_id text PRIMARY KEY,
    values jsonb NOT NULL DEFAULT '{}'::jsonb,
    revision bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_keys (
    id uuid PRIMARY KEY,
    user_id text NOT NULL,
    name text NOT NULL,
    key_prefix text NOT NULL,
    key_hash bytea NOT NULL UNIQUE,
    scopes text[] NOT NULL DEFAULT '{}',
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS api_keys_user_idx ON api_keys(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS auth_transactions (
    state_hash bytea PRIMARY KEY,
    code_verifier text NOT NULL,
    return_to text NOT NULL DEFAULT '/',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_sessions (
    session_hash bytea PRIMARY KEY,
    user_id text NOT NULL,
    email text NOT NULL DEFAULT '',
    display_name text NOT NULL DEFAULT '',
    roles text[] NOT NULL DEFAULT '{}',
    id_token_hint text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS user_sessions_expiry_idx ON user_sessions(expires_at);

CREATE TABLE IF NOT EXISTS system_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    logged_at timestamptz NOT NULL,
    level text NOT NULL,
    message text NOT NULL,
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    trace_id text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS system_logs_time_idx ON system_logs(logged_at DESC);
CREATE INDEX IF NOT EXISTS system_logs_level_time_idx ON system_logs(level, logged_at DESC);
