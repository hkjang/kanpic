package formula

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// 격자와 서버의 TEXT 는 같은 값을 같은 글자로 적어야 한다. 두 곳에서 따로
// 셈하므로 어긋나기 쉽고, 실제로 여러 번 어긋나 있었다 — 시각의 mm 을 달로
// 읽거나, 지수 자리를 채우지 않거나, "General" 을 숫자 서식으로 읽거나.
//
// testdata/cell-formats.json 을 web/src/lib/cellFormat.test.ts 와 함께
// 읽는다. 한쪽만 고치면 양쪽 다 걸린다.
func TestCellFormatsMatchTheGrid(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../testdata/cell-formats.json")
	if err != nil {
		t.Fatalf("서식 목록을 읽지 못했다: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Value  float64 `json:"value"`
			Format string  `json:"format"`
			Text   string  `json:"text"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("서식 목록을 읽지 못했다: %v", err)
	}
	if len(fixture.Cases) < 100 {
		t.Fatalf("서식 목록이 %d 줄뿐이다. 잘못 읽었다", len(fixture.Cases))
	}
	engine := New()
	mismatched := 0
	for _, testCase := range fixture.Cases {
		got := formatValue(testCase.Value, testCase.Format)
		if got == testCase.Text {
			continue
		}
		mismatched++
		if mismatched <= 20 {
			t.Errorf("TEXT(%v, %q) = %q, 격자는 %q 를 그린다", testCase.Value, testCase.Format, got, testCase.Text)
		}
	}
	if mismatched > 20 {
		t.Errorf("%d 곳이 더 어긋난다", mismatched-20)
	}
	// 엔진을 거쳐도 같은 답이 나오는지 몇 줄만 확인한다.
	for _, testCase := range fixture.Cases[:5] {
		formula := fmt.Sprintf(`=TEXT(%v,"%s")`, testCase.Value, testCase.Format)
		if result := engine.Evaluate(formula, map[string]any{}); result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
		}
	}
}

// 엑셀 서식은 값의 부호마다 다른 구역을 담고, 자리 기호 뒤의 쉼표는 자릿점이
// 아니라 천 단위 축약이다. 둘 다 재무 자료에서 온 파일에 흔하다.
//
// 예전에는 구역이 둘뿐인 줄 알고 0 을 늘 양수 구역으로 보냈고(회계 서식의
// 0 은 "-" 인데 "0" 이었다), 비워 둔 구역을 감추지 않았으며("0;;" 이 음수를
// 그대로 그렸다), 쉼표가 있기만 하면 자릿점으로 보아 "#,##0,," 이 백만 배로
// 보였다. 값은 testdata/cell-formats.json 이 격자와 함께 붙잡는다. 여기서는
// 규칙 자체를 적어 둔다.
func TestFormatSectionsAndThousandsScaleFollowExcel(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		number float64
		format string
		want   string
	}{
		// 두 구역이면 0 은 양수 구역이 그리고, 세 구역이면 0 구역이 그린다.
		{0, `#,##0;(#,##0)`, "0"},
		{0, `#,##0;(#,##0);"-"`, "-"},
		{-1234, `#,##0;(#,##0);"-"`, "(1,234)"},
		{0, `0;-0;"영"`, "영"},
		// 비워 둔 구역은 아무것도 적지 않는다.
		{-5, `0;;`, ""},
		{0, `0;;`, ""},
		{5, `0;;`, "5"},
		{-5, `0;`, ""},
		// 자리 기호가 없는 구역은 적어 둔 글자만 적는다.
		{5, `"판매"`, "판매"},
		// 따옴표 안의 쌍반점은 구역을 가르지 않는다.
		{-5, `#,##0;"내려감;"`, "내려감;"},
		// 자리 기호 뒤의 쉼표는 천 단위로 줄여 적으라는 뜻이다.
		{1234567, `#,##0,`, "1,235"},
		{1234567, `#,##0,,`, "1"},
		{1234567, `0.0,,`, "1.2"},
		{1234567890, `#,##0,,`, "1,235"},
		{-1234567, `#,##0,,`, "-1"},
		// 자리 기호 사이의 쉼표는 그대로 자릿점이다.
		{1234567, `#,##0`, "1,234,567"},
		{1234567, `0`, "1234567"},
	} {
		if got := formatValue(testCase.number, testCase.format); got != testCase.want {
			t.Errorf("TEXT(%v, %q) = %q, 엑셀은 %q 를 적는다", testCase.number, testCase.format, got, testCase.want)
		}
	}
}
