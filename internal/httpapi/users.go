package httpapi

import (
	"fmt"
	"net/http"

	"kanpic/internal/auth"
	"kanpic/internal/formula"
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

// ownedWorkbooks 는 한 사람이 가진 워크북을 모두 낸다. 퇴사자를 정리하려면
// "이 사람이 무엇을 가지고 있는가" 를 먼저 알아야 한다.
func (s *Server) ownedWorkbooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := s.repository.WorkbooksOwnedBy(r.Context(), r.PathValue("userId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	trashed := 0
	for _, item := range items {
		if item.DeletedAt != nil {
			trashed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "trashed": trashed})
}

// transferOwnedWorkbooks 는 한 사람이 가진 것을 한꺼번에 넘긴다.
//
// 워크북마다 하나씩 넘길 수는 이미 있었다. 그런데 퇴사자가 마흔 개를
// 가지고 있으면 마흔 번을 눌러야 하고, 몇 개를 빠뜨렸는지는 아무도 모른다.
//
// 넘긴 것과 못 넘긴 것을 하나하나 돌려준다. "다 됐습니다" 하고 절반만
// 넘어간 것이 가장 나쁘다 — 남은 절반은 정지된 계정에 묶인 채로 잊힌다.
func (s *Server) transferOwnedWorkbooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		NewOwnerID   string `json:"new_owner_id"`
		KeepAsEditor bool   `json:"keep_as_editor"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	previous := strings.TrimSpace(r.PathValue("userId"))
	next := strings.TrimSpace(input.NewOwnerID)
	if next == "" {
		s.writeError(w, r, fmt.Errorf("%w: new owner is required", workbook.ErrInvalid))
		return
	}
	if strings.EqualFold(previous, next) {
		s.writeError(w, r, fmt.Errorf("%w: the new owner is the current owner", workbook.ErrInvalid))
		return
	}
	// 정지된 사람에게 넘기면 받은 사람도 손댈 수 없다. 옮긴 것이 아니라
	// 옮긴 척한 것이 되므로 미리 막는다.
	if receiver, err := s.repository.GetUser(r.Context(), next); err == nil && receiver.Status == workbook.UserStatusSuspended {
		s.writeError(w, r, fmt.Errorf("%w: the new owner is suspended", workbook.ErrInvalid))
		return
	}
	items, err := s.repository.WorkbooksOwnedBy(r.Context(), previous)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	transferred := make([]map[string]any, 0)
	failed := make([]map[string]any, 0)
	skipped := 0
	for _, item := range items {
		// 휴지통에 있는 것은 넘기지 않는다. 되살리기 전에는 손댈 수 없고,
		// 넘기려 들면 오류만 쌓인다. 몇 개인지는 세어서 알린다.
		if item.DeletedAt != nil {
			skipped++
			continue
		}
		transfer := workbook.TransferOwnershipInput{NewOwnerID: next, KeepAsEditor: input.KeepAsEditor, ActorID: actorID(r)}
		if _, transferErr := s.repository.TransferWorkbookOwnership(r.Context(), item.ID, transfer); transferErr != nil {
			failed = append(failed, map[string]any{"id": item.ID, "title": item.Title, "reason": transferErr.Error()})
			continue
		}
		transferred = append(transferred, map[string]any{"id": item.ID, "title": item.Title})
	}
	s.recordAdminAction(r, "workbooks.transfer", previous,
		"new_owner", next, "transferred", len(transferred), "failed", len(failed), "skipped_trashed", skipped)
	writeJSON(w, http.StatusOK, map[string]any{
		"transferred": transferred, "failed": failed, "skipped_trashed": skipped,
	})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	scope, scoped := s.managedByActor(r)
	if !s.isGlobalAdmin(r) && !scoped && !s.requireAdmin(w, r) {
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
	// 부서 관리자에게는 맡은 구성원만 보인다. 조직 전체 명부가 보이면
	// 위임한 범위 밖의 사람까지 알게 된다.
	if scoped && !s.isGlobalAdmin(r) {
		kept := make([]workbook.DirectoryUser, 0, len(scope))
		names := s.adminRoleNames(r)
		for _, item := range items {
			if _, managed := scope[strings.ToLower(strings.TrimSpace(item.UserID))]; !managed {
				continue
			}
			// 관리자 계정은 목록에서도 뺀다. 열어 보면 막히는데 목록에는
			// 보이면, 관리자 명단을 훑는 것은 그대로 되는 셈이다.
			if holdsAdminRole(item.Roles, names) {
				continue
			}
			kept = append(kept, item)
		}
		items = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "admin_roles": adminRoles, "default_admin_role": auth.DefaultAdminRole, "scoped": scoped && !s.isGlobalAdmin(r)})
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireMemberAdmin(w, r, r.PathValue("userId")) {
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
	s.recordAdminAction(r, "user.create", user.UserID)
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireMemberAdmin(w, r, r.PathValue("userId")) {
		return
	}
	var input workbook.UpdateUserInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	userID := r.PathValue("userId")
	// 알림 메일은 디렉터리에 적힌 이메일로 간다. 부서 관리자가 그것을 바꿀
	// 수 있으면 구성원의 알림을 조용히 자기 쪽으로 돌릴 수 있다 — 지켜보기
	// 알림에는 바뀐 칸이, 멘션에는 댓글이 실려 나간다. 받는 사람은 메일이
	// 끊긴 것을 한참 뒤에야 안다.
	if input.Email != nil && !s.isGlobalAdmin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{
			"code": "forbidden", "message": "부서 관리자는 이메일을 바꿀 수 없습니다.",
		}})
		return
	}
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
	if input.Status != nil {
		s.recordAdminAction(r, "user.status", userID, "status", *input.Status)
	} else {
		s.recordAdminAction(r, "user.update", userID)
	}
	// A suspended account keeps no active sessions.
	if input.Status != nil && *input.Status == workbook.UserStatusSuspended && s.auth != nil {
		_ = s.auth.RevokeUserSessions(r.Context(), user.UserID)
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) grantUserRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireMemberAdmin(w, r, r.PathValue("userId")) {
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if s.refuseAdminRoleDelegation(w, r, input.Role) {
		return
	}
	user, err := s.repository.GrantUserRole(r.Context(), r.PathValue("userId"), input.Role, actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.recordAdminAction(r, "user.role.grant", r.PathValue("userId"), "role", input.Role)
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) revokeUserRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireMemberAdmin(w, r, r.PathValue("userId")) {
		return
	}
	userID, role := r.PathValue("userId"), r.PathValue("role")
	if s.refuseAdminRoleDelegation(w, r, role) {
		return
	}
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
	s.recordAdminAction(r, "user.role.revoke", userID, "role", role)
	writeJSON(w, http.StatusOK, user)
}

// revokeUserSessions signs a user out of every browser, which an administrator
// needs when a device is lost or a role changes.
func (s *Server) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	if !s.requireMemberAdmin(w, r, r.PathValue("userId")) {
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
	s.recordAdminAction(r, "user.sessions.revoke", r.PathValue("userId"))
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "revoked_sessions": count})
}

// listFormulaFunctions publishes the function catalog so the editor can show
// what the engine supports instead of leaving people to discover #NAME?.
func (s *Server) listFormulaFunctions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": formula.Catalog()})
}
