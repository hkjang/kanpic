-- 워크북에 저장해 두고 이름으로 부르는 수식. 팀에서 쓰는 셈을 한 번
-- 정의해 두면 =마진율(매출, 원가) 처럼 쓸 수 있다.
--
-- 매개변수는 순서가 뜻을 가지므로 배열로 둔다. 본문은 = 없이 저장한다.
CREATE TABLE IF NOT EXISTS named_functions (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    name text NOT NULL,
    parameters text[] NOT NULL DEFAULT '{}',
    body text NOT NULL,
    description text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workbook_id, idempotency_key)
);

-- 한 워크북 안에서 이름은 하나뿐이다. 대소문자를 가리지 않는다 — 수식에서
-- 부를 때도 가리지 않기 때문이다.
CREATE UNIQUE INDEX IF NOT EXISTS named_functions_workbook_name_unique
    ON named_functions (workbook_id, lower(name));

CREATE INDEX IF NOT EXISTS named_functions_workbook_list_idx
    ON named_functions (workbook_id, lower(name), id);
