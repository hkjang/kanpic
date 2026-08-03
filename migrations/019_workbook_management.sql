-- Per-user favourites and a recoverable workbook trash.
--
-- Favourites used to live on the workbook row, so a starred workbook was
-- starred for everybody it is shared with and viewers could not star at all.
-- Existing stars are migrated to their owner.

CREATE TABLE IF NOT EXISTS workbook_favorites (
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    user_id text NOT NULL CHECK (btrim(user_id) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workbook_id, user_id)
);

CREATE INDEX IF NOT EXISTS workbook_favorites_user_idx ON workbook_favorites (lower(user_id));

INSERT INTO workbook_favorites (workbook_id, user_id)
SELECT id, owner_id FROM workbooks WHERE favorite AND btrim(owner_id) <> ''
ON CONFLICT DO NOTHING;

-- The trash keeps who deleted a workbook so the console can explain a pending
-- deletion, and lets the owner restore it.
ALTER TABLE workbooks
    ADD COLUMN IF NOT EXISTS deleted_by text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS workbooks_deleted_idx ON workbooks (lower(owner_id), deleted_at DESC) WHERE deleted_at IS NOT NULL;
