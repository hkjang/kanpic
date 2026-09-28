package mail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRelay is a minimal SMTP server. It records the conversation so a test can
// assert what kanpic actually said, including whether it tried to authenticate.
type fakeRelay struct {
	address    string
	offerAuth  bool
	rejectFrom bool
	mu         sync.Mutex
	commands   []string
	body       string
	listener   net.Listener
}

func startRelay(t *testing.T, offerAuth bool) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relay := &fakeRelay{address: listener.Addr().String(), offerAuth: offerAuth, listener: listener}
	go relay.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return relay
}

func (f *fakeRelay) host() string { host, _, _ := net.SplitHostPort(f.address); return host }
func (f *fakeRelay) port() int {
	_, port, _ := net.SplitHostPort(f.address)
	value := 0
	_, _ = fmt.Sscanf(port, "%d", &value)
	return value
}

func (f *fakeRelay) record(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, line)
}

func (f *fakeRelay) transcript() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func (f *fakeRelay) serve() {
	for {
		connection, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(connection)
	}
}

func (f *fakeRelay) handle(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }
	write("220 relay.internal ESMTP kanpic-test")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		f.record(command)
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			write("250-relay.internal")
			if f.offerAuth {
				write("250-AUTH PLAIN LOGIN")
			}
			write("250 SIZE 35882577")
		case strings.HasPrefix(upper, "HELO"):
			write("250 relay.internal")
		case strings.HasPrefix(upper, "AUTH"):
			write("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM"):
			if f.rejectFrom {
				write("550 5.7.1 Sender rejected")
				continue
			}
			write("250 2.1.0 Ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			write("250 2.1.5 Ok")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			f.mu.Lock()
			f.body = body.String()
			f.mu.Unlock()
			write("250 2.0.0 Ok: queued")
		case upper == "QUIT":
			write("221 2.0.0 Bye")
			return
		default:
			write("250 2.0.0 Ok")
		}
	}
}

func relayConfig(relay *fakeRelay) Config {
	return Config{Enabled: true, Host: relay.host(), Port: relay.port(), FromAddress: "kanpic@corp.example",
		FromName: "kanpic 알림", Security: "auto", Timeout: 3 * time.Second, Events: map[string]bool{}}
}

// An internal relay that asks for nothing must work with no credentials.
func TestDeliverWithoutAuthentication(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	config := relayConfig(relay)
	err := Deliver(context.Background(), config, Message{To: "park@corp.example", Subject: "공유 알림", Body: "본문입니다.\n.점으로 시작"})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	transcript := strings.Join(relay.transcript(), "\n")
	if !strings.Contains(transcript, "MAIL FROM:<kanpic@corp.example>") || !strings.Contains(transcript, "RCPT TO:<park@corp.example>") {
		t.Fatalf("transcript=%s", transcript)
	}
	if strings.Contains(strings.ToUpper(transcript), "AUTH ") {
		t.Fatal("no credentials were configured, so no AUTH should be attempted")
	}
	relay.mu.Lock()
	body := relay.body
	relay.mu.Unlock()
	if !strings.Contains(body, "Subject: =?utf-8?q?") {
		t.Fatalf("body=%q", body)
	}
	// 줄 앞 점은 와이어에서 이스케이프되지만, 받는 쪽이 RFC 5321 대로
	// 되돌리면 적은 그대로여야 한다. `Contains(body, "..점으로 시작")` 으로
	// 적으면 와이어에 `...점으로 시작` 이 실려도 통과해 이중 이스케이프를
	// 가려내지 못하므로 되돌린 줄과 정확히 비교한다.
	if got, want := undotWireBody(t, body), []string{"본문입니다.", ".점으로 시작"}; !slices.Equal(got, want) {
		t.Fatalf("wire body=%q, want %q (raw=%q)", got, want, body)
	}
	if !strings.Contains(body, "Content-Type: text/plain; charset=UTF-8") {
		t.Fatalf("missing content type: %q", body)
	}
}

// undotWireBody 는 와이어에 실린 DATA 를 받는 쪽이 읽는 모습으로 되돌린다 —
// 헤더를 떼고, RFC 5321 4.5.2 의 투명성 규칙대로 점으로 시작하는 줄에서 점
// 하나를 뺀다. 이스케이프가 두 번 일어났다면 한 번만 되돌린 여기서 점이
// 남아 드러난다.
func undotWireBody(t *testing.T, wire string) []string {
	t.Helper()
	_, body, found := strings.Cut(wire, "\r\n\r\n")
	if !found {
		t.Fatalf("헤더와 본문을 가르는 빈 줄이 없다: %q", wire)
	}
	if !strings.HasSuffix(body, "\r\n") {
		t.Fatalf("본문은 CRLF 로 끝나야 한다: %q", body)
	}
	lines := strings.Split(strings.TrimSuffix(body, "\r\n"), "\r\n")
	for index, line := range lines {
		lines[index] = strings.TrimPrefix(line, ".")
	}
	return lines
}

// 줄 앞 점의 이스케이프는 와이어에서 딱 한 번만 일어나야 한다. compose 가
// 손으로 한 번 붙이고 client.Data() 의 net/textproto dot writer 가 또 붙이면
// 받는 사람은 `.점으로 시작` 을 `..점으로 시작` 으로 본다 — 이 테스트는
// compose 의 반환값이 아니라 실제 relay 에 실린 바이트를 본다.
func TestDeliverEscapesLeadingDotsOnceOnTheWire(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{"점으로 시작하는 줄", "본문입니다.\n.점으로 시작"},
		{"이미 점이 두 개인 줄", "본문입니다.\n..이미 두 점"},
		{"첫 줄이 점 하나", ".\n두 번째 줄"},
		{"마지막 줄이 점 하나", "첫 줄\n."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			relay := startRelay(t, false)
			message := Message{To: "park@corp.example", Subject: "공유 알림", Body: testCase.body}
			if err := Deliver(context.Background(), relayConfig(relay), message); err != nil {
				t.Fatalf("deliver: %v", err)
			}
			relay.mu.Lock()
			wire := relay.body
			relay.mu.Unlock()
			got, want := undotWireBody(t, wire), strings.Split(testCase.body, "\n")
			if !slices.Equal(got, want) {
				t.Fatalf("받는 쪽이 읽는 본문=%q, 적은 본문=%q (와이어=%q)", got, want, wire)
			}
		})
	}
}

// 봉투(RCPT TO)와 헤더(To:)는 같은 주소를 **글자 그대로** 같게 읽어야 한다.
// 봉투 쪽만 trim 하면 끝에 CRLF 가 붙은 주소에서 헤더 블록이 To 에서 끝나고
// Subject·Date·MIME-Version·Content-Type 이 전부 본문 글자가 된다 — 받는
// 사람은 제목 없는 깨진 메일을 보는데 Deliver 는 nil 을 돌려준다. 그래서
// compose 의 반환값이 아니라 실제 relay 에 실린 바이트를 본다.
func TestDeliverUsesTheSameRecipientInEnvelopeAndHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		to   string
	}{
		{"끝에 CRLF", "park@corp.example\r\n"},
		{"끝에 LF", "park@corp.example\n"},
		{"앞뒤 공백", "  park@corp.example\t"},
		{"군더더기 없음", "park@corp.example"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			relay := startRelay(t, false)
			message := Message{To: testCase.to, Subject: "공유 알림", Body: "본문입니다."}
			if err := Deliver(context.Background(), relayConfig(relay), message); err != nil {
				t.Fatalf("deliver: %v", err)
			}
			relay.mu.Lock()
			wire := relay.body
			relay.mu.Unlock()
			headers, body, found := strings.Cut(wire, "\r\n\r\n")
			if !found {
				t.Fatalf("헤더와 본문을 가르는 빈 줄이 없다: %q", wire)
			}
			envelope, header := envelopeRecipient(t, relay.transcript()), headerValue(t, headers, "To")
			if envelope != header {
				t.Fatalf("봉투 수신자=%q, To 헤더=%q — 글자 그대로 같아야 한다 (와이어=%q)", envelope, header, wire)
			}
			if envelope != "park@corp.example" {
				t.Fatalf("수신자=%q, want %q", envelope, "park@corp.example")
			}
			// 헤더 블록이 To 에서 끝나 버리는 것이 이 결함의 증상이다.
			for _, name := range []string{"Subject", "Date", "MIME-Version", "Content-Type"} {
				if headerValue(t, headers, name) == "" {
					t.Fatalf("%s 가 헤더 블록에서 빠졌다 — 본문으로 새어 나갔다 (헤더=%q, 본문=%q)", name, headers, body)
				}
			}
			if want := "본문입니다.\r\n"; body != want {
				t.Fatalf("본문=%q, want %q", body, want)
			}
		})
	}
}

// 주소 가운데의 CR/LF 는 헤더 주입이므로 연결 전에 거절한다. net/smtp 의
// validateLine 에 맡기면 릴레이에 붙은 뒤 영어 오류가 나온다.
func TestDeliverRejectsRecipientWithEmbeddedNewline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		to   string
	}{
		{"LF 로 헤더 주입", "park@corp.example\nBcc: spy@evil.example"},
		{"CR 로 헤더 주입", "park@corp.example\rBcc: spy@evil.example"},
		{"CRLF 로 헤더 주입", "park@corp.example\r\nBcc: spy@evil.example"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			relay := startRelay(t, false)
			message := Message{To: testCase.to, Subject: "공유 알림", Body: "본문입니다."}
			err := Deliver(context.Background(), relayConfig(relay), message)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), "줄바꿈") {
				t.Fatalf("문구가 저장소 관례대로 한국어여야 한다: %v", err)
			}
			if transcript := relay.transcript(); len(transcript) != 0 {
				t.Fatalf("릴레이에 연결조차 없어야 한다: %v", transcript)
			}
		})
	}
}

// envelopeRecipient 는 와이어의 `RCPT TO:<...>` 에서 주소만 꺼낸다.
func envelopeRecipient(t *testing.T, transcript []string) string {
	t.Helper()
	for _, line := range transcript {
		if !strings.HasPrefix(strings.ToUpper(line), "RCPT TO:") {
			continue
		}
		address := strings.TrimSpace(line[len("RCPT TO:"):])
		return strings.TrimSuffix(strings.TrimPrefix(address, "<"), ">")
	}
	t.Fatalf("RCPT TO 가 와이어에 없다: %v", transcript)
	return ""
}

// headerValue 는 헤더 블록에서 이름이 같은 첫 헤더의 값을 돌려준다. 없으면
// 빈 문자열이다 — 헤더가 본문으로 새어 나갔는지 보려면 그 구분이 필요하다.
func headerValue(t *testing.T, headers, name string) string {
	t.Helper()
	for _, line := range strings.Split(headers, "\r\n") {
		if value, found := strings.CutPrefix(line, name+": "); found {
			return value
		}
	}
	return ""
}

// When credentials are set and the relay offers AUTH, kanpic authenticates.
func TestDeliverAuthenticatesWhenConfigured(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, true)
	config := relayConfig(relay)
	config.Username, config.Password = "kanpic", "secret"
	if err := Deliver(context.Background(), config, Message{To: "lee@corp.example", Subject: "알림", Body: "본문"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if !strings.Contains(strings.ToUpper(strings.Join(relay.transcript(), "\n")), "AUTH PLAIN") {
		t.Fatalf("transcript=%v", relay.transcript())
	}
}

// A relay that cannot authenticate is reported clearly instead of silently
// sending nothing.
func TestDeliverExplainsMissingAuthSupport(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	config := relayConfig(relay)
	config.Username, config.Password = "kanpic", "secret"
	err := Deliver(context.Background(), config, Message{To: "lee@corp.example", Subject: "알림", Body: "본문"})
	if err == nil || !strings.Contains(err.Error(), "사용자 이름을 비우고") {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyGreetsWithoutSending(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	if err := Verify(context.Background(), relayConfig(relay)); err != nil {
		t.Fatalf("verify: %v", err)
	}
	transcript := strings.Join(relay.transcript(), "\n")
	if strings.Contains(transcript, "DATA") || strings.Contains(transcript, "RCPT") {
		t.Fatalf("verify should not send a message: %s", transcript)
	}
	if !strings.Contains(transcript, "EHLO corp.example") {
		t.Fatalf("EHLO should use the sender domain: %s", transcript)
	}
}

func TestDeliverReportsRejection(t *testing.T) {
	t.Parallel()
	relay := startRelay(t, false)
	relay.rejectFrom = true
	err := Deliver(context.Background(), relayConfig(relay), Message{To: "lee@corp.example", Subject: "알림", Body: "본문"})
	if err == nil || !strings.Contains(err.Error(), "MAIL FROM") {
		t.Fatalf("error=%v", err)
	}
}

func TestConfigValidationAndDefaults(t *testing.T) {
	t.Parallel()
	// Port 465 means implicit TLS without anybody having to say so.
	config := readValues(map[string]any{"mail.enabled": true, "mail.smtp_host": "relay.internal", "mail.smtp_port": float64(465)})
	if config.Security != "tls" || !config.Enabled {
		t.Fatalf("implicit TLS config=%#v", config)
	}
	// A missing sender falls back to the relay host so the setup stays short.
	if config.FromAddress != "kanpic@relay.internal" {
		t.Fatalf("from=%q", config.FromAddress)
	}
	// Unknown events default to enabled; a disabled one is respected.
	config.Events = map[string]bool{EventComment: false}
	if config.Allows(EventComment) || !config.Allows(EventShareGranted) {
		t.Fatal("event toggles are not applied")
	}
	if err := (Config{Host: "relay", Port: 25, FromAddress: "bad", Security: "auto"}).validate(); err == nil {
		t.Fatal("an address without @ should be rejected")
	}
	if err := (Config{Host: "relay", Port: 25, FromAddress: "a@b", Security: "quantum"}).validate(); err == nil {
		t.Fatal("an unknown security mode should be rejected")
	}
}

func TestNotificationRendersLinkAndFooter(t *testing.T) {
	t.Parallel()
	config := Config{BaseURL: "https://sheet.corp.example/"}
	body := ShareGranted("박지민", "월간 매출", "wb-1", "editor").Render(config)
	if !strings.Contains(body, "https://sheet.corp.example/workbooks/wb-1") {
		t.Fatalf("body=%q", body)
	}
	if !strings.Contains(body, "자동으로 발송되었습니다") {
		t.Fatalf("missing footer: %q", body)
	}
	// Without a base URL the mail still makes sense, just without a link.
	if strings.Contains(ShareGranted("박지민", "월간 매출", "wb-1", "editor").Render(Config{}), "바로 열기") {
		t.Fatal("no link should be offered without a base URL")
	}
}

// A stalled relay drains input but never answers at the selected stage. Cleanup
// closes both sides and joins the server even when the regression test fails.
func startStalledRelay(t *testing.T, stage string) (Config, <-chan error, <-chan struct{}, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	closed := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	var connection net.Conn
	stopping := false
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			ready <- err
			return
		}
		mu.Lock()
		connection = conn
		if stopping {
			_ = conn.Close()
		}
		mu.Unlock()
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if stage == "ehlo" {
			_, err = io.WriteString(conn, "220 stalled.example ESMTP\r\n")
			if err == nil {
				var line string
				line, err = reader.ReadString('\n')
				if err == nil && !strings.HasPrefix(line, "EHLO ") {
					err = fmt.Errorf("expected EHLO, got %q", line)
				}
			}
		}
		ready <- err
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, reader)
		close(closed)
	}()
	stop := func() {
		_ = listener.Close()
		mu.Lock()
		stopping = true
		if connection != nil {
			_ = connection.Close()
		}
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("stalled relay did not stop")
		}
	}
	t.Cleanup(stop)
	relay := &fakeRelay{address: listener.Addr().String()}
	config := relayConfig(relay)
	config.Security = "none"
	if stage == "tls" {
		config.Security = "tls"
	}
	return config, ready, closed, stop
}

func TestDeliverHonorsContext(t *testing.T) {
	testSMTPContext(t, func(ctx context.Context, config Config) error {
		return Deliver(ctx, config, Message{To: "lee@corp.example", Subject: "test", Body: "body"})
	})
}

func TestVerifyHonorsContext(t *testing.T) { testSMTPContext(t, Verify) }

func testSMTPContext(t *testing.T, call func(context.Context, Config) error) {
	t.Helper()
	for _, stage := range []string{"greeting", "ehlo", "tls"} {
		for _, mode := range []string{"deadline", "cancel"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				config, ready, closed, stop := startStalledRelay(t, stage)
				ctx, cancel := context.WithCancel(context.Background())
				if mode == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
				}
				defer cancel()
				result := make(chan error, 1)
				finished := make(chan struct{})
				go func() { defer close(finished); result <- call(ctx, config) }()
				defer func() {
					stop()
					select {
					case <-finished:
					case <-time.After(2 * time.Second):
						t.Error("SMTP call did not stop after relay cleanup")
					}
				}()
				select {
				case err := <-ready:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("relay did not reach stall stage")
				}
				if mode == "cancel" {
					cancel()
				}
				<-ctx.Done()
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("cancelled SMTP session returned nil")
					}
				case <-time.After(500 * time.Millisecond):
					t.Fatal("SMTP session blocked after context cancellation")
				}
				select {
				case <-closed:
				case <-time.After(500 * time.Millisecond):
					t.Fatal("SMTP session left its socket open")
				}
			})
		}
	}
}
