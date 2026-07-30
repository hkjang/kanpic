package settings

import (
	"context"
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
)

type Setting struct {
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value,omitempty"`
	ValueType   string          `json:"value_type"`
	Description string          `json:"description,omitempty"`
	Secret      bool            `json:"secret"`
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
	{Key: "auth.oidc.client_id", Value: json.RawMessage(`"kanpic"`), ValueType: "string", Description: "PKCE Public Client ID"},
	{Key: "auth.oidc.ca_pem", Value: json.RawMessage(`""`), ValueType: "string", Description: "사내 CA 인증서 PEM", Secret: true},
	{Key: "auth.oidc.scopes", Value: json.RawMessage(`["openid","profile","email"]`), ValueType: "string_list", Description: "OIDC Scope"},
	{Key: "auth.oidc.admin_roles", Value: json.RawMessage(`["kanpic-admin"]`), ValueType: "string_list", Description: "관리자 권한으로 인정할 Keycloak Role"},
	{Key: "auth.session_hours", Value: json.RawMessage(`8`), ValueType: "number", Description: "로그인 세션 유지 시간"},
	{Key: "server.public_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "프록시 외부 공개 URL (비우면 요청 Host 사용)"},
	{Key: "files.max_import_mb", Value: json.RawMessage(`20`), ValueType: "number", Description: "Import 파일 최대 크기"},
	{Key: "ai.enabled", Value: json.RawMessage(`false`), ValueType: "boolean", Description: "AI 기능 사용"},
	{Key: "ai.gateway_url", Value: json.RawMessage(`""`), ValueType: "string", Description: "사내 OpenAI 호환 LLM Gateway"},
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
			item.Value = nil
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
	return item, tx.Commit(ctx)
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
	return ValidationResult{Valid: len(issues) == 0, Issues: issues}, nil
}

func (r *Repository) Test(ctx context.Context) ([]TestResult, error) {
	results := make([]TestResult, 0, 2)
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
	return ""
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
