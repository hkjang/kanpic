-- 표의 합계 줄. 범위의 마지막 줄이 합계 줄이 된다.
--
-- 범위 안에 두는 까닭은 순환을 막기 위해서다. 합계 칸에 =SUM(매출표[금액])
-- 을 적는데, 매출표[금액] 이 합계 칸까지 세면 그 칸이 제 자신을 더한다.
-- 표가 합계 줄을 알고 있어야 열을 가리킬 때 그 줄을 뺄 수 있다.
ALTER TABLE sheet_tables ADD COLUMN IF NOT EXISTS totals_row boolean NOT NULL DEFAULT false;
