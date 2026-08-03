package httpapi

import (
	"fmt"
	"net/http"

	"kanpic/internal/auth"
	"strings"

	"kanpic/internal/workbook"
)

// The user directory is administrator-only: it decides who may sign in at all
// and which kanpic roles role-based sharing can target.

// lookupUsers resolves identifiers to display names for any signed-in user, so
// comments, mentions, presence and sharing can show a person instead of a raw
// identifier. It never exposes account status or administrative notes.
func (s *Server) lookupUsers(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query != "" {
		items, err := s.repository.SearchUsers(r.Context(), query, 10)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	raw := strings.Split(r.URL.Query().Get("ids"), ",")
	items, err := s.repository.LookupUsers(r.Context(), raw)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// adminOverview and governedWorkbooks give administrators a view across every
// workbook, including the ones they do not own, so over-sharing and orphaned
// data can be found and fixed.
func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	overview, err := s.repository.AdminOverview(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	policy := s.sharingPolicy(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"overview": overview, "policy": policy})
}

func (s *Server) governedWorkbooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := s.repository.GovernedWorkbooks(r.Context(), strings.TrimSpace(r.URL.Query().Get("filter")), 200)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := s.repository.ListUsers(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	adminRoles := []string{auth.DefaultAdminRole}
	if s.auth != nil {
		adminRoles = s.auth.AdminRoles(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "admin_roles": adminRoles, "default_admin_role": auth.DefaultAdminRole})
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	user, err := s.repository.GetUser(r.Context(), r.PathValue("userId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input workbook.UpsertUserInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	user, err := s.repository.UpsertUser(r.Context(), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input workbook.UpdateUserInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	userID := r.PathValue("userId")
	// Suspending yourself would lock the last administrator out of the console.
	if input.Status != nil && *input.Status == workbook.UserStatusSuspended && strings.EqualFold(userID, actorID(r)) {
		s.writeError(w, r, workbook.ErrInvalid)
		return
	}
	user, err := s.repository.UpdateUser(r.Context(), userID, input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	// A suspended account keeps no active sessions.
	if input.Status != nil && *input.Status == workbook.UserStatusSuspended && s.auth != nil {
		_ = s.auth.RevokeUserSessions(r.Context(), user.UserID)
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) grantUserRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	user, err := s.repository.GrantUserRole(r.Context(), r.PathValue("userId"), input.Role, actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) revokeUserRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userID, role := r.PathValue("userId"), r.PathValue("role")
	// Dropping your own administrator role would lock you out of the console.
	if strings.EqualFold(userID, actorID(r)) && s.auth != nil && s.auth.HasAdminRole(r.Context(), []string{role}) {
		s.writeError(w, r, fmt.Errorf("%w: 자신의 관리자 역할은 회수할 수 없습니다", workbook.ErrInvalid))
		return
	}
	user, err := s.repository.RevokeUserRole(r.Context(), userID, role)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// revokeUserSessions signs a user out of every browser, which an administrator
// needs when a device is lost or a role changes.
func (s *Server) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	userID := r.PathValue("userId")
	if _, err := s.repository.GetUser(r.Context(), userID); err != nil {
		s.writeError(w, r, err)
		return
	}
	count := 0
	if s.auth != nil {
		revoked, err := s.auth.RevokeUserSessionsCount(r.Context(), userID)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		count = revoked
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "revoked_sessions": count})
}
