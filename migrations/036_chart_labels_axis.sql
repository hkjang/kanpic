-- 차트에 값 표시와 세로축 범위를 더한다.
--
-- 값 표시는 막대나 점 옆에 숫자를 적는 것이다. 인쇄물이나 보고 자료에서는
-- 눈금을 짚어 읽는 것보다 숫자가 적혀 있는 편이 빠르다.
ALTER TABLE charts ADD COLUMN IF NOT EXISTS data_labels boolean NOT NULL DEFAULT false;

-- 축 범위를 정하지 않으면 자료에 맞춰 정한다. 0 에서 시작하지 않으면
-- 작은 차이가 크게 보이므로, 정하는 것은 사람이 뜻을 가지고 하는 일이다.
ALTER TABLE charts ADD COLUMN IF NOT EXISTS y_axis_min double precision;
ALTER TABLE charts ADD COLUMN IF NOT EXISTS y_axis_max double precision;
