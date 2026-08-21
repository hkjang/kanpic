package formula

import "testing"

// Unbounded references are how a spreadsheet stays correct as a table grows,
// so they have to evaluate over the rows in use and move with the grid.
func TestWholeColumnReferencesCoverTheUsedRows(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 10.0, "A2": 20.0, "A3": 30.0, "B1": "x"}
	for formula, expected := range map[string]any{
		"=SUM(A:A)":            60.0,
		"=SUM($A:$A)":          60.0,
		"=COUNT(A:A)":          3.0,
		"=SUM(A2:A)":           50.0,
		"=SUM(A:B)":            60.0,
		"=SUM(1:1)":            10.0,
		"=SUM(1:3)":            60.0,
		"=AVERAGE(A:A)":        20.0,
		"=SUM(A:A)+1":          61.0,
		"=SUM(A:A)/COUNT(A:A)": 20.0,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
}

// A number that is not part of a range keeps its ordinary meaning.
func TestPlainNumbersAreNotRowBands(t *testing.T) {
	t.Parallel()
	for formula, expected := range map[string]any{"=1+2": 3.0, "=SUM(1,2,3)": 6.0, "=MAX(2,5)": 5.0} {
		result := New().Evaluate(formula, map[string]any{"A1": 1.0})
		if result.Error != nil || result.Value != expected {
			t.Errorf("%s = %v (%v), want %v", formula, result.Value, result.Error, expected)
		}
	}
}

func TestWholeColumnReferenceDependsOnEveryUsedCell(t *testing.T) {
	t.Parallel()
	dependencies := New().Evaluate("=SUM(A:A)", map[string]any{"A1": 1.0, "A4": 2.0}).Dependencies
	if len(dependencies) != 4 {
		t.Fatalf("dependencies=%v", dependencies)
	}
}

// Inserting rows must not move a column band, and inserting columns must move
// it exactly as far as the insertion.
func TestBandReferencesFollowStructuralChanges(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula  string
		change   StructuralChange
		expected string
	}{
		{"=SUM(A:A)", StructuralChange{Axis: "row", Action: "insert", Index: 2, Count: 5}, "=SUM(A:A)"},
		{"=SUM(A:A)", StructuralChange{Axis: "column", Action: "insert", Index: 1, Count: 1}, "=SUM(B:B)"},
		{"=SUM(B:D)", StructuralChange{Axis: "column", Action: "insert", Index: 3, Count: 2}, "=SUM(B:F)"},
		{"=SUM($B:$D)", StructuralChange{Axis: "column", Action: "delete", Index: 1, Count: 1}, "=SUM($A:$C)"},
		{"=SUM(B:B)", StructuralChange{Axis: "column", Action: "delete", Index: 2, Count: 1}, "=SUM(#REF!)"},
		{"=SUM(2:5)", StructuralChange{Axis: "row", Action: "insert", Index: 1, Count: 2}, "=SUM(4:7)"},
		{"=SUM(2:5)", StructuralChange{Axis: "column", Action: "insert", Index: 1, Count: 2}, "=SUM(2:5)"},
		{"=SUM(2:5)", StructuralChange{Axis: "row", Action: "delete", Index: 2, Count: 4}, "=SUM(#REF!)"},
		{"=SUM(A:A)+SUM(A1:A3)", StructuralChange{Axis: "row", Action: "insert", Index: 2, Count: 1}, "=SUM(A:A)+SUM(A1:A4)"},
		{"=COUNTIF(A:A,\"2:5\")", StructuralChange{Axis: "row", Action: "insert", Index: 1, Count: 1}, "=COUNTIF(A:A,\"2:5\")"},
	} {
		testCase.change.CurrentSheet, testCase.change.TargetSheet = "Sheet1", "Sheet1"
		if got := TransformStructuralReferences(testCase.formula, testCase.change); got != testCase.expected {
			t.Errorf("%s -> %s, want %s", testCase.formula, got, testCase.expected)
		}
	}
}

// A band on another sheet only moves when that sheet is the one changing.
func TestQualifiedBandFollowsItsOwnSheet(t *testing.T) {
	t.Parallel()
	change := StructuralChange{Axis: "column", Action: "insert", Index: 1, Count: 1, CurrentSheet: "Report", TargetSheet: "Data"}
	if got := TransformStructuralReferences("=SUM(Data!A:A)+SUM(A:A)", change); got != "=SUM(Data!B:B)+SUM(A:A)" {
		t.Fatalf("got %s", got)
	}
	quoted := TransformStructuralReferences("=SUM('Raw Data'!2:4)", StructuralChange{Axis: "row", Action: "insert", Index: 1, Count: 1, CurrentSheet: "Report", TargetSheet: "Raw Data"})
	if quoted != "=SUM('Raw Data'!3:5)" {
		t.Fatalf("got %s", quoted)
	}
}

// Dates live as text in kanpic, so the extremes of a date column have to work
// without asking people to convert anything.
func TestMinAndMaxUnderstandDateColumns(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": "2026-03-15", "A2": "2025-12-01", "A3": "2026-07-04"}
	if result := New().Evaluate("=MIN(A1:A3)", cells); result.Value != "2025-12-01" {
		t.Errorf("MIN=%v (%v)", result.Value, result.Error)
	}
	if result := New().Evaluate("=MAX(A1:A3)", cells); result.Value != "2026-07-04" {
		t.Errorf("MAX=%v (%v)", result.Value, result.Error)
	}
	// Mixed text still reports zero rather than guessing.
	if result := New().Evaluate("=MIN(A1:A3)", map[string]any{"A1": "2026-03-15", "A2": "미정"}); result.Value != 0.0 {
		t.Errorf("mixed MIN=%v", result.Value)
	}
}

// An inline table is how a small lookup or a set of thresholds is written
// without giving up cells for it.
func TestArrayLiteralsBehaveLikeARange(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 5.0, "A2": 9.0}
	for formula, expected := range map[string]any{
		"=SUM({1,2,3})":                     6.0,
		"=SUM({1,2;3,4})":                   10.0,
		"=COUNT({1,2;3,4})":                 4.0,
		"=SUM({A1,A2})":                     14.0,
		"=SUMPRODUCT({1,2,3},{2,2,2})":      12.0,
		`=XLOOKUP(2,{1,2,3},{"하","중","상"})`: "중",
		`=XLOOKUP(120000,{50000;100000;300000},{"보급";"중급";"고급"},"보급",-1)`: "중급",
		"=INDEX({10,20;30,40},2,1)": 30.0,
		"=SUM({1,2},{3,4})":         10.0,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil || result.Value != expected {
			t.Errorf("%s = %v (%v), want %v", formula, result.Value, result.Error, expected)
		}
	}
	// A ragged literal is rejected rather than silently padded.
	if result := New().Evaluate("=SUM({1,2;3})", nil); result.Error == nil {
		t.Fatalf("ragged literal returned %v", result.Value)
	}
	// A semicolon still separates arguments outside a literal.
	if result := New().Evaluate("=SUM(1;2;3)", nil); result.Error != nil || result.Value != 6.0 {
		t.Fatalf("semicolon arguments = %v (%v)", result.Value, result.Error)
	}
	// A literal spills like any other array result.
	spilled := New().Evaluate("={1,2;3,4}", nil)
	if matrix, ok := spilled.Value.([][]any); !ok || len(matrix) != 2 || matrix[1][1] != 4.0 {
		t.Fatalf("literal spill = %v (%v)", spilled.Value, spilled.Error)
	}
}
