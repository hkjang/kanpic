-- 워크북에서 만든 프레젠테이션의 출처 기록.
--
-- 두 가지를 위해 필요하다. 첫째는 권한이다. 덱은 프레젠테이션 서비스의 공용
-- 계정 아래 만들어지므로, kanpic 이 어느 워크북에서 나온 덱인지 알지 못하면
-- 로그인한 누구나 남의 덱을 내려받을 수 있다. 둘째는 되짚기다 — 원본 범위와
-- 그때의 워크북 버전을 적어 두면 나중에 자료가 바뀌었는지 말할 수 있다.
CREATE TABLE IF NOT EXISTS presentations (
    id text PRIMARY KEY,
    provider text NOT NULL,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    cell_range text NOT NULL,
    source_version bigint NOT NULL DEFAULT 0,
    title text NOT NULL DEFAULT '',
    template text NOT NULL DEFAULT '',
    slide_count integer NOT NULL DEFAULT 0,
    edit_url text NOT NULL DEFAULT '',
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS presentations_workbook_idx ON presentations(workbook_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS presentations_sheet_idx ON presentations(sheet_id);
