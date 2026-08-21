-- Stacked and combination charts need their own type values, and a combination
-- chart needs somewhere to say that its line belongs on a second axis.
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
    CHECK (chart_type IN ('bar','line','area','pie','scatter','histogram','stacked_bar','stacked_area','combo'));

ALTER TABLE charts ADD COLUMN IF NOT EXISTS secondary_axis boolean NOT NULL DEFAULT false;
