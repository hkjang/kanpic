package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kanpic/internal/handoff"
	"kanpic/internal/workbook"
)

type handoffClaim struct {
	Claim       string    `json:"claim"`
	Source      string    `json:"source"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Bytes       int       `json:"bytes"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// handoffServer 는 alice 의 워크북 하나를 가진 서버다. 요청 로그는 버퍼에
// 모아 표가 로그에 남지 않는지 볼 수 있게 한다.
func handoffServer(t *testing.T, peers []string, clock func() time.Time) (*httptest.Server, *handoff.Service, workbook.Workbook, *bytes.Buffer) {
	t.Helper()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "3분기 실적", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"부서"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"영업1"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`120`)},
	}}); err != nil {
		t.Fatal(err)
	}
	values := fixedSettings{}
	if peers != nil {
		values[handoff.SettingPeers] = peers
	}
	service := handoff.NewService(values, handoff.NewMemoryStore())
	if clock != nil {
		service.WithClock(clock)
	}
	logs := &bytes.Buffer{}
	handler := NewPlatformWithServices(repository, nil, nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(logs, nil)), WithHandoff(service))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, service, book, logs
}

func TestHandoffClaimIsIssuedOnceAndServedOnce(t *testing.T) {
	t.Parallel()
	server, _, book, logs := handoffServer(t, nil, nil)
	issued := requestAs[handoffClaim](t, server, "alice", http.MethodPost, "/api/v1/handoff/claims", map[string]any{"resource": book.ID, "format": "csv"}, http.StatusCreated)
	if len(issued.Claim) < 22 || issued.Source != server.URL || !strings.HasSuffix(issued.Filename, ".csv") || issued.ContentType != "text/csv; charset=utf-8" || issued.Bytes == 0 {
		t.Fatalf("issued=%+v", issued)
	}
	if until := time.Until(issued.ExpiresAt); until <= 0 || until > handoff.ClaimTTL {
		t.Fatalf("expires_at=%s", issued.ExpiresAt)
	}

	// 표를 내주는 자리에는 로그인이 없다 — 행위자 헤더도 없이 부른다.
	response := doRequest(t, server, "", http.MethodGet, "/api/v1/handoff/claims/"+issued.Claim)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/csv; charset=utf-8" || len(body) != issued.Bytes {
		t.Fatalf("status=%d type=%q bytes=%d want %d", response.StatusCode, response.Header.Get("Content-Type"), len(body), issued.Bytes)
	}
	if !strings.Contains(response.Header.Get("Content-Disposition"), "filename*=UTF-8''") || !strings.Contains(string(body), "영업1,120") {
		t.Fatalf("disposition=%q body=%q", response.Header.Get("Content-Disposition"), body)
	}
	// 두 번째 요청은 404.
	again := doRequest(t, server, "", http.MethodGet, "/api/v1/handoff/claims/"+issued.Claim)
	again.Body.Close()
	if again.StatusCode != http.StatusNotFound {
		t.Fatalf("second use = %d", again.StatusCode)
	}
	// 표가 로그에 남지 않는다.
	if strings.Contains(logs.String(), issued.Claim) {
		t.Fatalf("the claim leaked into the request log:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "/api/v1/handoff/claims/{claim}") {
		t.Fatalf("the request was not logged at all:\n%s", logs.String())
	}
}

func TestHandoffClaimExpires(t *testing.T) {
	t.Parallel()
	now := time.Now()
	server, _, book, _ := handoffServer(t, nil, func() time.Time { return now })
	issued := requestAs[handoffClaim](t, server, "alice", http.MethodPost, "/api/v1/handoff/claims", map[string]any{"resource": book.ID, "format": "xlsx"}, http.StatusCreated)
	if issued.ContentType != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("content_type=%q", issued.ContentType)
	}
	now = now.Add(handoff.ClaimTTL + time.Second)
	response := doRequest(t, server, "", http.MethodGet, "/api/v1/handoff/claims/"+issued.Claim)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expired claim = %d", response.StatusCode)
	}
}

// 남의 문서로는 표가 만들어지지 않고, 받을 수 없는 형식으로도 만들어지지 않는다.
func TestHandoffClaimIsBoundToWhatTheUserMayRead(t *testing.T) {
	t.Parallel()
	server, _, book, _ := handoffServer(t, nil, nil)
	requestAs[map[string]any](t, server, "mallory", http.MethodPost, "/api/v1/handoff/claims", map[string]any{"resource": book.ID, "format": "csv"}, http.StatusForbidden)
	requestAs[map[string]any](t, server, "alice", http.MethodPost, "/api/v1/handoff/claims", map[string]any{"resource": "no-such-workbook", "format": "csv"}, http.StatusNotFound)
	requestAs[map[string]any](t, server, "alice", http.MethodPost, "/api/v1/handoff/claims", map[string]any{"resource": book.ID, "format": "pptx"}, http.StatusBadRequest)
}

// 허용 목록이 비어 있으면 보낼 곳이 없다 — 단추가 보이지 않는다. 받을 수
// 있는 형식이 맞는 서비스만 보낼 곳이다.
func TestHandoffTargetsFollowTheAllowList(t *testing.T) {
	t.Parallel()
	type targets struct {
		Source  string           `json:"source"`
		Targets []handoff.Target `json:"targets"`
	}
	empty, _, _, _ := handoffServer(t, nil, nil)
	if got := requestAs[targets](t, empty, "alice", http.MethodGet, "/api/v1/handoff/targets", nil, http.StatusOK); len(got.Targets) != 0 {
		t.Fatalf("targets=%+v", got.Targets)
	}
	server, _, _, _ := handoffServer(t, []string{"ptium=https://ptium.intra", "muni=https://muni.intra"}, nil)
	got := requestAs[targets](t, server, "alice", http.MethodGet, "/api/v1/handoff/targets", nil, http.StatusOK)
	if len(got.Targets) != 1 || got.Targets[0].Name != "ptium" || strings.Join(got.Targets[0].Formats, ",") != "csv,xlsx" {
		t.Fatalf("targets=%+v", got.Targets)
	}
}

// 두 kanpic 사이에서 한쪽이 보내고 다른 쪽이 연다. 받는 쪽은 워크북을 만들어
// 그리로 넘기고, 어디서 왔는지를 남긴다.
func TestHandoffIsReceivedIntoAWorkbookThatRemembersItsSource(t *testing.T) {
	t.Parallel()
	sender, _, book, _ := handoffServer(t, nil, nil)
	receiver, service, _, _ := handoffServer(t, []string{"kanpic=" + sender.URL}, nil)
	issued := requestAs[handoffClaim](t, sender, "alice", http.MethodPost, "/api/v1/handoff/claims", map[string]any{"resource": book.ID, "format": "csv"}, http.StatusCreated)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, _ := http.NewRequest(http.MethodGet, receiver.URL+"/handoff?source="+sender.URL+"&claim="+issued.Claim, nil)
	request.Header.Set("X-Kanpic-Actor", "bob")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location := response.Header.Get("Location")
	if response.StatusCode != http.StatusFound || !strings.HasPrefix(location, "/workbooks/") {
		t.Fatalf("status=%d location=%q", response.StatusCode, location)
	}
	workbookID := strings.TrimPrefix(location, "/workbooks/")
	received := requestAs[workbook.Workbook](t, receiver, "bob", http.MethodGet, "/api/v1/workbooks/"+workbookID, nil, http.StatusOK)
	if received.OwnerID != "bob" || !strings.HasPrefix(received.Title, "3분기 실적") {
		t.Fatalf("received=%+v", received)
	}
	cells := requestAs[struct {
		Items []workbook.Cell `json:"items"`
	}](t, receiver, "bob", http.MethodGet, "/api/v1/sheets/"+received.Sheets[0].ID+"/ranges/A1:B2", nil, http.StatusOK)
	if len(cells.Items) != 4 {
		t.Fatalf("cells=%+v", cells.Items)
	}
	origin := requestAs[handoff.Receipt](t, receiver, "bob", http.MethodGet, "/api/v1/workbooks/"+workbookID+"/handoff", nil, http.StatusOK)
	if origin.Source != sender.URL || origin.ReceivedBy != "bob" || !strings.HasSuffix(origin.Filename, ".csv") {
		t.Fatalf("origin=%+v", origin)
	}
	if stored, err := service.Origin(context.Background(), workbookID); err != nil || stored.Source != sender.URL {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	// 표는 이미 쓰였다 — 같은 주소를 다시 열면 사람이 읽을 수 있는 화면이다.
	response, _ = client.Do(request)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode == http.StatusFound || !strings.Contains(string(body), "만료되었거나 이미 쓰였습니다") {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
}

// 허용 목록에 없는 source 는 요청을 보내지 않고 거절한다.
func TestHandoffAsksNothingOfASourceOutsideTheAllowList(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("a,b\n"))
	}))
	t.Cleanup(stranger.Close)
	receiver, _, _, _ := handoffServer(t, []string{"ptium=https://ptium.intra"}, nil)
	response := doRequest(t, receiver, "bob", http.MethodGet, "/handoff?source="+stranger.URL+"&claim=anything")
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "허용 목록에 없는") {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if hits.Load() != 0 {
		t.Fatalf("the stranger was asked %d times", hits.Load())
	}
	// 허용 목록이 비어 있으면 아무 데서도 받지 않는다.
	empty, _, _, _ := handoffServer(t, nil, nil)
	refused := doRequest(t, empty, "bob", http.MethodGet, "/handoff?source="+stranger.URL+"&claim=anything")
	refused.Body.Close()
	if refused.StatusCode != http.StatusBadGateway || hits.Load() != 0 {
		t.Fatalf("status=%d hits=%d", refused.StatusCode, hits.Load())
	}
}
