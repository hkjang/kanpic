-- 진짜 표. 지금까지 "테이블 서식" 은 색만 칠하는 것이어서, 표를 만들어도
-- 수식에서 그것을 가리킬 방법이 없었다.
--
-- 표에 이름이 있으면 =SUM(매출표[금액]) 이라고 적을 수 있다. 열을 하나 끼워
-- 넣어도 이 수식은 그대로 맞다. 범위로 적은 =SUM(C2:C50) 은 그렇지 않다 —
-- 사람이 옮겨 적어야 하고, 잊으면 조용히 틀린 값을 낸다.
CREATE TABLE IF NOT EXISTS sheet_tables (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    name text NOT NULL,
    cell_range text NOT NULL,
    -- 첫 줄이 머리글이면 그 글자가 열 이름이 된다. 아니면 열1, 열2 로 센다.
    header_row boolean NOT NULL DEFAULT true,
    theme text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 1,
    created_by text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workbook_id, idempotency_key)
);

-- 이름은 워크북 안에서 하나여야 한다. 둘이면 매출표[금액] 이 어느 것을
-- 가리키는지 사람도 기계도 알 수 없다.
CREATE UNIQUE INDEX IF NOT EXISTS sheet_tables_workbook_name_unique
    ON sheet_tables (workbook_id, lower(name));

CREATE INDEX IF NOT EXISTS sheet_tables_sheet_idx ON sheet_tables (sheet_id);
