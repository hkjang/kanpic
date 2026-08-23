-- 아이콘 집합 조건부 서식. 신호등과 화살표는 색만으로 구분하기 어려운
-- 사람에게도 크기와 방향으로 뜻이 전해지므로 색 눈금과 따로 둔다.
-- 문턱값은 엑셀 기본값(3개는 33/67%, 4개는 25/50/75%, 5개는 20/40/60/80%)을
-- 그대로 쓰므로 따로 저장하지 않는다.
ALTER TABLE conditional_formats
  ADD COLUMN IF NOT EXISTS icon_style text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS icon_reverse boolean NOT NULL DEFAULT false;

DO $$
DECLARE constraint_name text;
BEGIN
  SELECT conname INTO constraint_name FROM pg_constraint
   WHERE conrelid='conditional_formats'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%rule_type%';
  IF constraint_name IS NOT NULL THEN
    EXECUTE format('ALTER TABLE conditional_formats DROP CONSTRAINT %I', constraint_name);
  END IF;
END $$;

ALTER TABLE conditional_formats
  ADD CONSTRAINT conditional_formats_rule_type_check
  CHECK (rule_type IN ('value','duplicate','color_scale','data_bar','custom_formula','rank','icon_set'));
