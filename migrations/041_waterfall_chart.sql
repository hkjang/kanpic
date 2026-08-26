-- 폭포 차트. 열을 앞에서부터 이름·값·(합계 여부)로 읽어, 증감이 쌓여
-- 결과에 이르는 과정을 그린다.
--
-- 차트 종류를 허락하는 자리가 둘이다 — Go 의 chartTypes 와 여기의 CHECK.
-- 한쪽만 고치면 화면에서는 만들어지는데 저장할 때 500 이 난다. 일정표를
-- 넣을 때 실제로 그렇게 났으므로 두 자리를 함께 고친다.
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
    CHECK (chart_type IN ('bar','line','area','pie','scatter','histogram','stacked_bar','stacked_area','combo','timeline','waterfall'));
