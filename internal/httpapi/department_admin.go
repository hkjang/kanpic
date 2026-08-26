package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/auth"
)

// 부서 관리자는 자기 부서와 그 아래 부서의 구성원 계정만 다룬다.
//
// 열어 주는 것: 구성원 목록 보기, 계정 정지·해제, 역할 부여·회수, 세션 종료.
// 열지 않는 것: 워크북 소유권 이전, 개요, 설정·로그·API 키·AI 정책·메일.
//
// 소유권 이전을 열지 않는 이유는 그것이 자료를 옮기는 일이기 때문이다.
// 잘못 옮기면 조용히 사고가 나고, 되돌리려면 누가 무엇을 가졌는지 다시
// 맞춰야 한다. 개요를 열지 않는 이유는 조직 전체 규모가 새어 나가기
// 때문이다 — 부서 관리자는 자기 부서만 알면 된다.

// managedByActor 는 이 요청을 보낸 사람이 맡은 구성원을 낸다. 전역 관리자면
// 두 번째 값이 true 이고 목록은 뜻이 없다.
func (s *Server) managedByActor(r *http.Request) (map[string]struct{}, bool) {
	if s.repository == nil {
		return nil, false
	}
	actor := strings.TrimSpace(actorID(r))
	if actor == "" {
		return nil, false
	}
	members, err := s.repository.ManagedMembers(r.Context(), actor)
	if err != nil || len(members) == 0 {
		return nil, false
	}
	scope := make(map[string]struct{}, len(members))
	for _, member := range members {
		scope[strings.ToLower(strings.TrimSpace(member))] = struct{}{}
	}
	return scope, true
}

// requireMemberAdmin 은 전역 관리자이거나, 그 사용자를 맡은 부서 관리자일
// 때만 통과시킨다.
//
// 자기 자신은 맡은 사람에 들어 있어도 다룰 수 없다. 스스로 정지를 풀거나
// 역할을 얹을 수 있으면 위임이 아니라 승격이다.
func (s *Server) requireMemberAdmin(w http.ResponseWriter, r *http.Request, targetUserID string) bool {
	if s.isGlobalAdmin(r) {
		return true
	}
	scope, ok := s.managedByActor(r)
	if !ok {
		return s.requireAdmin(w, r)
	}
	target := strings.ToLower(strings.TrimSpace(targetUserID))
	// 맡은 구성원 중에 전역 관리자가 섞여 있을 수 있다. 그 사람을 정지시키거나
	// 세션을 끊으면 관리자를 콘솔에서 잠가 버리는 것이 된다. 부서 하나를
	// 맡겼을 뿐인데 조직의 관리 자체를 멈출 수 있으면 위임이 아니다.
	if s.targetIsGlobalAdmin(r, targetUserID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{
			"code": "forbidden", "message": "관리자 계정은 부서 관리자가 다룰 수 없습니다.",
		}})
		return false
	}
	if _, managed := scope[target]; !managed || strings.EqualFold(target, strings.TrimSpace(actorID(r))) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{
			"code": "forbidden", "message": "이 사용자는 맡은 부서의 구성원이 아닙니다.",
		}})
		return false
	}
	return true
}

// refuseAdminRoleDelegation 은 부서 관리자가 관리자 역할을 건드리는 것을
// 막는다.
//
// 막지 않으면 부서 관리자가 구성원에게 kanpic-admin 을 얹어 전역 관리자를
// 하나 만들어 낼 수 있다. 그것은 위임이 아니라 승격이고, 자기 자신을 막아
// 둔 것도 이 한 걸음으로 우회된다.
//
// 회수도 함께 막는다. 남의 관리자 권한을 떼는 것 역시 맡은 범위 밖이다.
func (s *Server) refuseAdminRoleDelegation(w http.ResponseWriter, r *http.Request, role string) bool {
	if s.isGlobalAdmin(r) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(role))
	adminRoles := []string{auth.DefaultAdminRole}
	if s.auth != nil {
		adminRoles = s.auth.AdminRoles(r.Context())
	}
	for _, candidate := range adminRoles {
		if strings.ToLower(strings.TrimSpace(candidate)) != name {
			continue
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{
			"code": "forbidden", "message": "부서 관리자는 관리자 역할을 부여하거나 회수할 수 없습니다.",
		}})
		return true
	}
	return false
}

// targetIsGlobalAdmin 은 다루려는 사람이 전역 관리자인지 본다.
//
// 부여된 kanpic 역할이 관리자 역할에 드는지 본다. 관리자 역할 이름은
// 신원 공급자 설정에서 오므로 그쪽에 물어 맞춘다.
//
// 헷갈릴 때는 관리자 쪽으로 본다 — 아닌 쪽으로 잘못 보면 관리자를 잠글 수
// 있고, 맞는 쪽으로 잘못 보면 부서 관리자가 한 사람을 못 다룰 뿐이다.
func (s *Server) targetIsGlobalAdmin(r *http.Request, userID string) bool {
	name := strings.TrimSpace(userID)
	if name == "" {
		return false
	}
	if s.repository == nil {
		return false
	}
	user, err := s.repository.GetUser(r.Context(), name)
	if err != nil {
		return false
	}
	adminRoles := []string{auth.DefaultAdminRole}
	if s.auth != nil {
		adminRoles = s.auth.AdminRoles(r.Context())
	}
	for _, held := range user.Roles {
		for _, candidate := range adminRoles {
			if strings.EqualFold(strings.TrimSpace(held), strings.TrimSpace(candidate)) {
				return true
			}
		}
	}
	return false
}

// isGlobalAdmin 은 문을 열지 않고 전역 관리자인지만 본다. requireAdmin 은
// 아닐 때 403 을 적어 버리므로, 부서 관리자를 먼저 살펴야 하는 자리에서는
// 이쪽을 쓴다.
func (s *Server) isGlobalAdmin(r *http.Request) bool {
	recorder := silentWriter{}
	return s.requireAdmin(&recorder, r)
}

// silentWriter 는 requireAdmin 이 적는 거절을 삼키는 응답기다. 실제 응답에는
// 아무것도 나가지 않는다.
type silentWriter struct{ header http.Header }

func (w *silentWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *silentWriter) Write(data []byte) (int, error) { return len(data), nil }
func (w *silentWriter) WriteHeader(int)                {}
