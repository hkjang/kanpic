package handoff

import (
	"context"
	"strings"
	"time"
)

// SettingsProvider 는 관리 화면에서 바꾸는 저장된 설정이다.
type SettingsProvider interface {
	Values(ctx context.Context) (map[string]any, error)
}

// Service 는 표를 발급하고 내주며, 허용된 곳에서 받아 온다.
type Service struct {
	settings SettingsProvider
	store    Store
	options  FetchOptions
	now      func() time.Time
}

// NewService 는 설정과 저장소를 묶는다. settings 가 nil 이면 허용 목록이 비어
// 있는 것과 같다 — 보낼 곳도 없고 받을 곳도 없다.
func NewService(settings SettingsProvider, store Store) *Service {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Service{settings: settings, store: store, now: time.Now}
}

// WithClock 은 시험이 시간을 돌리기 위한 자리다.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// WithFetchOptions 는 받는 쪽의 상한을 바꾼다(시험용 전송 포함).
func (s *Service) WithFetchOptions(options FetchOptions) *Service {
	s.options = options
	return s
}

func (s *Service) values(ctx context.Context) map[string]any {
	if s.settings == nil {
		return nil
	}
	values, err := s.settings.Values(ctx)
	if err != nil {
		return nil
	}
	return values
}

// Peers 는 허용 목록이다. 설정을 읽지 못하면 비어 있다 — 설정 장애가 문을
// 여는 쪽으로 틀리면 안 된다.
func (s *Service) Peers(ctx context.Context) []Peer {
	return ParsePeers(s.values(ctx)[SettingPeers])
}

// Targets 는 보내기 단추에 보일 곳이다.
func (s *Service) Targets(ctx context.Context) []Target { return Targets(s.Peers(ctx)) }

// PublicURL 은 관리자가 적은 이 서비스의 바깥 주소다. 표의 source 에 적는다.
func (s *Service) PublicURL(ctx context.Context) string {
	text, _ := s.values(ctx)["server.public_url"].(string)
	return strings.TrimRight(strings.TrimSpace(text), "/")
}

// Issue 는 문서를 담은 표를 발급한다. 표 원문은 부른 쪽에 한 번 돌려주고
// 저장소에는 해시만 남는다.
func (s *Service) Issue(ctx context.Context, ticket Ticket) (string, Ticket, error) {
	claim, hash, err := NewClaim()
	if err != nil {
		return "", Ticket{}, err
	}
	ticket.ExpiresAt = s.now().Add(ClaimTTL)
	if err := s.store.Put(ctx, hash, ticket); err != nil {
		return "", Ticket{}, err
	}
	return claim, ticket, nil
}

// Redeem 은 표를 문서로 바꾼다. 한 번 받아 가면 끝이다.
func (s *Service) Redeem(ctx context.Context, claim string) (Ticket, error) {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return Ticket{}, ErrNotFound
	}
	return s.store.Take(ctx, HashClaim(claim), s.now())
}

// Fetch 는 허용 목록에 있는 source 에서 표를 받아 온다.
func (s *Service) Fetch(ctx context.Context, source, claim string) (Fetched, error) {
	return Fetch(ctx, s.Peers(ctx), source, claim, s.options)
}

// Record 는 들어온 문서에 출처를 남긴다.
func (s *Service) Record(ctx context.Context, receipt Receipt) error {
	if receipt.ReceivedAt.IsZero() {
		receipt.ReceivedAt = s.now()
	}
	return s.store.SaveReceipt(ctx, receipt)
}

// Origin 은 워크북이 어디서 왔는지다.
func (s *Service) Origin(ctx context.Context, workbookID string) (Receipt, error) {
	return s.store.ReceiptFor(ctx, workbookID)
}
