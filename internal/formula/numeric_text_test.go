package formula

import (
	"encoding/json"
	"os"
	"testing"
)

type numericTextCase struct {
	Text   string  `json:"text"`
	Counts bool    `json:"counts"`
	Value  float64 `json:"value"`
}

// 글자로 담긴 값이 셈에 들어가는 규칙은 엔진이 정하고 격자가 따른다. 두 곳이
// 다르면 =SUM 이 내는 값과 상태 줄의 합계가 어긋난다. 같은 자료를 웹의
// spreadsheetNumber.fixture.test.ts 가 함께 읽는다 — 한쪽만 고치면 양쪽 다 걸린다.
func TestNumericTextFixture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/numeric-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []numericTextCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 40 {
		t.Fatalf("자료가 %d 개뿐이다", len(fixture.Cases))
	}
	for _, item := range fixture.Cases {
		number, counted := toNumber(item.Text)
		if counted != item.Counts {
			t.Errorf("%q: 세는가 %v, 자료는 %v", item.Text, counted, item.Counts)
			continue
		}
		if item.Counts && number != item.Value {
			t.Errorf("%q: %v 로 셌다, 자료는 %v", item.Text, number, item.Value)
		}
	}
}

// 사람이 실제로 보던 증상. pandas 나 R 이 내보낸 CSV 에는 빠진 값이 "NaN" 이나
// "Inf" 라는 글자로 들어 있다. 그 한 칸이 열 전체의 합계를 #NUM! 으로 만들었다.
func TestOneExportedNaNDoesNotPoisonTheColumn(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1200.0, "A2": "NaN", "A3": 800.0, "A4": "Inf", "A5": 500.0}
	for _, formula := range []string{"=SUM(A1:A5)", "=AVERAGE(A1:A3)"} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Fatalf("%s: %v", formula, result.Error)
		}
		if number, ok := result.Value.(float64); !ok || number != map[string]float64{"=SUM(A1:A5)": 2500, "=AVERAGE(A1:A3)": 1000}[formula] {
			t.Fatalf("%s = %#v", formula, result.Value)
		}
	}
}
