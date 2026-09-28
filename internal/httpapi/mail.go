package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kanpic/internal/mail"
	"kanpic/internal/workbook"
)

// mailDirectory resolves account identifiers to addresses using the user
// directory that already backs sharing and mentions.
type mailDirectory struct{ repository workbook.Repository }

func (d mailDirectory) LookupEmails(ctx context.Context, ids []string) (map[string]string, error) {
	users, err := d.repository.LookupUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	addresses := make(map[string]string, len(users))
	for _, user := range users {
		email := strings.TrimSpace(user.Email)
		if email == "" {
			continue
		}
		addresses[strings.ToLower(strings.TrimSpace(user.UserID))] = email
		addresses[strings.ToLower(email)] = email
	}
	return addresses, nil
}

// NewMailDirectory adapts a workbook repository for the mail service.
func NewMailDirectory(repository workbook.Repository) interface {
	LookupEmails(context.Context, []string) (map[string]string, error)
} {
	return mailDirectory{repository: repository}
}

// actorLabel is the display name people recognise, falling back to a readable
// part of the identifier so a mail never shows a bare uuid or long address.
func (s *Server) actorLabel(ctx context.Context, id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "알 수 없는 사용자"
	}
	if users, err := s.repository.LookupUsers(ctx, []string{trimmed}); err == nil {
		for _, user := range users {
			if strings.EqualFold(strings.TrimSpace(user.UserID), trimmed) && strings.TrimSpace(user.DisplayName) != "" {
				return strings.TrimSpace(user.DisplayName)
			}
		}
	}
	if at := strings.Index(trimmed, "@"); at > 0 {
		return trimmed[:at]
	}
	return trimmed
}

// notifyContext 는 알림을 **채비할 때** 쓰는 컨텍스트다. 값은 그대로 두고 취소만
// 떼어 낸다.
//
// 브라우저가 칸 저장·댓글 등록 직후 떠나면 요청 컨텍스트는 곧 취소된다. 그런데
// 누구에게 보낼지를 정하는 일(지켜보기 규칙·워크북·공유 목록·사람 이름 읽기)은
// 모두 저장소를 거치고, pgx 는 취소된 컨텍스트에서 질의에 닿기도 전에 실패한다
// (풀의 Acquire 가 ctx.Done() 을 먼저 본다). 그러면 변경은 커밋됐는데 알림만
// 조용히 사라진다 — 수신자를 못 구했으니 mail.Notify 는 불리지도 않는다.
//
// mail.Service.Notify 도 안에서 같은 일을 하지만(mail/service.go:82) 그것은 수신자가
// 이미 정해진 다음이라 이 자리를 구제하지 못한다. 그래서 수신자를 구하는 이
// 경계에서 한 번 더 떼어 낸다. 취소를 떼면 남는 것이 없어 요청이 무한정 기다릴 수
// 있으니 시한을 같이 둔다 — triggerCellAutomationsContext(automations.go:249)와 같은
// 자다. 여기서 세는 일은 저장소 읽기 몇 번이고 실제 전송은 Notify 의 고루틴이 제
// 컨텍스트로 하므로 이 시한에 걸리지 않는다.
func notifyContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// notifyMail sends one event mail when the mailer is configured. Every caller
// is on a request path, so failures never surface to the user.
func (s *Server) notifyMail(ctx context.Context, notification mail.Notification, actorID string, recipients []string) {
	if s.mail == nil || len(recipients) == 0 {
		return
	}
	s.mail.Notify(ctx, notification, actorID, recipients)
}

// workbookAudience is everyone with a personal stake in a workbook: the owner
// and the people it is shared with directly.
//
// 이 자리는 알림에만 쓰이고 워크북을 못 읽으면 수신자 0명으로 돌아서므로,
// 부르는 쪽이 요청 컨텍스트를 그대로 넘겨도 알림이 사라지지 않게 여기서도 취소를
// 떼어 낸다(notifyContext 의 설명 참고). 부르는 쪽에서 이미 떼었어도 값만 물려
// 받으므로 손해가 없다.
func (s *Server) workbookAudience(ctx context.Context, workbookID string) (workbook.Workbook, []string) {
	ctx, cancel := notifyContext(ctx)
	defer cancel()
	book, err := s.repository.GetWorkbook(ctx, workbookID)
	if err != nil {
		return workbook.Workbook{}, nil
	}
	recipients := []string{book.OwnerID}
	if sharing, err := s.repository.GetWorkbookSharing(ctx, workbookID); err == nil {
		for _, share := range sharing.Shares {
			if share.PrincipalType == workbook.PrincipalUser {
				recipients = append(recipients, share.PrincipalID)
			}
		}
	}
	return book, recipients
}

func (s *Server) adminMailDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.mail == nil {
		writeJSON(w, http.StatusOK, mail.Page{Items: []mail.Delivery{}, Summary: mail.Summary{Status: map[string]int{}}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := s.mail.Deliveries(r.Context(), r.URL.Query().Get("status"), limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// adminSendTestMail proves the relay works before anybody depends on it.
func (s *Server) adminSendTestMail(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var input struct {
		Recipient string `json:"recipient"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if s.mail == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "mail_unavailable", "message": "메일 서비스가 구성되지 않았습니다."}})
		return
	}
	recipient := strings.TrimSpace(input.Recipient)
	if !strings.Contains(recipient, "@") {
		s.writeError(w, r, workbook.ErrInvalid)
		return
	}
	if err := s.mail.SendNow(r.Context(), mail.TestMessage(), actorID(r), recipient); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "mail_send_failed", "message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "recipient": recipient})
}

// publishCells 는 셀이 바뀌었다는 것을 함께 보고 있는 사람에게 알리고,
// 그 범위를 지켜보겠다고 해 둔 사람에게 메일을 보낸다.
//
// 둘을 한 자리에 묶은 까닭은, 셀을 바꾸는 자리가 스물한 곳이라 하나씩
// 붙이면 새 경로를 더할 때마다 한쪽을 빠뜨리기 때문이다. 빠뜨린 경로에서는
// 지켜보겠다고 해 둔 사람에게 아무 말도 가지 않는데, 그것은 조용한 실패다.
func (s *Server) publishCells(ctx context.Context, workbookID, sheetID, actorID, clientID string, cells []workbook.CellInput, result workbook.MutationResult) {
	s.collab.PublishOperation(workbookID, sheetID, actorID, clientID, cells, result)
	s.notifyWatchers(ctx, workbookID, sheetID, actorID, cells, result)
}

// notifyWatchers 는 이번 저장으로 바뀐 칸을 지켜보던 사람에게 알린다.
// 사람마다 한 통이고, 자기가 바꾼 것은 자기에게 보내지 않는다.
func (s *Server) notifyWatchers(ctx context.Context, workbookID, sheetID, actorID string, cells []workbook.CellInput, result workbook.MutationResult) {
	if s.mail == nil || result.Duplicate || len(cells) == 0 {
		return
	}
	ctx, cancel := notifyContext(ctx)
	defer cancel()
	rules, err := s.repository.SheetWatchRules(ctx, sheetID)
	if err != nil || len(rules) == 0 {
		return
	}
	changed := make([]workbook.CellCoordinate, 0, len(cells))
	for _, cell := range cells {
		changed = append(changed, workbook.CellCoordinate{Row: cell.Row, Column: cell.Column})
	}
	notices := workbook.WatchersToNotify(rules, actorID, changed)
	if len(notices) == 0 {
		return
	}
	book, err := s.repository.GetWorkbook(ctx, workbookID)
	if err != nil {
		return
	}
	label := s.actorLabel(ctx, actorID)
	for _, notice := range notices {
		s.notifyMail(ctx, mail.WatchChanged(label, book.Title, workbookID, notice.Label, notice.Ranges, notice.FirstCell, notice.Cells), actorID, []string{notice.Watcher})
	}
}
