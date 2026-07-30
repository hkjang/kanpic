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
