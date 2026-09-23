-- 자동 로그인(Silent SSO)은 prompt=none 으로 조용히 물어보는 시도다. Keycloak
-- 이 세션이 없다고 답하면 오류 화면이 아니라 로그인 화면으로 보내야 하므로,
-- 콜백이 그 시도가 조용한 것이었는지 알아야 한다. 시작할 때 여기 적어 둔다.
ALTER TABLE auth_transactions ADD COLUMN IF NOT EXISTS silent boolean NOT NULL DEFAULT false;
