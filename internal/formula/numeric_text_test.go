package formula

import (
	"encoding/json"
	"fmt"
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

// 조건도 셀 값과 같은 자로 읽어야 한다. 예전에는 조건만 Go 의 ParseFloat 로
// 읽어서, "Inf" 나 "1_000" 이 적힌 칸을 세려 하면 0 이 나왔다. 칸은 거기 있는데
// 없다고 답하는 것이 빈 답보다 나쁘다.
func TestCountifCountsTheTextItIsAskedFor(t *testing.T) {
	t.Parallel()
	labels := []string{"0x1F", "NaN", "1_000", "0b101", "Inf", "abc"}
	cells := map[string]any{}
	for index, label := range labels {
		cells[fmt.Sprintf("A%d", index+1)] = label
	}
	for _, label := range labels {
		formula := fmt.Sprintf(`=COUNTIF(A1:A%d,"%s")`, len(labels), label)
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Fatalf("%s: %v", formula, result.Error)
		}
		if number, ok := result.Value.(float64); !ok || number != 1 {
			t.Errorf("%s = %#v, 1 이어야 한다", formula, result.Value)
		}
	}
}

// 숫자 조건은 그대로 숫자로 읽어야 한다.
func TestNumericCriteriaStillCompareAsNumbers(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 500.0, "A2": 1500.0, "A3": 2500.0}
	for formula, expected := range map[string]float64{
		`=COUNTIF(A1:A3,">=1e3")`: 2,
		`=COUNTIF(A1:A3,">1000")`: 2,
		`=COUNTIF(A1:A3,"500")`:   1,
		`=SUMIF(A1:A3,">=1500")`:  4000,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Fatalf("%s: %v", formula, result.Error)
		}
		if number, ok := result.Value.(float64); !ok || number != expected {
			t.Errorf("%s = %#v, %v 여야 한다", formula, result.Value, expected)
		}
	}
}
