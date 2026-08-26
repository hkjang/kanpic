package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"kanpic/internal/workbook"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kanpic/internal/observability"
	"kanpic/internal/settings"
)

func (s *Server) versionInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.build)
}

func (s *Server) listSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := s.settings.List(r.Context(), false)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) putSetting(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input settings.Setting
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Key = r.PathValue("key")
	item, err := s.settings.Put(r.Context(), input, actorID(r))
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteSetting(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.settings.Delete(r.Context(), r.PathValue("key"), actorID(r)); err != nil {
		s.platformError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validateSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	result, err := s.settings.Validate(r.Context())
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) testSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx, cancel := timeoutContext(r, 10*time.Second)
	defer cancel()
	results, err := s.settings.Test(ctx)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": results})
}

func (s *Server) listSettingVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := s.settings.Versions(r.Context())
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) restoreSettingVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	action := r.PathValue("versionAction")
	if !strings.HasSuffix(action, ":restore") {
		http.NotFound(w, r)
		return
	}
	revision, err := strconv.ParseInt(strings.TrimSuffix(action, ":restore"), 10, 64)
	if err != nil {
		s.platformError(w, errors.New("invalid revision"))
		return
	}
	if err := s.settings.Restore(r.Context(), revision, actorID(r)); err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored_revision": revision})
}

func (s *Server) getPreferences(w http.ResponseWriter, r *http.Request) {
	item, err := s.settings.GetPreferences(r.Context(), actorID(r))
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) putPreferences(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Values map[string]any `json:"values"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Values == nil {
		input.Values = map[string]any{}
	}
	item, err := s.settings.PutPreferences(r.Context(), actorID(r), input.Values)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	from, to, rangeErr := logRange(r)
	if rangeErr != nil {
		s.platformError(w, rangeErr)
		return
	}
	items, err := s.logs.ListRange(r.Context(), r.URL.Query().Get("level"), r.URL.Query().Get("q"), from, to, limit)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// logRange 는 화면과 내보내기가 함께 쓰는 기간 읽기다. 두 곳이 따로 읽으면
// 감사에 넘긴 파일과 화면에서 본 것이 달라진다.
func logRange(r *http.Request) (time.Time, time.Time, error) {
	parse := func(name string) (time.Time, error) {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			return time.Time{}, nil
		}
		// 날짜만 적으면 그 날의 처음으로 본다. 사람이 2026-01-05 라고 적으면
		// 그 날을 뜻하지 그 날 0시 정각 한 순간을 뜻하지 않는다.
		if len(raw) == len("2006-01-02") {
			return time.Parse("2006-01-02", raw)
		}
		return time.Parse(time.RFC3339, raw)
	}
	from, err := parse("from")
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be 2006-01-02 or RFC3339")
	}
	to, err := parse("to")
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be 2006-01-02 or RFC3339")
	}
	// 끝 날짜만 적으면 그 날이 통째로 들어가야 한다. 그렇지 않으면 마지막
	// 날의 기록이 통째로 빠지고, 그것을 알아채는 사람은 없다.
	if !to.IsZero() && len(strings.TrimSpace(r.URL.Query().Get("to"))) == len("2006-01-02") {
		to = to.Add(24*time.Hour - time.Nanosecond)
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to is before from")
	}
	return from, to, nil
}

// exportLogs 는 거른 기록을 CSV 로 내보낸다.
//
// 감사에서는 "그 기간 기록을 주세요" 라고 한다. 화면은 500건에서 끊기므로
// 지금까지는 화면을 긁는 수밖에 없었다.
//
// 개수를 자르지 않는다. 감사에 넘길 파일이 조용히 잘려 있으면 없느니만
// 못하다 — 받은 사람은 그것이 전부라고 믿는다.
func (s *Server) exportLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.logs == nil {
		s.platformError(w, errors.New("log store is not configured"))
		return
	}
	from, to, rangeErr := logRange(r)
	if rangeErr != nil {
		s.platformError(w, rangeErr)
		return
	}
	name := "kanpic-logs"
	if !from.IsZero() || !to.IsZero() {
		name = fmt.Sprintf("kanpic-logs-%s-%s", stampOrAll(from), stampOrAll(to))
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	writer := csv.NewWriter(w)
	// 엑셀은 BOM 이 없으면 UTF-8 한글을 깨뜨린다. 감사에 넘기는 파일은
	// 받은 사람이 엑셀로 여는 것이 보통이다.
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	_ = writer.Write([]string{"logged_at", "level", "message", "trace_id", "attributes"})
	err := s.logs.Stream(r.Context(), r.URL.Query().Get("level"), r.URL.Query().Get("q"), from, to, func(entry observability.LogEntry) error {
		attributes := ""
		if len(entry.Attributes) > 0 {
			encoded, _ := json.Marshal(entry.Attributes)
			attributes = string(encoded)
		}
		return writer.Write([]string{entry.LoggedAt.UTC().Format(time.RFC3339), entry.Level, entry.Message, entry.TraceID, attributes})
	})
	writer.Flush()
	if err != nil {
		// 여기까지 왔으면 머리글이 이미 나갔으므로 상태 코드를 바꿀 수 없다.
		// 파일 끝에 잘렸다고 적어 둔다 — 조용히 끊기는 것보다 낫다.
		_, _ = w.Write([]byte("\n# 내보내기가 도중에 끊겼습니다: " + err.Error() + "\n"))
	}
}

func stampOrAll(value time.Time) string {
	if value.IsZero() {
		return "all"
	}
	return value.UTC().Format("20060102")
}

func (s *Server) purgeLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		Before time.Time `json:"before"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Before.IsZero() {
		s.platformError(w, errors.New("before is required"))
		return
	}
	count, err := s.logs.PurgeBefore(r.Context(), input.Before)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": count})
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	// A kanpic role granted in the console counts as well as an identity
	// provider role, so the resolved principal decides first.
	if principal, ok := r.Context().Value(principalCacheKey{}).(workbook.AccessPrincipal); ok && principal.Admin {
		return true
	}
	// Until OIDC is enabled, the local bootstrap user is an administrator so a
	// fresh offline installation can be configured. OIDC sessions replace this
	// header-derived bootstrap identity once enabled.
	if principal, ok := apiPrincipal(r); ok {
		if principal.Allows("admin.*") {
			return true
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "forbidden", "message": "관리자 scope가 필요합니다."}})
		return false
	}
	if user, ok := sessionUser(r); ok {
		if s.auth != nil && s.auth.IsAdmin(r.Context(), user) {
			return true
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "forbidden", "message": "Keycloak 관리자 Role이 필요합니다."}})
		return false
	}
	roles := strings.Split(r.Header.Get("X-Kanpic-Role"), ",")
	if actorID(r) == "local-user" {
		return true
	}
	for _, role := range roles {
		if strings.TrimSpace(role) == "admin" || strings.TrimSpace(role) == "kanpic-admin" {
			return true
		}
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "forbidden", "message": "관리자 권한이 필요합니다."}})
	return false
}

func (s *Server) platformError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "platform_error"
	if errors.Is(err, pgx.ErrNoRows) {
		status, code = http.StatusNotFound, "not_found"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": err.Error()}})
}

func timeoutContext(r *http.Request, duration time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), duration)
}

var _ = json.Valid
