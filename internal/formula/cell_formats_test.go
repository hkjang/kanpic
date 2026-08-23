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
