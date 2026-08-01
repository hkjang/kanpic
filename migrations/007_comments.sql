CREATE TABLE IF NOT EXISTS comment_threads (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    cell_range text NOT NULL,
    idempotency_key text NOT NULL,
    resolved boolean NOT NULL DEFAULT false,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    resolved_by text NOT NULL DEFAULT '',
    resolved_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workbook_id, created_by, idempotency_key)
);

CREATE INDEX IF NOT EXISTS comment_threads_workbook_sheet_idx
    ON comment_threads(workbook_id, sheet_id, resolved, updated_at DESC);

CREATE TABLE IF NOT EXISTS comment_messages (
    id uuid PRIMARY KEY,
    thread_id uuid NOT NULL REFERENCES comment_threads(id) ON DELETE CASCADE,
    author_id text NOT NULL,
    idempotency_key text NOT NULL,
    content text NOT NULL,
    mentions text[] NOT NULL DEFAULT '{}',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    deleted_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (thread_id, author_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS comment_messages_thread_idx
    ON comment_messages(thread_id, created_at);

CREATE TABLE IF NOT EXISTS mention_notifications (
    id uuid PRIMARY KEY,
    recipient text NOT NULL,
    actor_id text NOT NULL,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    thread_id uuid NOT NULL REFERENCES comment_threads(id) ON DELETE CASCADE,
    message_id uuid NOT NULL REFERENCES comment_messages(id) ON DELETE CASCADE,
    cell_range text NOT NULL,
    read_at timestamptz,
    created_at timestamptz NOT NULL,
    UNIQUE (recipient, message_id)
);

CREATE INDEX IF NOT EXISTS mention_notifications_recipient_idx
    ON mention_notifications(lower(recipient), read_at, created_at DESC);
