package mail

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"kanpic/internal/workbook"
)

// mailSettings 는 관리자 콘솔이 값을 담아 두는 곳의 자리를 메운다. 프로덕션
// 구현은 settings.Repository 이고 pgxpool 로 값을 읽으므로, 이미 취소된
// 컨텍스트로 부르면 질의에 닿기도 전에 puddle 의 Acquire 가 ctx.Err() 를
// 돌려준다(puddle/v2@v2.2.2 pool.go:338-344 — Acquire 첫머리에서 ctx.Done()
// 을 먼저 본다). 취소 갈래를 실제와 같게 보려면 여기서도 ctx 를 먼저 본다.
type mailSettings map[string]any

func (m mailSettings) Values(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[string]any(m), nil
}

// mailDirectory 는 프로덕션 디렉터리 배선을 그대로 탄다 — 사람을 찾는 일은
// 실제 workbook 저장소의 LookupUsers 가 하고, 그 결과를 주소 표로 바꾸는 규칙만
// 여기에 적는다. 유일한 프로덕션 어댑터인 httpapi.mailDirectory
// (internal/httpapi/mail.go:17-32)를 직접 부를 수는 없다 — httpapi 가 mail 을
// import 하므로 패키지 내부 테스트에서 부르면 순환이 된다. 그래서 키 모양을
// 그대로 따른다: 소문자 userID→email 과 소문자 email→email 을 **둘 다** 넣는다.
// 한쪽만 넣으면 실제와 다른 계약을 못 박게 된다.
type mailDirectory struct{ repository *workbook.MemoryRepository }

func (d mailDirectory) LookupEmails(ctx context.Context, ids []string) (map[string]string, error) {
	// 프로덕션 저장소는 PostgresRepository 라 취소된 컨텍스트에서는 질의가
	// 곧바로 실패한다(mailSettings 의 설명과 같은 이유). 여기서 쓰는
	// MemoryRepository 는 ctx 를 보지 않으므로 그 자리를 대신 지킨다 —
	// 설정 읽기만 컨텍스트에서 떼어 내고 수신자 조회는 남겨 두는 반쪽 수정을
	// 이 줄이 걸러 낸다.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

// newTestService 는 실제 Service 를 in-process relay 에 붙인다. pool 은 nil 로
// 둔다 — record·complete 는 pool 이 nil 이면 조용히 빠져나가므로 DB 없이 돈다
// (Deliveries 만은 nil 풀에서 패닉하니 여기서 부르지 않는다). 전송자도 실제
// Deliver 그대로여서 단언은 와이어에 실린 줄을 본다.
func newTestService(t *testing.T, relay *fakeRelay, overrides map[string]any) *Service {
	t.Helper()
	values := map[string]any{
		"mail.enabled":         true,
		"mail.smtp_host":       relay.host(),
		"mail.smtp_port":       float64(relay.port()),
		"mail.from_address":    "kanpic@corp.example",
		"mail.from_name":       "kanpic 알림",
		"mail.security":        "auto",
		"mail.timeout_seconds": float64(3),
	}
	for key, value := range overrides {
		values[key] = value
	}
	repository := workbook.NewMemoryRepository()
	for id, email := range map[string]string{"park": "park@corp.example", "lee": "lee@corp.example", "kim": "kim@corp.example"} {
		if _, err := repository.UpsertUser(context.Background(), workbook.UpsertUserInput{UserID: id, Email: email, ActorID: "admin"}); err != nil {
			t.Fatalf("디렉터리 준비: %v", err)
		}
	}
	// 주소가 없는 사람은 디렉터리에 있어도 주소 표에 들어가지 않는다.
	if _, err := repository.UpsertUser(context.Background(), workbook.UpsertUserInput{UserID: "noaddress", ActorID: "admin"}); err != nil {
		t.Fatalf("디렉터리 준비: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(nil, mailSettings(values), mailDirectory{repository: repository}, logger)
}

// waitForRecipients 는 와이어의 RCPT TO 줄이 want 개가 될 때까지 짧게 기다린
// 뒤 주소를 정렬해 돌려준다. Notify 는 수신자마다 고루틴을 띄우므로
// (service.go:89) 곧바로 보면 아직 아무것도 없을 수 있다.
func waitForRecipients(t *testing.T, relay *fakeRelay, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := recipients(relay)
		if len(got) >= want || time.Now().After(deadline) {
			slices.Sort(got)
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func recipients(relay *fakeRelay) []string {
	addresses := []string{}
	for _, line := range relay.transcript() {
		if rest, found := strings.CutPrefix(line, "RCPT TO:<"); found {
			addresses = append(addresses, strings.TrimSuffix(rest, ">"))
		}
	}
	return addresses
}

// expectNothingSent 는 "안 보낸다" 를 본다. Notify 가 게이트에서 돌아서면
// 고루틴을 띄우지도 않으므로 돌아온 뒤에는 새 연결이 생길 수 없지만, 늦게
// 도착하는 연결이 없는지 짧게 기다려 확인한다.
func expectNothingSent(t *testing.T, relay *fakeRelay) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	if transcript := relay.transcript(); len(transcript) != 0 {
		t.Fatalf("relay 에 아무것도 오지 않아야 하는데 왔다: %q", transcript)
	}
}

// 누가 받는가 — Notify 의 수신자 계약을 와이어의 RCPT TO 줄로 못 박는다.
// resolve 는 알림이 본인에게 되돌아가지 않게 하고, 같은 사람에게 두 번 가지
// 않게 하고, 주소를 모르는 식별자는 조용히 버린다.
func TestNotifyResolvesRecipientsOnTheWire(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		actor      string
		recipients []string
		want       []string
	}{
		{
			name:       "행동한 본인은 대소문자가 달라도 빠진다",
			actor:      "park",
			recipients: []string{"PARK", "lee"},
			want:       []string{"lee@corp.example"},
		},
		{
			name:       "같은 주소로 풀리는 둘은 한 번만 간다",
			actor:      "park",
			recipients: []string{"lee", "LEE@corp.example"},
			want:       []string{"lee@corp.example"},
		},
		{
			name:       "디렉터리가 모르는 식별자는 조용히 빠진다",
			actor:      "park",
			recipients: []string{"ghost", "lee"},
			want:       []string{"lee@corp.example"},
		},
		{
			name:       "디렉터리에 주소가 없는 사람도 빠진다",
			actor:      "park",
			recipients: []string{"noaddress", "lee"},
			want:       []string{"lee@corp.example"},
		},
		{
			name:       "@ 가 든 식별자는 디렉터리 없이 그대로 주소가 된다",
			actor:      "park",
			recipients: []string{"outside@partner.example"},
			want:       []string{"outside@partner.example"},
		},
		{
			name:       "빈 문자열과 공백만 있는 수신자는 빠진다",
			actor:      "park",
			recipients: []string{"", "   ", "lee"},
			want:       []string{"lee@corp.example"},
		},
		{
			name:       "여럿이 남으면 각자에게 한 통씩 간다",
			actor:      "park",
			recipients: []string{"lee", "kim"},
			want:       []string{"kim@corp.example", "lee@corp.example"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			relay := startRelay(t, false)
			service := newTestService(t, relay, nil)
			service.Notify(context.Background(), ShareGranted("박지민", "월간 매출", "wb-1", "editor"), testCase.actor, testCase.recipients)
			if got := waitForRecipients(t, relay, len(testCase.want)); !slices.Equal(got, testCase.want) {
				t.Fatalf("RCPT TO=%q, want %q (전체 transcript=%q)", got, testCase.want, relay.transcript())
			}
		})
	}
}

// 남는 수신자가 없으면 relay 에 연결조차 하지 않는다 — 본인의 행동을 본인에게
// 알리려고 메일 서버를 깨우지 않는다.
func TestNotifySkipsTheRelayWhenOnlyTheActorRemains(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	service := newTestService(t, relay, nil)
	service.Notify(context.Background(), ShareGranted("박지민", "월간 매출", "wb-1", "editor"), "park", []string{"park"})
	expectNothingSent(t, relay)
}

// 관리자가 메일을 아예 끄면 아무것도 나가지 않는다.
func TestNotifySendsNothingWhenMailIsDisabled(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	service := newTestService(t, relay, map[string]any{"mail.enabled": false})
	service.Notify(context.Background(), ShareGranted("박지민", "월간 매출", "wb-1", "editor"), "park", []string{"lee"})
	expectNothingSent(t, relay)
}

// 이벤트 하나를 끄면 그 이벤트만 멈춘다 — 다른 이벤트는 그대로 나간다.
func TestNotifyRespectsOneDisabledEvent(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	service := newTestService(t, relay, map[string]any{"mail.notify_comment": false})
	service.Notify(context.Background(), CommentPosted("박지민", "월간 매출", "wb-1", "B2", "확인 부탁드립니다", false), "park", []string{"lee"})
	expectNothingSent(t, relay)
	service.Notify(context.Background(), ShareGranted("박지민", "월간 매출", "wb-1", "editor"), "park", []string{"lee"})
	if got, want := waitForRecipients(t, relay, 1), []string{"lee@corp.example"}; !slices.Equal(got, want) {
		t.Fatalf("끄지 않은 이벤트는 나가야 한다: RCPT TO=%q, want %q", got, want)
	}
}

// 설정에 키가 아예 없으면 나간다 — Allows 의 기본 허용(mail.go:63-70)이라
// 알림을 하나 더 만들 때 설정부터 손봐야 하는 일이 없다.
func TestNotifyDeliversEventsTheSettingsNeverMention(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	service := newTestService(t, relay, nil)
	service.Notify(context.Background(), WatchChanged("박지민", "월간 매출", "wb-1", "매출", []string{"A1:B9"}, "A1", 3), "park", []string{"lee"})
	if got, want := waitForRecipients(t, relay, 1), []string{"lee@corp.example"}; !slices.Equal(got, want) {
		t.Fatalf("RCPT TO=%q, want %q", got, want)
	}
}

// SendNow 는 이벤트 게이트를 **일부러** 보지 않는다. 관리자 화면의 발송 테스트
// 버튼은 알림을 다 꺼 둔 상태에서도 눌려야 하고, 그래야 관리자가 SMTP 설정을
// 확인할 수 있다. Notify 와 갈리는 이 차이는 의도된 것이다.
func TestSendNowIgnoresTheEventGateOnPurpose(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	service := newTestService(t, relay, map[string]any{
		"mail.notify_comment": false, "mail.notify_share": false, "mail.notify_mention": false,
		"mail.notify_access_request": false, "mail.notify_watch": false,
	})
	if err := service.SendNow(context.Background(), CommentPosted("박지민", "월간 매출", "wb-1", "B2", "확인 부탁드립니다", false), "admin", "lee@corp.example"); err != nil {
		t.Fatalf("send now: %v", err)
	}
	if got, want := recipients(relay), []string{"lee@corp.example"}; !slices.Equal(got, want) {
		t.Fatalf("RCPT TO=%q, want %q", got, want)
	}
}

// 메일이 꺼져 있으면 SendNow 는 조용히 성공하지 않고 그 이유를 돌려준다.
func TestSendNowReportsThatMailIsDisabled(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	service := newTestService(t, relay, map[string]any{"mail.enabled": false})
	if err := service.SendNow(context.Background(), TestMessage(), "admin", "lee@corp.example"); err != ErrDisabled {
		t.Fatalf("error=%v, want %v", err, ErrDisabled)
	}
	expectNothingSent(t, relay)
}

// 요청이 끊겨도 알림은 나가야 한다. 브라우저가 칸 저장·댓글 등록 직후 떠나면
// 요청 컨텍스트는 취소되지만 변경은 이미 커밋됐다 — 그 알림이 조용히 사라지면
// 받는 사람은 바뀐 것을 영원히 모른다.
func TestNotifySendsEvenWhenTheRequestWasCancelled(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	service := newTestService(t, relay, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.Notify(ctx, ShareGranted("박지민", "월간 매출", "wb-1", "editor"), "park", []string{"lee"})
	if got, want := waitForRecipients(t, relay, 1), []string{"lee@corp.example"}; !slices.Equal(got, want) {
		t.Fatalf("취소된 요청의 알림이 사라졌다: RCPT TO=%q, want %q", got, want)
	}
}
