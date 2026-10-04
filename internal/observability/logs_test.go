package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

// loginName 은 slog.LogValuer 를 구현한다 — 콘솔은 Resolve() 한 값을 적으므로
// DB 기록도 같은 값을 담아야 한다.
type loginName string

func (n loginName) LogValue() slog.Value { return slog.StringValue("user:" + string(n)) }

// marshalingError 는 스스로 json.Marshaler 인 error 다. slog 은 이때 Error()
// 가 아니라 Marshaler 결과를 적으므로 DB 기록도 그 규칙을 따라야 한다.
type marshalingError struct{}

func (marshalingError) Error() string                { return "원문 오류" }
func (marshalingError) MarshalJSON() ([]byte, error) { return []byte(`{"code":42}`), nil }

// TestPersistedAttributesMatchConsoleJSON 은 레코드 **하나**로 콘솔(stdout)에
// 적힌 값과 DB 에 담길 값이 같은지 본다. 둘이 갈리면 장애 원인이 콘솔에만
// 남고 관리자 콘솔 로그 화면·CSV 내보내기가 읽는 영구 기록은 빈 객체가 된다.
func TestPersistedAttributesMatchConsoleJSON(t *testing.T) {
	var console bytes.Buffer
	// 프로덕션과 같은 생성 경로를 쓴다. run() 은 돌리지 않는다 — pool 이 nil
	// 이면 h.pool.Exec 가 nil 역참조로 패닉한다. 큐가 2048 이라 적재 고리
	// 없이도 레코드는 그대로 남는다.
	handler := newPersistentHandler(&console, nil)
	logger := slog.New(handler)

	when := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	logger.Error("request failed",
		"error", fmt.Errorf("상위: %w", errors.New("연결 거부")),
		"marshaler", marshalingError{},
		"valuer", loginName("hkjang"),
		slog.Group("g", "a", 1, slog.Group("inner", "b", "둘째")),
		"path", "/api/x",
		"status", 503,
		"ok", false,
		"dur", 3*time.Second,
		"trace_id", "T-1",
		"when", when,
	)

	close(handler.queue)
	record, ok := <-handler.queue
	if !ok {
		t.Fatal("큐에 레코드가 없다 — Handle 이 적재하지 않았다")
	}

	persisted := decodeKeys(t, mustMarshal(t, record.attributes), "DB 에 담길 attributes")
	consoleLine := decodeKeys(t, console.Bytes(), "콘솔이 적은 줄")

	// 콘솔 JSONHandler 가 적은 값과 글자 그대로 같아야 하는 키들.
	// when(time.Time)은 slog 이 밀리초까지만 적고 encoding/json 은 나노초
	// 정밀도로 적어 표현이 다르므로 아래에서 따로 본다.
	for _, key := range []string{"error", "marshaler", "valuer", "g", "path", "status", "ok", "dur", "trace_id"} {
		want, found := consoleLine[key]
		if !found {
			t.Fatalf("콘솔 줄에 %q 가 없다: %s", key, console.String())
		}
		got, found := persisted[key]
		if !found {
			t.Errorf("attributes 에 %q 가 없다 (콘솔=%s)", key, canonical(t, want))
			continue
		}
		if canonical(t, got) != canonical(t, want) {
			t.Errorf("%q: DB=%s, 콘솔=%s — 같은 레코드이므로 같아야 한다", key, canonical(t, got), canonical(t, want))
		}
	}

	// 지금 올바르게 담기는 종류는 그대로여야 한다: time 은 time.Time 으로
	// 남고(encoding/json 이 RFC3339 로 적는다), trace_id 는 문자열로 뽑힌다.
	if stored, isTime := record.attributes["when"].(time.Time); !isTime || !stored.Equal(when) {
		t.Errorf("when=%#v, want time.Time %v", record.attributes["when"], when)
	}
	if record.traceID != "T-1" {
		t.Errorf("traceID=%q, want \"T-1\"", record.traceID)
	}
	if record.level != "ERROR" || record.message != "request failed" {
		t.Errorf("level=%q message=%q, want ERROR/\"request failed\"", record.level, record.message)
	}
}

// TestPersistedAttributeKeysMatchConsoleJSON 은 콘솔이 적지 않는 것(빈 그룹,
// 키도 값도 없는 속성)과 펼쳐 적는 것(키 없는 그룹)에서 두 기록의 키가 같은지
// 본다 — 키가 갈리면 화면과 내보내기를 나란히 읽을 수 없다.
func TestPersistedAttributeKeysMatchConsoleJSON(t *testing.T) {
	var console bytes.Buffer
	handler := newPersistentHandler(&console, nil)
	logger := slog.New(handler)

	logger.Info("edges", slog.Group("none"), "", nil, slog.Group("", "flat", 1), "kept", "값")

	close(handler.queue)
	record := <-handler.queue

	wanted := decodeKeys(t, console.Bytes(), "콘솔이 적은 줄")
	// 콘솔 줄에만 있는 레코드 자체의 칸은 attributes 가 아니라 별도 열이다.
	for _, own := range []string{"time", "level", "msg"} {
		delete(wanted, own)
	}
	for key := range wanted {
		if _, found := record.attributes[key]; !found {
			t.Errorf("콘솔은 %q 를 적었는데 attributes 에는 없다: %s", key, mustMarshal(t, record.attributes))
		}
	}
	for key := range record.attributes {
		if _, found := wanted[key]; !found {
			t.Errorf("attributes 에 %q 가 있는데 콘솔은 적지 않았다: %s", key, console.String())
		}
	}
	if got := record.attributes["flat"]; got != int64(1) {
		t.Errorf("키 없는 그룹이 펼쳐지지 않았다: flat=%#v, want 1", got)
	}
	if got := record.attributes["kept"]; got != "값" {
		t.Errorf("kept=%#v, want \"값\"", got)
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

// decodeKeys 는 한 줄 JSON 을 키별 원문으로 가른다. 콘솔 줄과 attributes 를
// 같은 자로 견주려면 키 단위로 떼어 봐야 한다.
func decodeKeys(t *testing.T, data []byte, what string) map[string]json.RawMessage {
	t.Helper()
	keys := make(map[string]json.RawMessage)
	if err := json.Unmarshal(bytes.TrimSpace(data), &keys); err != nil {
		t.Fatalf("%s 를 읽을 수 없다: %v (%s)", what, err, data)
	}
	return keys
}

// canonical 은 맵 키 순서 같은 표기 차이를 없앤 꼴로 되돌린다. slog 은 적은
// 순서대로, encoding/json 은 키를 정렬해 적기 때문이다.
func canonical(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s 를 읽을 수 없다: %v", raw, err)
	}
	return string(mustMarshal(t, value))
}
