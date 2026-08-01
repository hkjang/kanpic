ALTER TABLE ai_actions
    DROP CONSTRAINT IF EXISTS ai_actions_status_check;

ALTER TABLE ai_actions
    ADD CONSTRAINT ai_actions_status_check
    CHECK (status IN ('planned','completed','applying','applied','undoing','undone','failed'));
