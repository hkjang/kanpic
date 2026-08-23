package formula

import "testing"

func TestGraphRecalculatesOnlyTransitiveDependents(t *testing.T) {
	t.Parallel()
	engine := New()
	cells := map[string]CellState{
		"A1": {Value: 10.0},
		"B1": {Value: 20.0, Formula: "=A1*2"},
		"C1": {Value: 21.0, Formula: "=B1+1"},
		"D1": {Value: 99.0, Formula: "=10*10-1"},
	}
	cells["A1"] = CellState{Value: 7.0}
	result, err := engine.Recalculate(cells, []string{"A1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cells) != 2 || result.Cells[0].Address != "B1" || result.Cells[0].Value != float64(14) || result.Cells[1].Address != "C1" || result.Cells[1].Value != float64(15) {
		t.Fatalf("unexpected recalculation: %#v", result)
	}
}

func TestGraphDetectsCycleAndPropagatesError(t *testing.T) {
	t.Parallel()
	result, err := New().Recalculate(map[string]CellState{
		"A1": {Formula: "=B1+1"},
		"B1": {Formula: "=A1+1"},
		"C1": {Formula: "=A1+10"},
	}, []string{"A1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cycles) != 2 || result.Cycles[0] != "A1" || result.Cycles[1] != "B1" {
		t.Fatalf("cycles = %#v", result.Cycles)
	}
	for _, cell := range result.Cells {
		if cell.Error == nil || cell.Error.Code != "#CIRC!" {
			t.Fatalf("cell did not receive circular error: %#v", cell)
		}
	}
}

func TestFormulaRangeLimitIsEnforcedBeforeExpansion(t *testing.T) {
	t.Parallel()
	result := New().Evaluate("=SUM(A1:A100001)", nil)
	if result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Fatalf("range limit error = %#v", result.Error)
	}
}

func TestFormulaTextStartingWithHashIsNotTreatedAsAnError(t *testing.T) {
	t.Parallel()
	result, err := New().Recalculate(map[string]CellState{
		"A1": {Value: "#heading", Formula: `="#heading"`},
		"B1": {Formula: `=A1&"-ok"`},
	}, []string{"A1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cells) != 2 || result.Cells[1].Error != nil || result.Cells[1].Value != "#heading-ok" {
		t.Fatalf("hash text recalculation: %#v", result)
	}
}

// 수식은 두 갈래로 셈한다. 수식 미리보기는 Evaluate 로 가고, 칸에 실제로
// 담기는 값은 Recalculate 로 간다. 두 갈래는 같은 답을 내야 한다.
//
// 담을 수 없는 값을 걸러내는 검사가 Evaluate 에만 있었다. 그래서 =EXP(1000)
// 은 미리보기에서 #NUM! 이었지만 칸에 적으면 무한대가 그대로 흘러갔고,
// JSON 으로 옮길 수 없어 저장 요청이 통째로 500 이 되었다. 저장 대기줄은
// 500 을 받은 항목을 지우지 않고 그대로 다시 보내므로, 그 워크북은 그
// 뒤로 **아무것도 저장하지 못했다** — 한 칸의 수식 하나 때문에.
func TestBothCalculationPathsRefuseValuesNothingCanStore(t *testing.T) {
	t.Parallel()
	for _, formula := range []string{
		"=EXP(1000)",      // 무한대
		"=POWER(10,1000)", // 무한대
		"=POWER(-8,1/3)",  // 음수의 세제곱근 — NaN
		"=(-8)^(1/3)",     // 연산자 쪽도 같다
		"=LAMBDA(x,x+1)",  // 값이 아니라 함수
	} {
		preview := New().Evaluate(formula, map[string]any{})
		if preview.Error == nil {
			t.Errorf("%s: Evaluate 가 %v 를 돌려주었다. 오류여야 한다", formula, preview.Value)
			continue
		}
		graph, err := New().Recalculate(map[string]CellState{"A1": {Formula: formula}}, []string{"A1"})
		if err != nil {
			t.Fatalf("%s: %v", formula, err)
		}
		if len(graph.Cells) != 1 {
			t.Fatalf("%s: %d개의 칸이 나왔다. 하나여야 한다", formula, len(graph.Cells))
		}
		stored := graph.Cells[0]
		if stored.Error == nil {
			t.Errorf("%s: Recalculate 가 %v 를 돌려주었다. 오류여야 한다", formula, stored.Value)
			continue
		}
		// 두 갈래가 같은 오류를 내야 한다. 한쪽만 고치면 여기서 걸린다.
		if stored.Error.Code != preview.Error.Code {
			t.Errorf("%s: 미리보기는 %s, 칸은 %s 를 냈다", formula, preview.Error.Code, stored.Error.Code)
		}
	}
}
