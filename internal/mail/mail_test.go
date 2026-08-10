package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
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
	// The subject is encoded and a leading dot inside the body is escaped.
	if !strings.Contains(body, "Subject: =?utf-8?q?") || !strings.Contains(body, "..점으로 시작") {
		t.Fatalf("body=%q", body)
	}
	if !strings.Contains(body, "Content-Type: text/plain; charset=UTF-8") {
		t.Fatalf("missing content type: %q", body)
	}
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
