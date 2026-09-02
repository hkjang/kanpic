-- 시트 위에 떠 있는 이미지. 이진 파일은 여기 bytea 로 둔다 — 새 저장소 없이
-- 백업과 함께 가고, 도커 이미지 하나로 돈다. 크기는 장당 files.max_image_mb
-- (기본 5MB), 개수는 워크북당 50장까지다.
--
-- 자리는 차트와 같은 픽셀 좌표다. 셀에 닻을 내리는 방식이 더 낫지만, 이 앱의
-- 떠 있는 개체는 모두 이 좌표를 쓰므로 같은 규칙을 따른다.
CREATE TABLE IF NOT EXISTS sheet_images (
    id uuid PRIMARY KEY,
    workbook_id uuid NOT NULL REFERENCES workbooks(id) ON DELETE CASCADE,
    sheet_id uuid NOT NULL REFERENCES sheets(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    content_type text NOT NULL CHECK (content_type IN ('image/png','image/jpeg','image/gif','image/webp')),
    byte_size integer NOT NULL CHECK (byte_size > 0),
    natural_width integer NOT NULL CHECK (natural_width > 0),
    natural_height integer NOT NULL CHECK (natural_height > 0),
    position_x integer NOT NULL DEFAULT 24 CHECK (position_x >= 0),
    position_y integer NOT NULL DEFAULT 24 CHECK (position_y >= 0),
    width integer NOT NULL CHECK (width BETWEEN 16 AND 4000),
    height integer NOT NULL CHECK (height BETWEEN 16 AND 4000),
    data bytea NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by text NOT NULL,
    updated_by text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workbook_id, created_by, idempotency_key)
);

CREATE INDEX IF NOT EXISTS sheet_images_workbook_sheet_idx ON sheet_images(workbook_id, sheet_id, created_at, id);
