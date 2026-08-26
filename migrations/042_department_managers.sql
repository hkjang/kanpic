-- 부서 관리자. 전역 관리자가 지정하고, 자기 부서와 그 아래 부서의 구성원
-- 계정만 다룰 수 있다.
--
-- 소유권 이전과 설정·로그·키는 여기에 열지 않는다. 자료를 옮기는 일은
-- 조용히 사고가 나므로 전역 관리자만 한다.
CREATE TABLE IF NOT EXISTS department_managers (
    department_id uuid NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    user_id text NOT NULL CHECK (btrim(user_id) <> ''),
    added_by text NOT NULL DEFAULT '',
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (department_id, user_id)
);

CREATE INDEX IF NOT EXISTS department_managers_user_idx ON department_managers (lower(user_id));
