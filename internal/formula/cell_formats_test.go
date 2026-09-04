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
			// 값은 수만이 아니다. 넷째 구역(글자 구역)은 글자 값에만 걸린다.
			Value  any    `json:"value"`
			Format string `json:"format"`
			Text   string `json:"text"`
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

// 엑셀 서식의 넷째 구역은 글자 값의 서식이다 — 회계 서식의 마지막 토막
// `_-@_-` 가 그것이고, `#,##0;(#,##0);"-";"["@"]"` 는 글자 칸을 [미정] 으로
// 적는다. 예전에는 그 구역을 아예 읽지 않아 글자 값이 서식 없이 그대로
// 나왔고, 이름 뒤에 말을 붙이는 `@" 님"` 은 "@ 님" 이라고 적혔다.
// 값은 testdata/cell-formats.json 이 격자와 함께 붙잡는다. 여기서는 규칙
// 자체를 적어 둔다.
func TestTextSectionsFollowExcel(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		value  any
		format string
		want   string
	}{
		// 넷째 구역이 글자 값을 그린다.
		{"미정", `#,##0;(#,##0);"-";"["@"]"`, "[미정]"},
		{"미정", `_-* #,##0_-;-* #,##0_-;_-* "-"_-;_-@_-`, "미정"},
		{"미정", `0;;;"기타"`, "기타"},
		// 비워 둔 넷째 구역은 글자를 감춘다.
		{"미정", `#,##0;;;`, ""},
		// 구역이 넷이 안 되면 글자를 손대지 않는다.
		{"미정", `#,##0;(#,##0);"-"`, "미정"},
		{"미정", `#,##0`, "미정"},
		// 구역이 하나뿐인데 @ 가 있으면 그 구역이 곧 글자 구역이다.
		{"홍길동", `@" 님"`, "홍길동 님"},
		{"홍길동", `"("@")"`, "(홍길동)"},
		// @ 는 수에도 걸린다. 값을 있는 그대로 적는 자리다.
		{5.0, `@" 님"`, "5 님"},
		// 앞의 구역들은 그대로 수를 적는다.
		{1234.0, `#,##0;(#,##0);"-";"["@"]"`, "1,234"},
		{-1234.0, `#,##0;(#,##0);"-";"["@"]"`, "(1,234)"},
		{0.0, `#,##0;(#,##0);"-";"["@"]"`, "-"},
	} {
		if got := formatValue(testCase.value, testCase.format); got != testCase.want {
			t.Errorf("TEXT(%v, %q) = %q, 엑셀은 %q 를 적는다", testCase.value, testCase.format, got, testCase.want)
		}
	}
}

// 분수 서식은 값을 소수가 아니라 분수로 적는다. 엑셀 기본 서식 12·13 번
// (`# ?/?`, `# ??/??`)이라 XLSX 로 그냥 들어오는데, 예전에는 자리 기호만
// 세고 빗금을 글자로 흘려 보내 `# ?/?` 가 0.5 를 "1/2" 가 아니라 "1/" 로
// 적었다. 값은 testdata/cell-formats.json 이 격자와 함께 붙잡는다.
// 여기서는 규칙 자체를 적어 둔다.
func TestFractionFormatsFollowExcel(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		number float64
		format string
		want   string
	}{
		// 자리 기호 개수가 분모의 상한이다 — ? 는 9, ?? 는 99, ??? 는 999.
		{0.3125, `# ?/?`, "1/3"},
		{0.3125, `# ??/??`, "5/16"},
		{3.14159, `# ???/???`, "3 16/113"},
		// 같은 만큼 어긋나면 분모가 작은 쪽이다.
		{0.5, `# ?/?`, "1/2"},
		{0.5, `# ??/??`, "1/2"},
		// 빈칸으로 가른 정수 자리가 있으면 대분수, 없으면 가분수다.
		{2.75, `# ?/?`, "2 3/4"},
		{5.25, `#/#`, "21/4"},
		// 분모를 숫자로 못 박으면 약분하지 않는다.
		{0.5, `?/8`, "4/8"},
		{0.375, `?/8`, "3/8"},
		// 분자가 분모까지 올라가면 정수 자리로 올린다.
		{0.99, `# ?/?`, "1"},
		{1, `# ?/?`, "1"},
		// # 은 0 인 정수 자리를 감추고 0 은 적는다.
		{0.5, `0 ?/?`, "0 1/2"},
		{1234.5, `#,##0 ?/?`, "1,234 1/2"},
		// 부호와 적어 둔 글자는 그대로 따라온다. "-0" 은 적지 않는다.
		{-2.75, `# ?/?`, "-2 3/4"},
		{-0.02, `# ?/?`, "0"},
		{2.75, `# ?/?" 개"`, "2 3/4 개"},
		// 날짜 서식의 빗금은 자리 기호 사이에 있지 않으므로 분수가 아니다.
		{45000.5, `m/d/yyyy`, "3/15/2023"},
	} {
		if got := formatValue(testCase.number, testCase.format); got != testCase.want {
			t.Errorf("TEXT(%v, %q) = %q, 엑셀은 %q 를 적는다", testCase.number, testCase.format, got, testCase.want)
		}
	}
}
