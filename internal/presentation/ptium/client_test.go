package ptium

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kanpic/internal/presentation"
)

func clientFor(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(Config{BaseURL: server.URL, APIKey: "ptium_test", Timeout: 5 * time.Second}, nil)
}

// 실패가 사용자에게 무엇을 말하는지는 보안 문제이기도 하다. 워크북을 읽을 수
// 있다는 것이 사내 서비스 주소를 알아도 된다는 뜻은 아니다.
func TestUpstreamFailureKeepsTheAddressOutOfTheMessage(t *testing.T) {
	t.Parallel()
	// 아무도 받지 않는 주소. 전송 자체가 실패한다.
	client := New(Config{BaseURL: "http://127.0.0.1:1", APIKey: "ptium_test", Timeout: time.Second}, nil)
	_, err := client.Create(context.Background(), presentation.CreateRequest{Deck: presentation.Deck{Title: "덱"}})
	if err == nil {
		t.Fatal("a dead service was not reported as a failure")
	}
	if !errors.Is(err, presentation.ErrUpstream) {
		t.Fatalf("err=%v is not an upstream failure", err)
	}
	var upstream *presentation.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("err=%v carries no user message", err)
	}
	if strings.Contains(upstream.UserMessage(), "127.0.0.1") || strings.Contains(upstream.UserMessage(), "http") {
		t.Fatalf("the message shows the service address: %q", upstream.UserMessage())
	}
	// 운영자는 주소가 있어야 고칠 수 있다.
	if !strings.Contains(upstream.Error(), "127.0.0.1") {
		t.Fatalf("the log detail lost the address: %q", upstream.Error())
	}
}

func TestUpstreamStatusesSayWhoseProblemItIs(t *testing.T) {
	t.Parallel()
	for name, probe := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"a rejected key":        {http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`, "인증을 거부했습니다"},
		"a fault on their side": {http.StatusInternalServerError, `{"error":{"message":"boom"}}`, "오류가 발생했습니다"},
		"a rejected request":    {http.StatusUnprocessableEntity, `{"error":{"message":"the selected template does not exist"}}`, "the selected template does not exist"},
	} {
		client := clientFor(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(probe.status)
			_, _ = w.Write([]byte(probe.body))
		})
		_, err := client.Templates(context.Background())
		var upstream *presentation.UpstreamError
		if !errors.As(err, &upstream) {
			t.Fatalf("%s: err=%v", name, err)
		}
		if !strings.Contains(upstream.UserMessage(), probe.want) {
			t.Fatalf("%s: message=%q, want it to mention %q", name, upstream.UserMessage(), probe.want)
		}
		// 열쇠가 거절당한 사정을 사용자가 알 필요는 없다.
		if probe.status == http.StatusUnauthorized && strings.Contains(upstream.UserMessage(), "invalid api key") {
			t.Fatalf("%s: message repeated the service's own words: %q", name, upstream.UserMessage())
		}
	}
}

// 덱을 만드는 것은 초안 만들기와 소스 컴파일 두 번의 호출이다. 두 번째가
// 실패하면 첫 번째가 남긴 빈 덱이 있으므로, 성공했다고 말하면 안 된다.
func TestCreateFailsWhenTheSlidesCannotBeCompiled(t *testing.T) {
	t.Parallel()
	client := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"deck-1","title":"덱","status":"draft"}}`))
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"message":"source produced no slides"}}`))
	})
	if _, err := client.Create(context.Background(), presentation.CreateRequest{Deck: presentation.Deck{Title: "덱"}}); err == nil {
		t.Fatal("a deck with no slides was reported as made")
	}
}

func TestCreateAppliesTheDeckAsSource(t *testing.T) {
	t.Parallel()
	applied := ""
	client := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"deck-1","title":"부서별 매출","status":"draft"}}`))
			return
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		applied = string(body)
		_, _ = w.Write([]byte(`{"data":{"applied":true,"warnings":["한 줄 줄였습니다"],"presentation":{"id":"deck-1","title":"부서별 매출","status":"completed","slideCount":3,"templateName":"기본"}}}`))
	})
	result, err := client.Create(context.Background(), presentation.CreateRequest{Deck: presentation.Deck{
		Title: "부서별 매출", Slides: []presentation.Slide{{Kind: presentation.SlideCover, Title: "부서별 매출"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "deck-1" || result.SlideCount != 3 || result.Template != "기본" {
		t.Fatalf("result=%+v", result)
	}
	// 서비스가 무엇을 바꿨는지는 삼키지 않는다.
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings=%v", result.Warnings)
	}
	if !strings.Contains(applied, "# 부서별 매출") {
		t.Fatalf("the deck was not sent as source: %s", applied)
	}
}

// 목록에서 "편집기에서 열기" 를 누르면 고칠 수 있는 화면이 떠야 한다.
// /presentations/{id} 까지만 적어 보내면 보기 화면이 열려서, 고치려고
// 누른 사람이 고칠 수가 없다. 화면은 떴으니 무엇이 잘못됐는지도 알기
// 어렵다.
func TestEditURLPointsAtTheEditor(t *testing.T) {
	t.Parallel()
	client := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"deck 1","title":"제목","status":"draft"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"applied":true,"presentation":{"id":"deck 1","title":"제목","status":"completed","slideCount":1,"templateName":"기본"}}}`))
	})
	result, err := client.Create(context.Background(), presentation.CreateRequest{Deck: presentation.Deck{
		Title: "제목", Slides: []presentation.Slide{{Kind: presentation.SlideCover, Title: "제목"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(result.EditURL, "/editor") {
		t.Fatalf("edit url = %q, /editor 로 끝나야 한다", result.EditURL)
	}
	// 아이디에 그대로 쓸 수 없는 글자가 있어도 주소가 깨지지 않아야 한다.
	if !strings.Contains(result.EditURL, "/presentations/deck%201/editor") {
		t.Fatalf("edit url = %q", result.EditURL)
	}
}
