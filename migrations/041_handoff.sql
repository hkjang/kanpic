-- 서비스 간 문서 넘기기(HANDOFF-STANDARD).
--
-- handoff_claims 는 보내는 쪽이 발급한 표다. 표 원문은 어디에도 적지 않고
-- 해시만 둔다 — 표가 곧 자격이라 저장소가 새어도 표가 새면 안 된다. 문서는
-- 표를 만들 때 미리 뽑아 함께 두므로, 받아 가는 쪽은 누른 사람이 본 그
-- 상태를 받는다. 꺼내면서 지우므로 한 번만 쓰인다.
CREATE TABLE IF NOT EXISTS handoff_claims (
    claim_hash text PRIMARY KEY,
    resource text NOT NULL,
    format text NOT NULL,
    file_name text NOT NULL,
    content_type text NOT NULL,
    data bytea NOT NULL,
    issued_by text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS handoff_claims_expires_idx ON handoff_claims(expires_at);

-- handoff_receipts 는 받는 쪽이 들어온 문서에 남기는 출처다. 어느 서비스에서
-- 왔는지 나중에 물어볼 수 있어야 한다.
CREATE TABLE IF NOT EXISTS handoff_receipts (
    workbook_id uuid PRIMARY KEY REFERENCES workbooks(id) ON DELETE CASCADE,
    source text NOT NULL,
    file_name text NOT NULL DEFAULT '',
    received_by text NOT NULL DEFAULT '',
    received_at timestamptz NOT NULL DEFAULT now()
);
