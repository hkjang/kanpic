-- 상위/하위 N개(그리고 백분율) 조건부 서식. 한 칸만 봐서는 답할 수 없고
-- 범위 전체의 순위를 알아야 하므로 규칙 종류를 따로 둔다.
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
  CHECK (rule_type IN ('value','duplicate','color_scale','data_bar','custom_formula','rank'));
