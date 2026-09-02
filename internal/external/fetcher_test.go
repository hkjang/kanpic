package external

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kanpic/internal/formula"
)

type fixedSettings map[string]any

func (f fixedSettings) Values(context.Context) (map[string]any, error) { return f, nil }

// httptest 서버는 루프백에 있다. 시험만 그것을 허용하고, 정책이 루프백을 막는
// 것은 따로 본다.
func testFetcher(settings fixedSettings) *Fetcher {
	fetcher := New(settings, nil)
	fetcher.allowPrivate = true
	return fetcher
}

func serve(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	return server, host
}

func withInsecureTLS(fetcher *Fetcher, server *httptest.Server) *Fetcher {
	// 시험 서버의 자체 서명 인증서를 받아들인다. 운영에서는 시스템 신뢰 저장소를 쓴다.
	fetcher.tlsClient = server.Client()
	return fetcher
}

func TestOffByDefaultAndOnlyAllowedHosts(t *testing.T) {
	server, host := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) })
	request := formula.ExternalRequest{Function: "WEBSERVICE", URL: server.URL + "/x"}
	key := formula.ExternalKey(request.Function, request.URL)

	off := withInsecureTLS(testFetcher(fixedSettings{"external.enabled": false, "external.allowed_hosts": []any{host}}), server)
	if got := off.Resolve(context.Background(), []formula.ExternalRequest{request})[key]; got.Err == nil || !strings.Contains(got.Err.Message, "꺼져") {
		t.Fatalf("꺼져 있으면 거절해야 한다: %+v", got)
	}
	notListed := withInsecureTLS(testFetcher(fixedSettings{"external.enabled": true, "external.allowed_hosts": []any{"api.example.com"}}), server)
	if got := notListed.Resolve(context.Background(), []formula.ExternalRequest{request})[key]; got.Err == nil || !strings.Contains(got.Err.Message, "허용되지 않은 호스트") {
		t.Fatalf("목록에 없는 호스트는 거절해야 한다: %+v", got)
	}
	listed := withInsecureTLS(testFetcher(fixedSettings{"external.enabled": true, "external.allowed_hosts": []any{host}}), server)
	if got := listed.Resolve(context.Background(), []formula.ExternalRequest{request})[key]; got.Err != nil || got.Text != "hello" {
		t.Fatalf("허용된 호스트는 가져와야 한다: %+v", got)
	}
}

func TestPolicyRefusesWhatMustNeverBeReached(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.5.5", "169.254.169.254", "100.64.0.1", "::1", "fe80::1", "0.0.0.0"} {
		if PublicAddress(net.ParseIP(ip)) {
			t.Errorf("%s 은 닿으면 안 된다", ip)
		}
	}
	for _, ip := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946", "8.8.8.8"} {
		if !PublicAddress(net.ParseIP(ip)) {
			t.Errorf("%s 은 공개 주소다", ip)
		}
	}
	if _, err := CheckURL("http://api.example.com/x", []string{"api.example.com"}); err == nil {
		t.Error("http 는 거절해야 한다")
	}
	if _, err := CheckURL("https://user:pw@api.example.com/x", []string{"api.example.com"}); err == nil {
		t.Error("자격 증명이 든 주소는 거절해야 한다")
	}
	if !HostAllowed("data.example.com", []string{"*.example.com"}) || HostAllowed("example.com", []string{"*.example.com"}) || HostAllowed("evil-example.com", []string{"*.example.com"}) {
		t.Error("와일드카드는 아래 호스트만 허용해야 한다")
	}
	if !HostAllowed("API.Example.com.", []string{"api.example.com"}) {
		t.Error("대소문자와 끝점은 무시해야 한다")
	}
	// 루프백을 허용하지 않는 진짜 정책으로 루프백 서버를 부르면 거절돼야 한다.
	server, host := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("secret")) })
	strict := withInsecureTLS(New(fixedSettings{"external.enabled": true, "external.allowed_hosts": []any{host}}, nil), server)
	request := formula.ExternalRequest{Function: "WEBSERVICE", URL: server.URL}
	if got := strict.Resolve(context.Background(), []formula.ExternalRequest{request})[formula.ExternalKey(request.Function, request.URL)]; got.Err == nil || !strings.Contains(got.Err.Message, "사설망") {
		t.Fatalf("루프백은 허용 목록에 있어도 거절해야 한다: %+v", got)
	}
}

func TestRedirectsAndOversizedBodiesAreRefused(t *testing.T) {
	server, host := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "https://elsewhere.example.com/", http.StatusFound)
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 3*1024)))
		default:
			_, _ = w.Write([]byte("ok"))
		}
	})
	fetcher := withInsecureTLS(testFetcher(fixedSettings{"external.enabled": true, "external.allowed_hosts": []any{host}, "external.max_kb": float64(2)}), server)
	resolve := func(path string) formula.ExternalResult {
		request := formula.ExternalRequest{Function: "WEBSERVICE", URL: server.URL + path}
		return fetcher.Resolve(context.Background(), []formula.ExternalRequest{request})[formula.ExternalKey(request.Function, request.URL)]
	}
	if got := resolve("/redirect"); got.Err == nil || !strings.Contains(got.Err.Message, "넘기는") {
		t.Errorf("리다이렉트는 따라가지 않아야 한다: %+v", got)
	}
	if got := resolve("/big"); got.Err == nil || !strings.Contains(got.Err.Message, "크기") {
		t.Errorf("큰 응답은 거절해야 한다: %+v", got)
	}
	if got := resolve("/ok"); got.Err != nil || got.Text != "ok" {
		t.Errorf("보통 응답은 와야 한다: %+v", got)
	}
}

func TestImportDataParsesATableAndCachesIt(t *testing.T) {
	hits := 0
	server, host := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("\uFEFF품목,단가,비고\n연필,1200,\n공책,3.5e3,\"a,b\"\n"))
	})
	fetcher := withInsecureTLS(testFetcher(fixedSettings{"external.enabled": true, "external.allowed_hosts": []any{host}, "external.cache_seconds": float64(60)}), server)
	now := time.Now()
	fetcher.now = func() time.Time { return now }
	request := formula.ExternalRequest{Function: "IMPORTDATA", URL: server.URL + "/t.csv"}
	key := formula.ExternalKey(request.Function, request.URL)
	got := fetcher.Resolve(context.Background(), []formula.ExternalRequest{request})[key]
	if got.Err != nil || got.Rows != 3 || got.Columns != 3 {
		t.Fatalf("표를 읽지 못했다: %+v", got)
	}
	if got.Values[0] != "품목" || got.Values[4] != 1200.0 || got.Values[7] != 3500.0 || got.Values[8] != "a,b" {
		t.Fatalf("칸이 다르다: %#v", got.Values)
	}
	fetcher.Resolve(context.Background(), []formula.ExternalRequest{request})
	if hits != 1 {
		t.Fatalf("캐시가 있으면 다시 부르지 않아야 한다: %d번", hits)
	}
	now = now.Add(2 * time.Minute)
	fetcher.Resolve(context.Background(), []formula.ExternalRequest{request})
	if hits != 2 {
		t.Fatalf("캐시가 지나면 다시 불러야 한다: %d번", hits)
	}
}
