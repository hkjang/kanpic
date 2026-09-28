package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"kanpic/internal/mail"
	"kanpic/internal/workbook"
)

// cancelAwareRepository 는 프로덕션 저장소가 취소된 컨텍스트에서 어떻게
// 움직이는지를 메모리 저장소 위에 되살린다. 운영에서 쓰는 PostgresRepository 는
// pgx 풀을 거치고, 풀의 Acquire 는 질의에 닿기도 전에 ctx.Done() 을 먼저 보므로
// (puddle/v2@v2.2.2 pool.go:338-344) 요청이 끊긴 뒤 요청 컨텍스트로 저장소를
// 부르면 무조건 오류다. MemoryRepository 는 ctx 를 아예 보지 않아 그 자리를 그냥
// 지나가니, 이 대역 없이는 "알림 채비가 요청 컨텍스트에 매달려 있다" 는 결함을
// 테스트가 볼 수 없다.
type cancelAwareRepository struct {
	*workbook.MemoryRepository
}

func (r cancelAwareRepository) SheetWatchRules(ctx context.Context, sheetID string) ([]workbook.WatchRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.MemoryRepository.SheetWatchRules(ctx, sheetID)
}

func (r cancelAwareRepository) GetWorkbook(ctx context.Context, workbookID string) (workbook.Workbook, error) {
	if err := ctx.Err(); err != nil {
		return workbook.Workbook{}, err
	}
	return r.MemoryRepository.GetWorkbook(ctx, workbookID)
}

func (r cancelAwareRepository) GetWorkbookSharing(ctx context.Context, workbookID string) (workbook.WorkbookSharing, error) {
	if err := ctx.Err(); err != nil {
		return workbook.WorkbookSharing{}, err
	}
	return r.MemoryRepository.GetWorkbookSharing(ctx, workbookID)
}

func (r cancelAwareRepository) LookupUsers(ctx context.Context, ids []string) ([]workbook.UserSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.MemoryRepository.LookupUsers(ctx, ids)
}

// mailValues 는 관리자 콘솔이 값을 담아 두는 곳의 자리를 메운다. 프로덕션
// 구현(settings.Repository)도 pgxpool 로 값을 읽으므로 취소된 컨텍스트에서는
// 실패한다.
type mailValues map[string]any

func (m mailValues) Values(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[string]any(m), nil
}

// captureSender 는 전송만 대신한다. 실제 SMTP 배선은 internal/mail 의
// mail_test.go 가 in-process relay 로 이미 덮으므로, 여기서 볼 것은 "요청이
// 끊긴 뒤에도 수신자가 구해져 Notify 까지 닿는가" 하나다.
type captureSender struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (c *captureSender) send(_ context.Context, _ mail.Config, message mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, message)
	return nil
}

// recipients 는 want 통이 잡힐 때까지 짧게 기다린다 — Notify 는 수신자마다
// 고루틴을 띄우므로(mail/service.go:99) 곧바로 보면 아직 비어 있을 수 있다.
func (c *captureSender) recipients(want int) []string {
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		addresses := make([]string, 0, len(c.sent))
		for _, message := range c.sent {
			addresses = append(addresses, message.To)
		}
		c.mu.Unlock()
		if len(addresses) >= want || time.Now().After(deadline) {
			slices.Sort(addresses)
			return addresses
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *captureSender) subjects() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	subjects := make([]string, 0, len(c.sent))
	for _, message := range c.sent {
		subjects = append(subjects, message.Subject)
	}
	return subjects
}

// newNotifyServer 는 알림 경로만 배선한 Server 를 만든다. mail.Service 는 pool
// 없이 돌고(record·complete 는 nil 풀에서 조용히 빠져나간다) 수신자 조회는
// 프로덕션 어댑터 NewMailDirectory 를 그대로 탄다.
func newNotifyServer(t *testing.T) (*Server, cancelAwareRepository, *captureSender) {
	t.Helper()
	memory := workbook.NewMemoryRepository()
	repository := cancelAwareRepository{MemoryRepository: memory}
	for id, email := range map[string]string{"alice": "alice@corp.example", "park": "park@corp.example", "lee": "lee@corp.example"} {
		if _, err := memory.UpsertUser(context.Background(), workbook.UpsertUserInput{UserID: id, Email: email, ActorID: "admin"}); err != nil {
			t.Fatalf("디렉터리 준비: %v", err)
		}
	}
	sender := &captureSender{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := mail.NewService(nil, mailValues{
		"mail.enabled":      true,
		"mail.smtp_host":    "relay.corp.example",
		"mail.from_address": "kanpic@corp.example",
	}, NewMailDirectory(repository), logger)
	service.SetSender(sender.send)
	return &Server{repository: repository, mail: service, logger: logger}, repository, sender
}

// cancelledRequestContext 는 브라우저가 저장 직후 떠난 뒤의 요청 컨텍스트다 —
// 변경은 이미 커밋됐고 남은 것은 알림뿐이다.
func cancelledRequestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// 칸 저장 알림은 요청이 취소돼도 나가야 한다. 지켜보기 규칙을 읽는 일이 요청
// 컨텍스트에 매달려 있으면 mail.Notify 에 닿지도 못한다.
func TestNotifyWatchersSendsAfterRequestCancel(t *testing.T) {
	t.Parallel()
	server, repository, sender := newNotifyServer(t)
	book, err := repository.CreateWorkbook(context.Background(), workbook.CreateWorkbookInput{Title: "예산", OwnerID: "alice"})
	if err != nil {
		t.Fatalf("워크북 준비: %v", err)
	}
	sheetID := book.Sheets[0].ID
	if _, err := repository.CreateWatchRule(context.Background(), book.ID, "park", workbook.CreateWatchRuleInput{
		IdempotencyKey: "watch-1", SheetID: sheetID, Watcher: "park", Range: "B2:B9", Label: "매출",
	}); err != nil {
		t.Fatalf("지켜보기 규칙 준비: %v", err)
	}

	cells := []workbook.CellInput{{SheetID: sheetID, Row: 2, Column: 2}}
	result := workbook.MutationResult{WorkbookID: book.ID, SheetID: sheetID, AppliedCells: 1}
	server.notifyWatchers(cancelledRequestContext(t), book.ID, sheetID, "alice", cells, result)

	if got := sender.recipients(1); !slices.Equal(got, []string{"park@corp.example"}) {
		t.Fatalf("지켜보던 사람에게 가야 한다: %v", got)
	}
	// 제목이 워크북 이름을 담으려면 GetWorkbook 도 취소에서 떨어져 있어야 한다.
	for _, subject := range sender.subjects() {
		if !strings.Contains(subject, "예산") {
			t.Fatalf("제목에 워크북 이름이 없다: %q", subject)
		}
	}
}

// 접근 요청 알림의 수신자는 workbookAudience 가 구한다. 그것이 취소된
// 컨텍스트에서 nil 을 돌려주면 notifyMail 이 수신자 0명으로 멈춘다.
func TestWorkbookAudienceSurvivesRequestCancel(t *testing.T) {
	t.Parallel()
	server, repository, _ := newNotifyServer(t)
	book, err := repository.CreateWorkbook(context.Background(), workbook.CreateWorkbookInput{Title: "예산", OwnerID: "alice"})
	if err != nil {
		t.Fatalf("워크북 준비: %v", err)
	}
	if _, err := repository.PutWorkbookShare(context.Background(), book.ID, workbook.ShareInput{
		PrincipalType: workbook.PrincipalUser, PrincipalID: "lee", Role: workbook.RoleEditor, ActorID: "alice",
	}); err != nil {
		t.Fatalf("공유 준비: %v", err)
	}

	got, audience := server.workbookAudience(cancelledRequestContext(t), book.ID)
	if got.Title != "예산" {
		t.Fatalf("워크북을 읽어야 한다: %#v", got)
	}
	slices.Sort(audience)
	if !slices.Equal(audience, []string{"alice", "lee"}) {
		t.Fatalf("소유자와 공유받은 사람이 수신자다: %v", audience)
	}
}

// 댓글 알림도 같은 경로다 — 라벨·워크북·수신자를 모두 요청 컨텍스트로 읽는다.
func TestNotifyCommentMailSendsAfterRequestCancel(t *testing.T) {
	t.Parallel()
	server, repository, sender := newNotifyServer(t)
	book, err := repository.CreateWorkbook(context.Background(), workbook.CreateWorkbookInput{Title: "예산", OwnerID: "alice"})
	if err != nil {
		t.Fatalf("워크북 준비: %v", err)
	}
	thread := workbook.CommentThread{
		ID: "thread-1", WorkbookID: book.ID, SheetID: book.Sheets[0].ID, Range: "B2",
		Messages: []workbook.CommentMessage{{ID: "message-1", AuthorID: "park", Content: "확인 부탁드립니다 @lee", Mentions: []string{"lee"}}},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/comments", nil).WithContext(cancelledRequestContext(t))
	request.Header.Set("X-Kanpic-Actor", "park")

	server.notifyCommentMail(request, thread, false)

	if got := sender.recipients(2); !slices.Equal(got, []string{"alice@corp.example", "lee@corp.example"}) {
		t.Fatalf("워크북 관계자와 언급된 사람에게 가야 한다: %v", got)
	}
}
