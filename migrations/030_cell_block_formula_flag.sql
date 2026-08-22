-- Writing one cell used to load and lock every cell block in the workbook,
-- because a formula anywhere might depend on the cell being written. In a
-- sheet with no formulas nothing can depend on anything, and that load is the
-- whole cost: a single-cell write on a 50,000 row sheet took 1.3 seconds.
--
-- This flag lets the write path ask the cheap question first.
ALTER TABLE cell_blocks ADD COLUMN IF NOT EXISTS has_formula boolean NOT NULL DEFAULT true;

UPDATE cell_blocks SET has_formula = (payload::text LIKE '%"formula":%');

CREATE INDEX IF NOT EXISTS cell_blocks_formula_idx ON cell_blocks (sheet_id) WHERE has_formula;
