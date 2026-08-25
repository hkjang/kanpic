-- 일정표 차트. 열을 앞에서부터 이름·시작·끝으로 읽어 가로 막대로 그린다.
--
-- 차트 종류를 허락하는 자리가 둘이다 — Go 의 chartTypes 와 여기의 CHECK.
-- 한쪽만 고치면 화면에서는 만들어지는데 저장할 때 500 이 난다. 실제로 이번에
-- 그렇게 났고, 그래서 두 자리를 함께 고친다.
DO $$
DECLARE constraint_name text;
BEGIN
    SELECT conname INTO constraint_name
    FROM pg_constraint
    WHERE conrelid = 'charts'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%chart_type%';
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE charts DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

ALTER TABLE charts ADD CONSTRAINT charts_chart_type_check
    CHECK (chart_type IN ('bar','line','area','pie','scatter','histogram','stacked_bar','stacked_area','combo','timeline'));
