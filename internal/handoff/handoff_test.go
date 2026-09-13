package handoff

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeersAreReadFromTheAllowList(t *testing.T) {
	t.Parallel()
	peers := ParsePeers([]any{
		"ptium=https://ptium.intra",
		" Weekly = https://weekly.intra/ ",
		"https://kanpic-staging.intra:8443", // 이름 없이 — 받기만 허용
		"ptium=https://ptium.intra/docs",    // 오리진이 아니다
		"ftp://files.intra",
		"",
	})
	want := []Peer{{Name: "ptium", Origin: "https://ptium.intra"}, {Name: "weekly", Origin: "https://weekly.intra"}, {Name: "", Origin: "https://kanpic-staging.intra:8443"}}
	if len(peers) != len(want) {
		t.Fatalf("peers=%+v", peers)
	}
	for i := range want {
		if peers[i] != want[i] {
			t.Fatalf("peers[%d]=%+v want %+v", i, peers[i], want[i])
		}
	}
}

// 받을 수 없는 형식을 보내는 단추는 만들지 않는다. kanpic 은 csv·xlsx 를
// 보내므로 그것을 받는 ptium·kanpic 만 보낼 곳이고, 마크다운만 받는 muni 와
// 이름 없는 줄은 보이지 않는다.
func TestTargetsAreOnlyThoseThatReceiveWhatKanpicSends(t *testing.T) {
	t.Parallel()
	targets := Targets(ParsePeers([]any{"muni=https://muni.intra", "ptium=https://ptium.intra", "https://other.intra", "kanpic=https://kanpic2.intra", "umm=https://umm.intra"}))
	if len(targets) != 2 || targets[0].Name != "kanpic" || targets[1].Name != "ptium" {
		t.Fatalf("targets=%+v", targets)
	}
	if strings.Join(targets[1].Formats, ",") != "csv,xlsx" {
		t.Fatalf("formats=%v", targets[1].Formats)
	}
	if len(Targets(nil)) != 0 {
		t.Fatal("an empty allow list must show no button")
	}
}

// 허용 목록은 정확히 같은 오리진만 통한다.
func TestAllowedMatchesTheOriginExactly(t *testing.T) {
	t.Parallel()
	peers := ParsePeers([]any{"ptium=https://ptium.intra"})
	for _, source := range []string{"https://ptium.intra", "HTTPS://PTIUM.INTRA/", "https://ptium.intra/"} {
		if _, ok := Allowed(peers, source); !ok {
			t.Fatalf("%q should be allowed", source)
		}
	}
	for _, source := range []string{"http://ptium.intra", "https://ptium.intra:8443", "https://evil.ptium.intra", "https://ptium.intra.evil", "https://ptium.intra/../", "https://user@ptium.intra", "ptium.intra", ""} {
		if _, ok := Allowed(peers, source); ok {
			t.Fatalf("%q must not be allowed", source)
		}
	}
}

// 표는 한 번만 쓰이고, 시간이 지나면 같은 404 다.
func TestAClaimIsTakenOnceAndNotAfterItExpires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	service := NewService(nil, nil).WithClock(func() time.Time { return now })
	claim, ticket, err := service.Issue(ctx, Ticket{Resource: "wb", Format: "csv", Filename: "표.csv", Data: []byte("a,b\n")})
	if err != nil {
		t.Fatal(err)
	}
	if len(claim) < 22 || !ticket.ExpiresAt.Equal(now.Add(ClaimTTL)) {
		t.Fatalf("claim=%q expires=%s", claim, ticket.ExpiresAt)
	}
	taken, err := service.Redeem(ctx, claim)
	if err != nil || string(taken.Data) != "a,b\n" {
		t.Fatalf("taken=%+v err=%v", taken, err)
	}
	if _, err := service.Redeem(ctx, claim); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second use err=%v", err)
	}
	claim, _, err = service.Issue(ctx, Ticket{Resource: "wb", Format: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(ClaimTTL)
	if _, err := service.Redeem(ctx, claim); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired err=%v", err)
	}
	if _, err := service.Redeem(ctx, "never-issued"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown err=%v", err)
	}
}

// 받는 쪽의 규칙. 허용 목록에 없는 source 는 요청을 보내지 않고 거절하고,
// 리다이렉트를 따라가지 않으며, 상한을 넘는 본문은 끊고, 받을 수 없는
// 형식은 버린다.
func TestFetchRefusesWhatTheStandardSaysToRefuse(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/ok"):
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''%EB%A7%A4%EC%B6%9C.csv`)
			_, _ = w.Write([]byte("a,b\n1,2\n"))
		case strings.HasSuffix(r.URL.Path, "/moved"):
			http.Redirect(w, r, "http://127.0.0.1:9/elsewhere", http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/big"):
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		case strings.HasSuffix(r.URL.Path, "/html"):
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)
	peers := ParsePeers([]any{"umm=" + upstream.URL})
	options := FetchOptions{MaxBytes: 50}

	if _, err := Fetch(context.Background(), peers, "http://127.0.0.1:9", "ok", options); rejectionCode(err) != "source_not_allowed" {
		t.Fatalf("err=%v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("a source outside the allow list must not be asked anything")
	}

	fetched, err := Fetch(context.Background(), peers, upstream.URL, "ok", options)
	if err != nil || fetched.Format != "csv" || fetched.Filename != "매출.csv" || string(fetched.Data) != "a,b\n1,2\n" {
		t.Fatalf("fetched=%+v err=%v", fetched, err)
	}
	for claim, code := range map[string]string{"moved": "redirect", "big": "too_large", "html": "content_type", "gone": "claim_rejected"} {
		if _, err := Fetch(context.Background(), peers, upstream.URL, claim, options); rejectionCode(err) != code {
			t.Fatalf("%s: err=%v want %s", claim, err, code)
		}
	}
	if hits.Load() != 5 {
		t.Fatalf("hits=%d", hits.Load())
	}
}

func TestFetchGivesUpAfterTheTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); upstream.Close() })
	_, err := Fetch(context.Background(), ParsePeers([]any{"umm=" + upstream.URL}), upstream.URL, "slow", FetchOptions{Timeout: 50 * time.Millisecond})
	if code := rejectionCode(err); code != "upstream" && code != "timeout" {
		t.Fatalf("err=%v", err)
	}
}

func TestFilenameFollowsTheContentType(t *testing.T) {
	t.Parallel()
	cases := map[[2]string]string{
		{`attachment; filename="report.csv"`, "csv"}:       "report.csv",
		{`attachment; filename="report.xlsx"`, "csv"}:      "report.csv",
		{`attachment; filename="report"`, "xlsx"}:          "report.xlsx",
		{`attachment; filename="../../etc/passwd"`, "csv"}: "passwd.csv",
		{``, "xlsx"}: "넘겨받은 문서.xlsx",
		{`attachment; filename*=UTF-8''2026%20%EA%B0%9C%ED%8E%B8.csv`, "csv"}: "2026 개편.csv",
	}
	for input, want := range cases {
		if got := filenameFor(input[0], input[1]); got != want {
			t.Errorf("filenameFor(%q,%q)=%q want %q", input[0], input[1], got, want)
		}
	}
}

func rejectionCode(err error) string {
	var rejection *Rejection
	if errors.As(err, &rejection) {
		return rejection.Code
	}
	return ""
}
