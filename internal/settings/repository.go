package settings

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/internal/analytics"
	"kanpic/internal/mail"
)

type Setting struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value,omitempty"`
	ValueType   string          `json:"value_type"`
	Description string          `json:"description,omitempty"`
	Secret      bool            `json:"secret"`
	Configured  bool            `json:"configured,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
	UpdatedBy   string          `json:"updated_by"`
}

type Version struct {
	Revision      int64     `json:"revision"`
	ChangeSummary string    `json:"change_summary"`
	ActorID       string    `json:"actor_id"`
	CreatedAt     time.Time `json:"created_at"`
}

type Preferences struct {
	UserID    string         `json:"user_id"`
	Values    map[string]any `json:"values"`
	Revision  int64          `json:"revision"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type ValidationIssue struct {
	Key      string `json:"key"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues"`
}

type TestResult struct {
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	DurationMS int64  `json:"duration_ms"`
}

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var defaults = []Setting{
	{Key: "branding.product_name", Value: json.RawMessage(`"kanpic"`), ValueType: "string", Description: "화면에 표시할 제품명"},
	{Key: "localization.locale", Value: json.RawMessage(`"ko-KR"`), ValueType: "string", Description: "기본 로케일"},
	{Key: "localization.timezone", Value: json.RawMessage(`"Asia/Seoul"`), ValueType: "string", Description: "기본 시간대"},
	{Key: "editor.autosave_batch_ms", Value: json.RawMessage(`250`), ValueType: "number", Description: "자동 저장 배치 간격"},
	{Key: "editor.max_cells_per_operation", Value: json.RawMessage(`1000`), ValueType: "number", Description: "쓰기 요청당 최대 셀 수"},
	{Key: "auth.oidc.enabled", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "Keycloak OIDC 로그인 사용"},
	{Key: "auth.oidc.issuer_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "Keycloak Realm Issuer URL"},
	{Key: "auth.oidc.client_id", Value: json.RawMessage(`"kanpic"`), ValueType: "string", Description: "Keycloak Client ID"},
	{Key: "auth.oidc.client_secret", Value: json.RawMessage(`""`), ValueType: "string", Description: "Confidential Client Secret (선택)", Secret: true},
	{Key: "auth.oidc.ca_pem", Value: json.RawMessage(`""`), ValueType: "string", Description: "사내 CA 인증서 PEM", Secret: true},
	{Key: "auth.oidc.scopes", Value: json.RawMessage(`["openid","profile","email"]`), ValueType: "string_list", Description: "OIDC Scope"},
	{Key: "auth.oidc.admin_roles", Value: json.RawMessage(`["kanpic-admin"]`), ValueType: "string_list", Description: "관리자 권한으로 인정할 Keycloak Role"},
	{Key: "auth.session_hours", Value: json.RawMessage(`8`), ValueType: "number", Description: "로그인 세션 유지 시간"},
	{Key: "server.public_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "프록시 외부 공개 URL (비우면 요청 Host 사용)"},
	{Key: "files.max_import_mb", Value: json.RawMessage(`20`), ValueType: "number", Description: "Import 파일 최대 크기"},
	{Key: "ai.enabled", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "AI 기능 사용"},
	{Key: "ai.gateway_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "사내 OpenAI 호환 LLM Gateway"},
	{Key: "ai.model", Value: json.RawMessage(`"kanpic-default"`), ValueType: "string", Description: "AI 작업에 사용할 모델"},
	{Key: "ai.api_key", Value: json.RawMessage(`""`), ValueType: "string", Description: "LLM Gateway API Key (선택)", Secret: true},
	{Key: "ai.ca_pem", Value: json.RawMessage(`""`), ValueType: "string", Description: "사내 LLM Gateway CA 인증서 PEM", Secret: true},
	{Key: "ai.timeout_seconds", Value: json.RawMessage(`30`), ValueType: "number", Description: "AI Gateway 요청 제한 시간(초)"},
	{Key: "ai.max_input_cells", Value: json.RawMessage(`200`), ValueType: "number", Description: "AI에 전달할 선택 범위 최대 셀 수"},
	{Key: "ai.max_changes", Value: json.RawMessage(`100`), ValueType: "number", Description: "AI 계획 한 건의 최대 변경 셀 수"},
	{Key: "ai.max_output_tokens", Value: json.RawMessage(`0`), ValueType: "number", Description: "AI 응답 최대 토큰 수. 0이면 모델의 컨텍스트 길이에서 자동 계산"},
	{Key: "ai.history_retention_days", Value: json.RawMessage(`0`), ValueType: "number", Description: "AI 호출 이력 보존 기간(일). 0이면 계속 보관"},
	{Key: "presentation.enabled", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "선택 범위로 프레젠테이션 만들기 사용"},
	{Key: "presentation.provider", Value: json.RawMessage(`"ptium"`), ValueType: "string", Description: "프레젠테이션 서비스 종류"},
	{Key: "presentation.base_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "프레젠테이션 서비스 주소 (예: https://ptium.example.com)"},
	{Key: "presentation.api_key", Value: json.RawMessage(`""`), ValueType: "string", Description: "프레젠테이션 서비스 API Key. presentations:read 와 presentations:write 만 있으면 됩니다", Secret: true},
	{Key: "presentation.timeout_seconds", Value: json.RawMessage(`60`), ValueType: "number", Description: "프레젠테이션 서비스 요청 제한 시간(초)"},
	{Key: "presentation.default_template_id", Value: json.RawMessage(`""`), ValueType: "string", Description: "기본 템플릿 ID. 비우면 서비스 기본 디자인"},
	{Key: "presentation.max_cells", Value: json.RawMessage(`5000`), ValueType: "number", Description: "한 프레젠테이션이 읽을 최대 셀 수"},
	{Key: "mail.enabled", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "이벤트 알림 메일 발송 사용"},
	{Key: "mail.smtp_host", Value: json.RawMessage(`""`), ValueType: "string", Description: "사내 SMTP 서버 주소"},
	{Key: "mail.smtp_port", Value: json.RawMessage(`25`), ValueType: "number", Description: "SMTP 포트. 25는 사내 릴레이, 587은 STARTTLS, 465는 TLS"},
	{Key: "mail.security", Value: json.RawMessage(`"auto"`), ValueType: "string", Description: "전송 보안: auto, none, starttls, tls"},
	{Key: "mail.username", Value: json.RawMessage(`""`), ValueType: "string", Description: "SMTP 사용자 이름. 비우면 인증 없이 발송"},
	{Key: "mail.password", Value: json.RawMessage(`""`), ValueType: "string", Description: "SMTP 비밀번호", Secret: true},
	{Key: "mail.from_address", Value: json.RawMessage(`""`), ValueType: "string", Description: "보내는 사람 주소. 비우면 kanpic@SMTP호스트"},
	{Key: "mail.from_name", Value: json.RawMessage(`"kanpic"`), ValueType: "string", Description: "보내는 사람 이름"},
	{Key: "mail.base_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "메일 본문 링크에 사용할 kanpic 주소"},
	{Key: "mail.skip_tls_verify", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "사설 인증서 SMTP의 인증서 검증 생략"},
	{Key: "mail.timeout_seconds", Value: json.RawMessage(`10`), ValueType: "number", Description: "SMTP 연결 제한 시간(초)"},
	{Key: "mail.notify_share", Value: json.RawMessage(`true`), ValueType: "boolean", Description: "워크북 공유 시 메일 발송"},
	{Key: "mail.notify_comment", Value: json.RawMessage(`true`), ValueType: "boolean", Description: "댓글과 답글 작성 시 메일 발송"},
	{Key: "mail.notify_mention", Value: json.RawMessage(`true`), ValueType: "boolean", Description: "댓글 멘션 시 메일 발송"},
	{Key: "mail.notify_access_request", Value: json.RawMessage(`true`), ValueType: "boolean", Description: "액세스 요청과 처리 결과 메일 발송"},
	{Key: "analytics.enabled", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "방문자 추적 코드 삽입 사용"},
	{Key: "analytics.provider", Value: json.RawMessage(`"none"`), ValueType: "string", Description: "추적 도구: none, ga4, gtm, matomo, custom"},
	{Key: "analytics.measurement_id", Value: json.RawMessage(`""`), ValueType: "string", Description: "GA4 측정 ID(G-) 또는 GTM 컨테이너 ID(GTM-)"},
	{Key: "analytics.matomo_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "Matomo 서버 주소"},
	{Key: "analytics.matomo_site_id", Value: json.RawMessage(`""`), ValueType: "string", Description: "Matomo 사이트 ID"},
	{Key: "analytics.custom_snippet", Value: json.RawMessage(`""`), ValueType: "string", Description: "직접 입력하는 추적 코드. script 태그를 포함한 HTML"},
	{Key: "analytics.allowed_hosts", Value: json.RawMessage(`""`), ValueType: "string", Description: "추적 코드가 접속할 추가 도메인. 쉼표로 구분"},
	{Key: "analytics.include_admin", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "관리자·개인 설정 화면에도 추적 코드 삽입"},
	{Key: "analytics.placement", Value: json.RawMessage(`"head"`), ValueType: "string", Description: "삽입 위치: head 또는 body"},
	{Key: "automation.enabled", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "워크북 자동화 실행 사용"},
	{Key: "automation.max_cells_per_run", Value: json.RawMessage(`1000`), ValueType: "number", Description: "자동화 실행 한 건의 최대 변경 셀 수"},
	{Key: "automation.max_runs_per_hour", Value: json.RawMessage(`100`), ValueType: "number", Description: "워크북별 시간당 자동화 실행 한도"},
	{Key: "automation.scheduler_poll_seconds", Value: json.RawMessage(`15`), ValueType: "number", Description: "PostgreSQL 스케줄 자동화 확인 주기(초)"},
	{Key: "sharing.max_link_access", Value: json.RawMessage(`"anyone"`), ValueType: "string", Description: "허용할 최대 링크 액세스 범위 (restricted, organization, anyone)"},
	{Key: "sharing.default_link_access", Value: json.RawMessage(`"restricted"`), ValueType: "string", Description: "새 워크북의 기본 링크 액세스"},
	{Key: "mcp.enabled", Value: json.RawMessage(`true`), ValueType: "boolean", Description: "MCP Gateway 사용"},
	{Key: "observability.log_retention_days", Value: json.RawMessage(`30`), ValueType: "number", Description: "서버 로그 보존 일수"},
}

func (r *Repository) EnsureDefaults(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, item := range defaults {
		if _, err := tx.Exec(ctx, `INSERT INTO system_settings(key,value,value_type,description,secret,updated_by) VALUES($1,$2,$3,$4,$5,'system') ON CONFLICT(key) DO NOTHING`, item.Key, item.Value, item.ValueType, item.Description, item.Secret); err != nil {
			return err
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM settings_versions`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if err := r.snapshot(ctx, tx, "initial defaults", "system"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) List(ctx context.Context, revealSecrets bool) ([]Setting, error) {
	rows, err := r.pool.Query(ctx, `SELECT key,value,value_type,description,secret,updated_at,updated_by FROM system_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Setting, 0)
	for rows.Next() {
		var item Setting
		if err := rows.Scan(&item.Key, &item.Value, &item.ValueType, &item.Description, &item.Secret, &item.UpdatedAt, &item.UpdatedBy); err != nil {
			return nil, err
		}
		if item.Secret && !revealSecrets {
			item = redact(item)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) Values(ctx context.Context) (map[string]any, error) {
	items, err := r.List(ctx, true)
	if err != nil {
		return nil, err
	}
	values := make(map[string]any, len(items))
	for _, item := range items {
		var value any
		if err := json.Unmarshal(item.Value, &value); err != nil {
			return nil, err
		}
		values[item.Key] = value
	}
	return values, nil
}

func (r *Repository) Put(ctx context.Context, item Setting, actorID string) (Setting, error) {
	item.Key = strings.TrimSpace(item.Key)
	if item.Key == "" || strings.ContainsAny(item.Key, " \t\r\n") {
		return Setting{}, fmt.Errorf("invalid setting key")
	}
	if issue := validateValue(item); issue != "" {
		return Setting{}, errors.New(issue)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Setting{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `INSERT INTO system_settings(key,value,value_type,description,secret,updated_by) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(key) DO UPDATE SET value=excluded.value,value_type=excluded.value_type,description=excluded.description,secret=excluded.secret,updated_at=now(),updated_by=excluded.updated_by RETURNING updated_at`, item.Key, item.Value, item.ValueType, item.Description, item.Secret, actorID).Scan(&item.UpdatedAt)
	if err != nil {
		return Setting{}, err
	}
	item.UpdatedBy = actorID
	if err := r.snapshot(ctx, tx, "upsert "+item.Key, actorID); err != nil {
		return Setting{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Setting{}, err
	}
	return redact(item), nil
}

func (r *Repository) Delete(ctx context.Context, key, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM system_settings WHERE key=$1`, key)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err := r.snapshot(ctx, tx, "delete "+key, actorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) Validate(ctx context.Context) (ValidationResult, error) {
	items, err := r.List(ctx, true)
	if err != nil {
		return ValidationResult{}, err
	}
	issues := make([]ValidationIssue, 0)
	values := make(map[string]Setting, len(items))
	for _, item := range items {
		values[item.Key] = item
		if message := validateValue(item); message != "" {
			issues = append(issues, ValidationIssue{Key: item.Key, Severity: "error", Message: message})
		}
	}
	if enabled, ok := boolValue(values["auth.oidc.enabled"]); ok && enabled {
		for _, key := range []string{"auth.oidc.issuer_url", "auth.oidc.client_id"} {
			if value, ok := stringValue(values[key]); !ok || strings.TrimSpace(value) == "" {
				issues = append(issues, ValidationIssue{Key: key, Severity: "error", Message: "OIDC를 사용할 때 필수입니다."})
			}
		}
	}
	if enabled, ok := boolValue(values["analytics.enabled"]); ok && enabled {
		if issue := validateAnalytics(values); issue != "" {
			issues = append(issues, ValidationIssue{Key: "analytics.provider", Severity: "error", Message: issue})
		}
	}
	if enabled, ok := boolValue(values["mail.enabled"]); ok && enabled {
		if host, ok := stringValue(values["mail.smtp_host"]); !ok || strings.TrimSpace(host) == "" {
			issues = append(issues, ValidationIssue{Key: "mail.smtp_host", Severity: "error", Message: "메일 발송을 사용할 때 필수입니다."})
		}
	}
	if enabled, ok := boolValue(values["presentation.enabled"]); ok && enabled {
		if value, ok := stringValue(values["presentation.base_url"]); !ok || strings.TrimSpace(value) == "" {
			issues = append(issues, ValidationIssue{Key: "presentation.base_url", Severity: "error", Message: "프레젠테이션을 사용할 때 필수입니다."})
		}
	}
	if enabled, ok := boolValue(values["ai.enabled"]); ok && enabled {
		for _, key := range []string{"ai.gateway_url", "ai.model"} {
			if value, ok := stringValue(values[key]); !ok || strings.TrimSpace(value) == "" {
				issues = append(issues, ValidationIssue{Key: key, Severity: "error", Message: "AI를 사용할 때 필수입니다."})
			}
		}
	}
	return ValidationResult{Valid: len(issues) == 0, Issues: issues}, nil
}

func (r *Repository) Test(ctx context.Context) ([]TestResult, error) {
	results := make([]TestResult, 0, 4)
	started := time.Now()
	err := r.pool.Ping(ctx)
	results = append(results, TestResult{Name: "PostgreSQL", Success: err == nil, Message: resultMessage(err, "연결 성공"), DurationMS: time.Since(started).Milliseconds()})
	values, valueErr := r.Values(ctx)
	if valueErr != nil {
		return results, valueErr
	}
	if enabled, _ := values["auth.oidc.enabled"].(bool); enabled {
		issuer, _ := values["auth.oidc.issuer_url"].(string)
		started = time.Now()
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", nil)
		var testErr error
		if requestErr != nil {
			testErr = requestErr
		} else {
			response, err := http.DefaultClient.Do(request)
			testErr = err
			if response != nil {
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					testErr = fmt.Errorf("HTTP %d", response.StatusCode)
				}
			}
		}
		results = append(results, TestResult{Name: "Keycloak OIDC", Success: testErr == nil, Message: resultMessage(testErr, "Discovery 연결 성공"), DurationMS: time.Since(started).Milliseconds()})
	}
	if enabled, _ := values["ai.enabled"].(bool); enabled {
		started = time.Now()
		testErr := testAIGateway(ctx, values)
		results = append(results, TestResult{Name: "사내 LLM Gateway", Success: testErr == nil, Message: resultMessage(testErr, "OpenAI 호환 models 연결 성공"), DurationMS: time.Since(started).Milliseconds()})
	}
	if enabled, _ := values["mail.enabled"].(bool); enabled {
		started = time.Now()
		testErr := testSMTP(ctx, values)
		results = append(results, TestResult{Name: "사내 SMTP", Success: testErr == nil, Message: resultMessage(testErr, "SMTP 연결과 인사 성공"), DurationMS: time.Since(started).Milliseconds()})
	}
	if enabled, _ := values["automation.enabled"].(bool); enabled {
		started = time.Now()
		_, testErr := r.pool.Exec(ctx, `SELECT a.next_run_at,r.scheduled_for,r.trigger_key_id,r.payload_digest,r.payload_bytes,r.counts_toward_rate FROM automations a LEFT JOIN automation_runs r ON r.automation_id=a.id LIMIT 0`)
		results = append(results, TestResult{Name: "자동화 저장소", Success: testErr == nil, Message: resultMessage(testErr, "정의·예약·웹훅·실행 이력 저장소 준비 완료"), DurationMS: time.Since(started).Milliseconds()})
	}
	return results, nil
}

func (r *Repository) Versions(ctx context.Context) ([]Version, error) {
	rows, err := r.pool.Query(ctx, `SELECT revision,change_summary,actor_id,created_at FROM settings_versions ORDER BY revision DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Version, 0)
	for rows.Next() {
		var item Version
		if err := rows.Scan(&item.Revision, &item.ChangeSummary, &item.ActorID, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) Restore(ctx context.Context, revision int64, actorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var data []byte
	if err := tx.QueryRow(ctx, `SELECT snapshot FROM settings_versions WHERE revision=$1`, revision).Scan(&data); errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	} else if err != nil {
		return err
	}
	var items []Setting
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM system_settings`); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO system_settings(key,value,value_type,description,secret,updated_at,updated_by) VALUES($1,$2,$3,$4,$5,now(),$6)`, item.Key, item.Value, item.ValueType, item.Description, item.Secret, actorID); err != nil {
			return err
		}
	}
	if err := r.snapshot(ctx, tx, fmt.Sprintf("restore revision %d", revision), actorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) GetPreferences(ctx context.Context, userID string) (Preferences, error) {
	var item Preferences
	var data []byte
	err := r.pool.QueryRow(ctx, `SELECT user_id,values,revision,updated_at FROM personal_preferences WHERE user_id=$1`, userID).Scan(&item.UserID, &data, &item.Revision, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preferences{UserID: userID, Values: map[string]any{}, Revision: 0}, nil
	}
	if err != nil {
		return Preferences{}, err
	}
	if err := json.Unmarshal(data, &item.Values); err != nil {
		return Preferences{}, err
	}
	return item, nil
}

func (r *Repository) PutPreferences(ctx context.Context, userID string, values map[string]any) (Preferences, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return Preferences{}, err
	}
	item := Preferences{UserID: userID, Values: values}
	err = r.pool.QueryRow(ctx, `INSERT INTO personal_preferences(user_id,values) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET values=excluded.values,revision=personal_preferences.revision+1,updated_at=now() RETURNING revision,updated_at`, userID, data).Scan(&item.Revision, &item.UpdatedAt)
	return item, err
}

func (r *Repository) snapshot(ctx context.Context, tx pgx.Tx, summary, actorID string) error {
	rows, err := tx.Query(ctx, `SELECT key,value,value_type,description,secret,updated_at,updated_by FROM system_settings ORDER BY key`)
	if err != nil {
		return err
	}
	items := make([]Setting, 0)
	for rows.Next() {
		var item Setting
		if err := rows.Scan(&item.Key, &item.Value, &item.ValueType, &item.Description, &item.Secret, &item.UpdatedAt, &item.UpdatedBy); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	data, _ := json.Marshal(items)
	_, err = tx.Exec(ctx, `INSERT INTO settings_versions(snapshot,change_summary,actor_id) VALUES($1,$2,$3)`, data, summary, actorID)
	return err
}

func validateValue(item Setting) string {
	if !json.Valid(item.Value) {
		return "value가 유효한 JSON이 아닙니다."
	}
	var value any
	_ = json.Unmarshal(item.Value, &value)
	valid := false
	switch item.ValueType {
	case "string":
		_, valid = value.(string)
	case "number":
		_, valid = value.(float64)
	case "boolean":
		_, valid = value.(bool)
	case "string_list":
		list, ok := value.([]any)
		valid = ok
		for _, candidate := range list {
			if _, ok := candidate.(string); !ok {
				valid = false
			}
		}
	case "object":
		_, valid = value.(map[string]any)
	default:
		return "지원하지 않는 value_type입니다."
	}
	if !valid {
		return "value와 value_type이 일치하지 않습니다."
	}
	if item.Key == "auth.oidc.issuer_url" {
		if text, _ := value.(string); text != "" {
			parsed, err := url.Parse(text)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return "Issuer URL은 http 또는 https URL이어야 합니다."
			}
		}
	}
	if item.Key == "ai.gateway_url" {
		if text, _ := value.(string); text != "" {
			parsed, err := url.Parse(text)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return "AI Gateway URL은 http 또는 https URL이어야 합니다."
			}
		}
	}
	if item.Key == "editor.autosave_batch_ms" {
		number, _ := value.(float64)
		if number < 100 || number > 500 {
			return "자동 저장 간격은 100~500ms여야 합니다."
		}
	}
	if item.Key == "files.max_import_mb" {
		number, _ := value.(float64)
		if number < 1 || number > 2048 {
			return "Import 파일 한도는 1~2048MB여야 합니다."
		}
	}
	if item.Key == "ai.timeout_seconds" {
		number, _ := value.(float64)
		if number < 1 || number > 120 {
			return "AI 요청 제한 시간은 1~120초여야 합니다."
		}
	}
	if item.Key == "ai.max_input_cells" {
		number, _ := value.(float64)
		if number < 1 || number > 1000 {
			return "AI 입력 범위는 1~1000셀이어야 합니다."
		}
	}
	if item.Key == "ai.max_changes" {
		number, _ := value.(float64)
		if number < 1 || number > 1000 {
			return "AI 변경 한도는 1~1000셀이어야 합니다."
		}
	}
	if item.Key == "automation.max_cells_per_run" {
		number, _ := value.(float64)
		if number < 1 || number > 10000 {
			return "자동화 변경 한도는 1~10000셀이어야 합니다."
		}
	}
	if item.Key == "automation.max_runs_per_hour" {
		number, _ := value.(float64)
		if number < 1 || number > 10000 {
			return "자동화 실행 한도는 시간당 1~10000건이어야 합니다."
		}
	}
	if item.Key == "automation.scheduler_poll_seconds" {
		number, _ := value.(float64)
		if number < 5 || number > 300 {
			return "자동화 스케줄 확인 주기는 5~300초여야 합니다."
		}
	}
	return ""
}

func testAIGateway(ctx context.Context, values map[string]any) error {
	base, _ := values["ai.gateway_url"].(string)
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("AI Gateway URL이 올바르지 않습니다.")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/chat/completions") {
		path = strings.TrimSuffix(path, "/chat/completions")
	}
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	parsed.Path = path + "/models"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if ca, _ := values["ai.ca_pem"].(string); strings.TrimSpace(ca) != "" {
		roots, rootErr := x509.SystemCertPool()
		if rootErr != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			return errors.New("ai.ca_pem이 올바른 인증서가 아닙니다.")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	if key, _ := values["ai.api_key"].(string); strings.TrimSpace(key) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	}
	response, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil
}

func redact(item Setting) Setting {
	if !item.Secret {
		return item
	}
	var value any
	if len(item.Value) > 0 && json.Unmarshal(item.Value, &value) == nil {
		switch typed := value.(type) {
		case string:
			item.Configured = strings.TrimSpace(typed) != ""
		default:
			item.Configured = value != nil
		}
	}
	item.Value = nil
	return item
}

func boolValue(item Setting) (bool, bool) {
	var value bool
	if len(item.Value) == 0 || json.Unmarshal(item.Value, &value) != nil {
		return false, false
	}
	return value, true
}
func stringValue(item Setting) (string, bool) {
	var value string
	if len(item.Value) == 0 || json.Unmarshal(item.Value, &value) != nil {
		return "", false
	}
	return value, true
}
func resultMessage(err error, success string) string {
	if err != nil {
		return err.Error()
	}
	return success
}

func SortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// testSMTP opens a connection and greets the relay without sending anything.
func testSMTP(ctx context.Context, values map[string]any) error {
	config, err := mail.ConfigFromValues(values)
	if err != nil {
		return err
	}
	return mail.Verify(ctx, config)
}

// validateAnalytics reuses the analytics rules so the settings screen reports
// a missing measurement id before a page is served without tracking.
func validateAnalytics(items map[string]Setting) string {
	values := make(map[string]any, len(items))
	for key, item := range items {
		var decoded any
		if err := json.Unmarshal(item.Value, &decoded); err == nil {
			values[key] = decoded
		}
	}
	if err := analytics.ReadConfig(values).Validate(); err != nil {
		return err.Error()
	}
	return ""
}
