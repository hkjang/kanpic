// Package mail delivers event notifications through a company SMTP relay.
//
// Internal relays commonly accept mail on port 25 with no credentials and no
// TLS, so authentication and encryption are optional and the transport adapts
// to whatever the server advertises. Nothing here blocks a request: sending
// happens in the background and every attempt is recorded so an administrator
// can see what left the building.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var (
	ErrDisabled = errors.New("mail is disabled")
	ErrInvalid  = errors.New("invalid mail configuration")
)

// Event names, also used as the settings suffix that switches each one off.
const (
	EventShareGranted  = "share.granted"
	EventComment       = "comment.created"
	EventMention       = "comment.mention"
	EventAccessRequest = "access_request.created"
	EventAccessDecided = "access_request.decided"
	EventWatchChanged  = "watch.changed"
	EventTest          = "test"
)

type Config struct {
	Enabled     bool          `json:"enabled"`
	Host        string        `json:"host"`
	Port        int           `json:"port"`
	Username    string        `json:"-"`
	Password    string        `json:"-"`
	FromAddress string        `json:"from_address"`
	FromName    string        `json:"from_name"`
	Security    string        `json:"security"`
	SkipVerify  bool          `json:"skip_verify"`
	BaseURL     string        `json:"base_url"`
	Timeout     time.Duration `json:"-"`
	Events      map[string]bool
}

// Address is the RFC 5322 From header value.
func (c Config) Address() string {
	from := strings.TrimSpace(c.FromAddress)
	if name := strings.TrimSpace(c.FromName); name != "" {
		return fmt.Sprintf("%s <%s>", name, from)
	}
	return from
}

func (c Config) endpoint() string { return net.JoinHostPort(c.Host, fmt.Sprint(c.Port)) }

// Allows reports whether an event should be delivered. Unknown events are sent,
// so adding a notification never requires a settings change first.
func (c Config) Allows(event string) bool {
	if enabled, known := c.Events[event]; known {
		return enabled
	}
	return true
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("%w: mail.smtp_host is required", ErrInvalid)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: mail.smtp_port must be between 1 and 65535", ErrInvalid)
	}
	if !strings.Contains(c.FromAddress, "@") {
		return fmt.Errorf("%w: mail.from_address must be an email address", ErrInvalid)
	}
	switch c.Security {
	case "auto", "none", "starttls", "tls":
	default:
		return fmt.Errorf("%w: mail.security must be auto, none, starttls, or tls", ErrInvalid)
	}
	return nil
}

type Message struct {
	To      string
	Subject string
	Body    string
}

// Deliver opens a connection and sends one message. It is exported so the
// settings screen can prove the relay works before anything depends on it.
func Deliver(ctx context.Context, config Config, message Message) error {
	if err := config.validate(); err != nil {
		return err
	}
	// 수신자는 여기서 **한 번만** 다듬고, 봉투(RCPT TO)와 To 헤더가 그 같은
	// 값을 쓴다. 봉투 쪽만 trim 하면 끝에 CRLF 가 붙은 주소에서 헤더 블록이
	// To 에서 끝나 Subject 이하가 전부 본문 글자가 되는데, Rcpt 는 trim 후를
	// 보므로 통과하고 Deliver 는 nil 을 돌려준다 — 아무도 모르게 깨진다.
	// Message 는 값으로 받으므로 이 갱신은 호출자에게 새지 않는다.
	to := strings.TrimSpace(message.To)
	if to == "" {
		return fmt.Errorf("%w: recipient is required", ErrInvalid)
	}
	// 주소 가운데의 CR/LF 는 헤더 주입이라 다듬어 넘길 수 없다. net/smtp 의
	// validateLine 에 맡기면 릴레이에 붙은 뒤에야 영어 오류가 나오므로,
	// 연결 전에 저장소 관례대로 한국어로 거절한다.
	if strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("%w: 수신자 주소에 줄바꿈 문자를 쓸 수 없습니다", ErrInvalid)
	}
	message.To = to
	client, err := dial(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := startSession(client, config); err != nil {
		return err
	}
	if err := client.Mail(strings.TrimSpace(config.FromAddress)); err != nil {
		return fmt.Errorf("MAIL FROM 실패: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return fmt.Errorf("RCPT TO 실패: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA 실패: %w", err)
	}
	if _, err := writer.Write([]byte(compose(config, message))); err != nil {
		return fmt.Errorf("본문 전송 실패: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("본문 종료 실패: %w", err)
	}
	return client.Quit()
}

// Verify performs the handshake without sending anything, which is what the
// settings connection test needs.
func Verify(ctx context.Context, config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	client, err := dial(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := startSession(client, config); err != nil {
		return err
	}
	return client.Quit()
}

func dial(ctx context.Context, config Config) (*smtp.Client, error) {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	if config.Security == "tls" {
		connection, err := tls.DialWithDialer(dialer, "tcp", config.endpoint(), config.tlsConfig())
		if err != nil {
			return nil, fmt.Errorf("SMTP TLS 연결 실패: %w", err)
		}
		client, err := smtp.NewClient(connection, config.Host)
		if err != nil {
			_ = connection.Close()
			return nil, fmt.Errorf("SMTP 세션 시작 실패: %w", err)
		}
		return client, nil
	}
	connection, err := dialer.DialContext(ctx, "tcp", config.endpoint())
	if err != nil {
		return nil, fmt.Errorf("SMTP 연결 실패: %w", err)
	}
	client, err := smtp.NewClient(connection, config.Host)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SMTP 세션 시작 실패: %w", err)
	}
	return client, nil
}

// startSession upgrades and authenticates only as far as the relay allows, so
// an unauthenticated internal relay works with the same settings as a hosted
// provider that demands both.
func startSession(client *smtp.Client, config Config) error {
	if err := client.Hello(helloName(config)); err != nil {
		return fmt.Errorf("EHLO 실패: %w", err)
	}
	if config.Security == "starttls" || config.Security == "auto" {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(config.tlsConfig()); err != nil {
				return fmt.Errorf("STARTTLS 실패: %w", err)
			}
		} else if config.Security == "starttls" {
			return fmt.Errorf("%w: 서버가 STARTTLS를 지원하지 않습니다", ErrInvalid)
		}
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil
	}
	supported, mechanisms := client.Extension("AUTH")
	if !supported {
		return fmt.Errorf("%w: 서버가 인증을 지원하지 않습니다. 사용자 이름을 비우고 사용하세요", ErrInvalid)
	}
	if strings.Contains(strings.ToUpper(mechanisms), "PLAIN") {
		return client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host))
	}
	if strings.Contains(strings.ToUpper(mechanisms), "LOGIN") {
		return client.Auth(loginAuth{username: config.Username, password: config.Password, host: config.Host})
	}
	return client.Auth(smtp.CRAMMD5Auth(config.Username, config.Password))
}

func (c Config) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.SkipVerify} //nolint:gosec // opt-in for internal relays with private certificates
}

// helloName keeps the EHLO name to the sender domain, which relays that check
// the greeting are happier with than a container hostname.
func helloName(config Config) string {
	if index := strings.LastIndex(config.FromAddress, "@"); index >= 0 && index+1 < len(config.FromAddress) {
		return config.FromAddress[index+1:]
	}
	return "localhost"
}

// loginAuth implements the LOGIN mechanism that several corporate relays use
// instead of PLAIN. The standard library only ships PLAIN and CRAM-MD5.
type loginAuth struct{ username, password, host string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && server.Name != a.host {
		return "", nil, errors.New("LOGIN 인증은 신뢰할 수 있는 서버에서만 사용합니다")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimRight(string(fromServer), ": ")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("알 수 없는 LOGIN 요청: %s", fromServer)
}
