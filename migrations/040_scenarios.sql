-- 시나리오. 가정 한 벌에 이름을 붙여 저장해 두고 서로 견준다.
--
-- 데이터 표가 한두 칸을 여러 값으로 바꿔 보는 것이라면, 시나리오는 여러 칸을
-- 한 벌로 묶어 "낙관", "보수" 처럼 이름을 붙이는 것이다. 회의에서 두 안을
-- 나란히 놓고 보는 그 일이다.
CREATE TABLE IF NOT EXISTS scenarios (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    name text NOT NULL,
    -- 가정은 [{"cell":"B1","value":1000}, ...] 꼴이다. 칸 주소를 값과 함께
    -- 담아 두므로, 행이 움직이면 이 주소들도 함께 옮겨야 한다.
    inputs jsonb NOT NULL DEFAULT '[]'::jsonb,
    note text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 1,
    created_by text NOT NULL DEFAULT '',
    updated_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workbook_id, idempotency_key)
);

-- 이름이 둘이면 회의에서 어느 것을 보고 있는지 알 수 없다.
CREATE UNIQUE INDEX IF NOT EXISTS scenarios_workbook_name_unique
    ON scenarios (workbook_id, lower(name));

CREATE INDEX IF NOT EXISTS scenarios_sheet_idx ON scenarios (sheet_id);
