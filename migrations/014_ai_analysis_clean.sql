ALTER TABLE ai_actions
    DROP CONSTRAINT IF EXISTS ai_actions_mode_check;

ALTER TABLE ai_actions
    ADD CONSTRAINT ai_actions_mode_check
    CHECK (mode IN ('formula','explain','fix','summarize','anomaly','clean'));

ALTER TABLE ai_actions
    ADD COLUMN IF NOT EXISTS findings jsonb NOT NULL DEFAULT '[]'::jsonb;
