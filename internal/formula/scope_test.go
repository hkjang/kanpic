package formula

import (
	"reflect"
	"testing"
)

func TestScopedEvaluatorResolvesQuotedAndUnquotedSheetReferences(t *testing.T) {
	t.Parallel()
	evaluator := NewScoped("sheet-current", map[string]string{"Sheet1": "sheet-current", "Sales Data": "sheet-sales", "매출": "sheet-sales"})
	cells := map[string]any{
		CellKey("sheet-current", "A1"): 2,
		CellKey("sheet-sales", "B1"):   10,
		CellKey("sheet-sales", "B2"):   20,
	}
	result := evaluator.Evaluate(`=A1+SUM('Sales Data'!B1:B2)`, cells)
	if result.Error != nil || result.Value != float64(32) {
		t.Fatalf("scoped result = %#v, error=%v", result.Value, result.Error)
	}
	want := []string{CellKey("sheet-current", "A1"), CellKey("sheet-sales", "B1"), CellKey("sheet-sales", "B2")}
	if !reflect.DeepEqual(result.Dependencies, want) {
		t.Fatalf("dependencies = %#v, want %#v", result.Dependencies, want)
	}
	korean := evaluator.Evaluate(`=매출!B1`, cells)
	if korean.Error != nil || korean.Value != 10 {
		t.Fatalf("unquoted Unicode sheet result = %#v, error=%v", korean.Value, korean.Error)
	}
	missing := evaluator.Evaluate(`=Missing!A1`, cells)
	if missing.Error == nil || missing.Error.Code != "#REF!" {
		t.Fatalf("missing sheet error = %#v", missing.Error)
	}
}

func TestGraphRecalculatesAcrossSheetsAndDetectsCrossSheetCycle(t *testing.T) {
	t.Parallel()
	evaluator := NewScoped("", map[string]string{"Input": "sheet-input", "Report": "sheet-report"})
	input := CellKey("sheet-input", "A1")
	report := CellKey("sheet-report", "B1")
	result, err := evaluator.Recalculate(map[string]CellState{
		input:  {Value: 4},
		report: {Formula: `=Input!A1*5`},
	}, []string{input})
	if err != nil || len(result.Cells) != 1 || result.Cells[0].Address != report || result.Cells[0].Value != float64(20) {
		t.Fatalf("cross-sheet graph = %#v, error=%v", result, err)
	}

	cycle, err := evaluator.Recalculate(map[string]CellState{
		input:  {Formula: `=Report!B1`},
		report: {Formula: `=Input!A1`},
	}, []string{input})
	if err != nil || len(cycle.Cycles) != 2 {
		t.Fatalf("cross-sheet cycle = %#v, error=%v", cycle, err)
	}
}

func TestUnscopedEvaluatorAcceptsQualifiedCellMaps(t *testing.T) {
	t.Parallel()
	result := New().Evaluate(`=Sheet2!A1+1`, map[string]any{"sheet2!a1": 9})
	if result.Error != nil || result.Value != float64(10) {
		t.Fatalf("unscoped qualified result = %#v, error=%v", result.Value, result.Error)
	}
}

func TestScopedEvaluatorResolvesWorkbookNamedRanges(t *testing.T) {
	t.Parallel()
	evaluator := NewScopedWithNames("sheet-report", map[string]string{"Input": "sheet-input", "Report": "sheet-report"}, map[string]NamedRange{
		"Quarter_Sales": {SheetID: "sheet-input", Range: "B1:B3"},
		"TaxRate":       {SheetID: "sheet-input", Range: "C1"},
		"XFE1":          {SheetID: "sheet-input", Range: "D1"},
		"A1048577":      {SheetID: "sheet-input", Range: "E1"},
	})
	cells := map[string]any{
		CellKey("sheet-input", "B1"): 10,
		CellKey("sheet-input", "B2"): 20,
		CellKey("sheet-input", "B3"): 30,
		CellKey("sheet-input", "C1"): .1,
		CellKey("sheet-input", "D1"): 2,
		CellKey("sheet-input", "E1"): 3,
	}
	result := evaluator.Evaluate("=SUM(Quarter_Sales)*(1+TaxRate)", cells)
	if result.Error != nil || result.Value != float64(66) {
		t.Fatalf("named result = %#v, error=%v", result.Value, result.Error)
	}
	want := []string{CellKey("sheet-input", "B1"), CellKey("sheet-input", "B2"), CellKey("sheet-input", "B3"), CellKey("sheet-input", "C1")}
	if !reflect.DeepEqual(result.Dependencies, want) {
		t.Fatalf("named dependencies = %#v, want %#v", result.Dependencies, want)
	}
	missing := evaluator.Evaluate("=MissingName", cells)
	if missing.Error == nil || missing.Error.Code != "#NAME?" {
		t.Fatalf("missing named range error = %#v", missing.Error)
	}
	outOfBoundsNames := evaluator.Evaluate("=XFE1+A1048577", cells)
	if outOfBoundsNames.Error != nil || outOfBoundsNames.Value != float64(5) {
		t.Fatalf("out-of-bounds reference names = %#v, error=%v", outOfBoundsNames.Value, outOfBoundsNames.Error)
	}
}
