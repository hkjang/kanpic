package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

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
	items, err := s.logs.List(r.Context(), r.URL.Query().Get("level"), r.URL.Query().Get("q"), limit)
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
