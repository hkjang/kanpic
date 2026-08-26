package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

// 감사에서는 "그 기간 기록" 을 달라고 한다. 기간을 읽는 규칙이 화면과
// 내보내기에서 같아야, 넘긴 파일과 화면에서 본 것이 같다.
func TestLogRangeReadsWhatAPersonWouldWrite(t *testing.T) {
	t.Parallel()
	at := func(query string) (time.Time, time.Time, error) {
		return logRange(httptest.NewRequest("GET", "/api/v1/admin/logs?"+query, nil))
	}

	from, to, err := at("from=2026-01-05&to=2026-01-06")
	if err != nil {
		t.Fatal(err)
	}
	if from.Format(time.RFC3339) != "2026-01-05T00:00:00Z" {
		t.Errorf("from = %s", from.Format(time.RFC3339))
	}
	// 끝 날짜만 적으면 그 날이 통째로 들어가야 한다. 그렇지 않으면 마지막
	// 날의 기록이 통째로 빠지고, 그것을 알아채는 사람은 없다.
	if !to.After(time.Date(2026, 1, 6, 23, 59, 59, 0, time.UTC)) {
		t.Errorf("to = %s — 마지막 날이 통째로 들어가야 한다", to.Format(time.RFC3339))
	}

	// 시각까지 적으면 그대로 쓴다.
	from, _, err = at("from=2026-01-05T09:30:00Z")
	if err != nil || from.Format(time.RFC3339) != "2026-01-05T09:30:00Z" {
		t.Errorf("from = %v err=%v", from, err)
	}

	// 비워 두면 기간을 좁히지 않는다.
	from, to, err = at("")
	if err != nil || !from.IsZero() || !to.IsZero() {
		t.Errorf("빈 기간 = %v %v err=%v", from, to, err)
	}

	// 거꾸로 적은 것은 잡는다. 그대로 두면 결과가 늘 비어 있고, 사람은
	// 기록이 없다고 믿는다.
	if _, _, err := at("from=2026-01-06&to=2026-01-05"); err == nil {
		t.Error("끝이 시작보다 앞인데 받아들였다")
	}
	if _, _, err := at("from=어제"); err == nil {
		t.Error("읽을 수 없는 날짜를 받아들였다")
	}
}
