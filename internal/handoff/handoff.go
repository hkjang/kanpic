// Package handoff 는 사내 서비스끼리 문서를 넘기는 표준(HANDOFF-STANDARD)의
// kanpic 몫이다. 보내는 쪽은 한 번만 쓸 수 있는 5분짜리 표(claim)를 발급하고,
// 표를 들고 온 쪽이 원본을 직접 받아 간다. 받는 쪽은 관리자가 허용 목록에
// 적은 오리진에서만 받아 온다. 서비스끼리 서로의 자격 증명을 들고 있지 않는다.
package handoff

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// ClaimTTL 은 표의 유효 기간이다. 표준은 5분을 넘기지 말라고 한다.
	ClaimTTL = 5 * time.Minute
	// DefaultMaxBytes 는 받는 쪽이 한 문서에서 읽는 크기의 상한이다(표준 기본 25MB).
	DefaultMaxBytes = int64(25 << 20)
	// DefaultTimeout 은 받는 쪽이 한 문서를 기다리는 시간의 상한이다(표준 기본 30초).
	DefaultTimeout = 30 * time.Second
	// SettingPeers 는 허용 목록이 사는 설정 키다. 추적 설정과 같은 자리 —
	// 관리 화면에서 바꾸는 저장된 설정 — 에 둔다. 기본값은 비어 있다.
	SettingPeers = "handoff.peers"
)

// ErrNotFound 는 없는 표·이미 쓴 표·시간이 지난 표를 가리지 않고 같은 답이다.
// 왜 거절됐는지 구별해 주지 않는다.
var ErrNotFound = errors.New("handoff claim not found")

// Sends 는 kanpic 이 표준의 형식 표에서 보내기로 맡은 형식이다.
var Sends = []string{"csv", "xlsx"}

// Receives 는 kanpic 이 받기로 맡은 형식이다.
var Receives = []string{"csv", "xlsx"}

// receivesByService 는 표준의 형식 표 '받는다' 열이다. 받을 수 없는 형식을
// 보내는 단추는 만들지 않으므로, 보낼 곳을 고를 때 이 표를 본다.
var receivesByService = map[string][]string{
	"umm":    nil,
	"muni":   {"markdown"},
	"kanpic": {"csv", "xlsx"},
	"ptium":  {"markdown", "docx", "csv", "xlsx", "txt"},
	"weekly": {"markdown", "docx", "pptx"},
}

// contentTypes 는 형식마다 표에 적고 받을 때 확인하는 Content-Type 이다.
var contentTypes = map[string]string{
	"csv":  "text/csv",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

// ContentType 은 형식의 미디어 타입이다. 모르는 형식이면 빈 문자열이다.
func ContentType(format string) string { return contentTypes[format] }

// FormatOf 는 응답의 Content-Type 을 받을 수 있는 형식으로 돌린다. 매개변수
// (charset 등)는 보지 않는다. 받을 수 없으면 빈 문자열이다.
func FormatOf(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	for format, candidate := range contentTypes {
		if mediaType == candidate {
			return format
		}
	}
	return ""
}

// Peer 는 허용 목록의 한 줄이다. 설정에는 "ptium=https://ptium.intra" 꼴로
// 적는다 — 이름이 있어야 그 서비스가 무엇을 받는지 표준의 표에서 찾는다.
// 이름 없이 오리진만 적으면 받기만 허용하고 보낼 곳으로는 보이지 않는다.
type Peer struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

// Accepts 는 이 서비스가 그 형식을 받을 수 있는지다.
func (p Peer) Accepts(format string) bool {
	for _, candidate := range receivesByService[p.Name] {
		if candidate == format {
			return true
		}
	}
	return false
}

// Target 은 보내기 단추에 보일 한 곳이다. Formats 는 kanpic 이 보내고 그쪽이
// 받는 형식의 교집합이다.
type Target struct {
	Name    string   `json:"name"`
	Origin  string   `json:"origin"`
	Formats []string `json:"formats"`
}

// ParsePeers 는 설정 값(string_list)을 허용 목록으로 읽는다. 잘못 적은 줄은
// 조용히 빼지 않고 목록에 넣지 않는다 — 없는 줄은 아무것도 허용하지 않는다.
func ParsePeers(value any) []Peer {
	var lines []string
	switch typed := value.(type) {
	case []string:
		lines = typed
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				lines = append(lines, text)
			}
		}
	case string:
		lines = strings.Split(typed, ",")
	}
	peers := make([]Peer, 0, len(lines))
	for _, line := range lines {
		name, origin := "", strings.TrimSpace(line)
		if index := strings.Index(origin, "="); index >= 0 {
			name, origin = strings.ToLower(strings.TrimSpace(origin[:index])), strings.TrimSpace(origin[index+1:])
		}
		normalized, ok := NormalizeOrigin(origin)
		if !ok {
			continue
		}
		peers = append(peers, Peer{Name: name, Origin: normalized})
	}
	return peers
}

// NormalizeOrigin 은 주소를 scheme://host[:port] 만 남긴 오리진으로 만든다.
// 경로·질의·자격 증명이 붙어 있거나 http(s) 가 아니면 오리진이 아니다.
func NormalizeOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", false
	}
	return scheme + "://" + strings.ToLower(parsed.Host), true
}

// Allowed 는 밖에서 들어온 source 가 허용 목록에 있는 오리진인지다. 정확히
// 같은 오리진만 통한다 — 하위 도메인도, 다른 포트도, 다른 스킴도 아니다.
func Allowed(peers []Peer, source string) (Peer, bool) {
	normalized, ok := NormalizeOrigin(source)
	if !ok {
		return Peer{}, false
	}
	for _, peer := range peers {
		if peer.Origin == normalized {
			return peer, true
		}
	}
	return Peer{}, false
}

// Targets 는 허용 목록에서 보낼 곳을 고른다. kanpic 이 보내는 형식 가운데
// 그 서비스가 받는 것이 하나도 없으면 단추가 없다.
func Targets(peers []Peer) []Target {
	targets := []Target{}
	for _, peer := range peers {
		formats := []string{}
		for _, format := range Sends {
			if peer.Accepts(format) {
				formats = append(formats, format)
			}
		}
		if len(formats) == 0 {
			continue
		}
		targets = append(targets, Target{Name: peer.Name, Origin: peer.Origin, Formats: formats})
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

// Ticket 은 표 하나가 가리키는 문서다. 표를 만들 때 문서를 미리 뽑아 함께
// 둔다 — 그래야 표에 적은 bytes 가 실제로 내줄 것과 같고, 받아 가는 쪽이
// 누른 사람이 본 그 상태를 받는다.
type Ticket struct {
	Resource    string
	Format      string
	Filename    string
	ContentType string
	Data        []byte
	IssuedBy    string
	ExpiresAt   time.Time
}

// Receipt 는 받는 쪽이 들어온 문서에 남기는 출처다. 슬라이드가 어느 캔버스에서
// 나왔는지 나중에 물어볼 수 있어야 한다.
type Receipt struct {
	WorkbookID string    `json:"workbook_id"`
	Source     string    `json:"source"`
	Filename   string    `json:"filename"`
	ReceivedBy string    `json:"received_by"`
	ReceivedAt time.Time `json:"received_at"`
}

// Store 는 표와 출처를 둔다. 표는 원문이 아니라 해시로만 찾으므로 저장소가
// 새어도 표가 새지 않는다.
type Store interface {
	// Put 은 표를 둔다. 같은 해시가 이미 있으면 오류다.
	Put(ctx context.Context, hash string, ticket Ticket) error
	// Take 는 표를 꺼내면서 지운다. 없거나 지났거나 이미 쓴 표는 ErrNotFound.
	Take(ctx context.Context, hash string, now time.Time) (Ticket, error)
	SaveReceipt(ctx context.Context, receipt Receipt) error
	// ReceiptFor 는 워크북의 출처다. 넘겨받은 것이 아니면 ErrNotFound.
	ReceiptFor(ctx context.Context, workbookID string) (Receipt, error)
}

// NewClaim 은 표 원문과 그 해시를 만든다. 표는 128비트 이상 난수여야 한다 —
// 여기서는 256비트를 URL 에 실을 수 있는 글자로 적는다.
func NewClaim() (claim, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	claim = base64.RawURLEncoding.EncodeToString(raw)
	return claim, HashClaim(claim), nil
}

// HashClaim 은 표 원문을 저장소의 자리 이름으로 바꾼다.
func HashClaim(claim string) string {
	sum := sha256.Sum256([]byte(claim))
	return hex.EncodeToString(sum[:])
}

// MemoryStore 는 시험과 단일 프로세스용이다.
type MemoryStore struct {
	mutex    sync.Mutex
	tickets  map[string]Ticket
	receipts map[string]Receipt
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tickets: map[string]Ticket{}, receipts: map[string]Receipt{}}
}

func (s *MemoryStore) Put(_ context.Context, hash string, ticket Ticket) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, exists := s.tickets[hash]; exists {
		return errors.New("handoff claim already exists")
	}
	s.tickets[hash] = ticket
	return nil
}

func (s *MemoryStore) Take(_ context.Context, hash string, now time.Time) (Ticket, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	ticket, found := s.tickets[hash]
	if !found {
		return Ticket{}, ErrNotFound
	}
	delete(s.tickets, hash)
	if !now.Before(ticket.ExpiresAt) {
		return Ticket{}, ErrNotFound
	}
	return ticket, nil
}

func (s *MemoryStore) SaveReceipt(_ context.Context, receipt Receipt) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.receipts[receipt.WorkbookID] = receipt
	return nil
}

func (s *MemoryStore) ReceiptFor(_ context.Context, workbookID string) (Receipt, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	receipt, found := s.receipts[workbookID]
	if !found {
		return Receipt{}, ErrNotFound
	}
	return receipt, nil
}
