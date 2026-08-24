-- "이 범위가 바뀌면 알려줘". 구글 시트의 알림 규칙에 해당한다.
--
-- 지켜보는 사람마다 한 줄이다. 같은 범위를 여럿이 지켜볼 수 있고, 한 사람이
-- 여러 범위를 지켜볼 수도 있다.
CREATE TABLE IF NOT EXISTS watch_rules (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    -- 지켜보는 사람. 이 사람에게 알린다.
    watcher text NOT NULL,
    -- 비어 있으면 시트 전체를 지켜본다.
    cell_range text NOT NULL DEFAULT '',
    label text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workbook_id, idempotency_key)
);

-- 한 사람이 같은 시트의 같은 범위를 두 번 지켜볼 까닭이 없다.
CREATE UNIQUE INDEX IF NOT EXISTS watch_rules_unique
    ON watch_rules (sheet_id, lower(watcher), cell_range);

-- 셀이 바뀔 때마다 이 시트의 규칙을 찾는다. 그 길이 빨라야 한다.
CREATE INDEX IF NOT EXISTS watch_rules_sheet_idx
    ON watch_rules (sheet_id) WHERE enabled;

CREATE INDEX IF NOT EXISTS watch_rules_watcher_idx
    ON watch_rules (lower(watcher), workbook_id);
