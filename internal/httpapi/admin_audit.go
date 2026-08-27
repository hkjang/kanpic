package httpapi

import (
	"net/http"
	"strings"
)

// 관리자 콘솔에서 한 일을 기록으로 남긴다.
//
// 계정 정지, 역할 부여, 소유권 이전 같은 것은 사람에게 영향을 준다. 그런데
// 누가 언제 했는지가 어디에도 남지 않았다. 부서 관리자에게 권한을 나눠 준
// 뒤로는 더 그렇다 — 나눠 주고 무엇을 했는지 볼 수 없으면 위임이 아니라
// 방치다.
//
// 새 저장소를 만들지 않는다. 서버 로그가 이미 PostgreSQL 에 쌓이고, 기간으로
// 거를 수 있고, CSV 로 내보낼 수 있다. 감사에서 달라고 하는 것이 그것이다.
//
// 메시지를 "admin.action <이름>" 으로 맞춘다. 로그 화면의 검색이 메시지를
// 훑으므로 admin.action 만 적으면 관리자 행위만 모아 볼 수 있다.
const adminActionPrefix = "admin.action "

// recordAdminAction 은 관리자 행위 한 줄을 남긴다.
//
// 실패한 시도도 남길 수 있게 부르는 쪽에서 결과를 정한다. 성공만 남기면
// "정지시키려다 막힌" 흔적이 사라진다.
func (s *Server) recordAdminAction(r *http.Request, action, target string, details ...any) {
	if s.logger == nil {
		return
	}
	actor := strings.TrimSpace(actorID(r))
	if actor == "" {
		actor = "(알 수 없음)"
	}
	// 전역 관리자가 한 일인지 위임받은 부서 관리자가 한 일인지 갈라 적는다.
	// 감사에서 먼저 묻는 것이 "이 사람이 그럴 권한이 있었는가" 이다.
	scope := "global"
	if !s.isGlobalAdmin(r) {
		scope = "department"
	}
	attributes := []any{"actor", actor, "target", strings.TrimSpace(target), "scope", scope}
	attributes = append(attributes, details...)
	s.logger.Info(adminActionPrefix+action, attributes...)
}
