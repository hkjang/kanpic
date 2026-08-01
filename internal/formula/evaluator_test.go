package formula

import (
	"reflect"
	"testing"
)

func TestMVPFunctionsAndDependencies(t *testing.T) {
	t.Parallel()
	evaluator := New()
	result := evaluator.Evaluate(`=IF(SUM(A1:A3)>=10, CONCAT("달성-", ROUND(AVERAGE(A1:A3), 1)), "미달")`, map[string]any{"A1": 2.0, "A2": 3.0, "A3": 7.0})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if result.Value != "달성-4" {
		t.Fatalf("value = %#v", result.Value)
	}
	if len(result.Dependencies) != 3 {
		t.Fatalf("dependencies = %#v", result.Dependencies)
	}
}

func TestTextAndDateFunctions(t *testing.T) {
	t.Parallel()
	evaluator := New()
	for formula, want := range map[string]any{
		`=LEFT("kanpic",3)&RIGHT("sheet",2)`: "kanet",
		`=MID("spreadsheet",7,5)`:            "sheet",
		`=DATE(2026,7,30)`:                   "2026-07-30",
	} {
		result := evaluator.Evaluate(formula, nil)
		if result.Error != nil || result.Value != want {
			t.Errorf("%s = %#v, %v; want %#v", formula, result.Value, result.Error, want)
		}
	}
}

func TestFormulaErrors(t *testing.T) {
	t.Parallel()
	evaluator := New()
	for formula, code := range map[string]string{"=1/0": "#DIV/0!", "=UNKNOWN(1)": "#NAME?", "=SUM(": "#ERROR!"} {
		result := evaluator.Evaluate(formula, nil)
		if result.Error == nil || result.Error.Code != code {
			t.Errorf("%s error = %#v; want %s", formula, result.Error, code)
		}
	}
}

func TestConditionalAggregateFunctions(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": "North", "A2": "North", "A3": "South", "A4": "Northeast",
		"B1": 10, "B2": 15, "B3": 25, "B4": 5,
		"C1": "active", "C2": "inactive", "C3": "active", "C4": "active",
		"D1": "*", "D2": "star",
	}
	tests := []struct {
		formula string
		want    any
	}{
		{`=SUMIF(A1:A4,"North",B1:B4)`, float64(25)},
		{`=COUNTIF(A1:A4,"North*")`, float64(3)},
		{`=COUNTIF(B1:B4,">=15")`, float64(2)},
		{`=SUMIFS(B1:B4,A1:A4,"North*",C1:C4,"active")`, float64(15)},
		{`=COUNTIFS(A1:A4,"<>South",B1:B4,">5")`, float64(2)},
		{`=COUNTIF(D1:D2,"~*")`, float64(1)},
	}
	for _, test := range tests {
		result := New().Evaluate(test.formula, cells)
		if result.Error != nil || !reflect.DeepEqual(result.Value, test.want) {
			t.Errorf("%s = %#v, %v; want %#v", test.formula, result.Value, result.Error, test.want)
		}
	}
	result := New().Evaluate(`=SUMIF(A1:A2,"North",B1:B3)`, cells)
	if result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Fatalf("mismatched SUMIF error = %#v", result.Error)
	}
}

func TestLookupFunctions(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": "Alice", "B1": 10, "C1": "East",
		"A2": "Bob", "B2": 20, "C2": "West",
		"A3": "Carla", "B3": 30, "C3": "North",
		"D1": 1, "E1": "low", "D2": 5, "E2": "mid", "D3": 10, "E3": "high",
		"F1": 10, "F2": 8, "F3": 5,
		"G1": "Jan", "H1": "Feb", "I1": "Mar", "G2": 100, "H2": 200, "I2": 300,
	}
	tests := []struct {
		formula string
		want    any
	}{
		{`=VLOOKUP("Bob",A1:C3,2,FALSE)`, 20},
		{`=VLOOKUP("C*",A1:C3,3,FALSE)`, "North"},
		{`=VLOOKUP(7,D1:E3,2,TRUE)`, "mid"},
		{`=HLOOKUP("Feb",G1:I2,2,FALSE)`, 200},
		{`=INDEX(A1:C3,2,3)`, "West"},
		{`=INDEX(A1:C1,2)`, 10},
		{`=MATCH("Bob",A1:A3,0)`, float64(2)},
		{`=MATCH(7,D1:D3,1)`, float64(2)},
		{`=MATCH(7,F1:F3,-1)`, float64(2)},
	}
	for _, test := range tests {
		result := New().Evaluate(test.formula, cells)
		if result.Error != nil || !reflect.DeepEqual(result.Value, test.want) {
			t.Errorf("%s = %#v, %v; want %#v", test.formula, result.Value, result.Error, test.want)
		}
	}
	for formula, code := range map[string]string{
		`=VLOOKUP("missing",A1:C3,2,FALSE)`: "#N/A",
		`=VLOOKUP("Alice",A1:C3,4,FALSE)`:   "#REF!",
		`=MATCH("Alice",A1:B2,0)`:           "#N/A",
	} {
		result := New().Evaluate(formula, cells)
		if result.Error == nil || result.Error.Code != code {
			t.Errorf("%s error = %#v; want %s", formula, result.Error, code)
		}
	}
}

func TestFilterSortAndArrayOperators(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": "a", "B1": 30,
		"A2": "b", "B2": 10,
		"A3": "c", "B3": 20,
		"A4": "d", "B4": 20,
	}
	tests := []struct {
		formula string
		want    any
	}{
		{`=FILTER(A1:B4,B1:B4>=20)`, [][]any{{"a", 30}, {"c", 20}, {"d", 20}}},
		{`=FILTER(A1:B4,B1:B4>=20,A1:A4<>"a")`, [][]any{{"c", 20}, {"d", 20}}},
		{`=FILTER(A1:B4,B1:B4>100,"none")`, "none"},
		{`=SORT(A1:B4,2,1)`, [][]any{{"b", 10}, {"c", 20}, {"d", 20}, {"a", 30}}},
		{`=SORT(A1:B4,2,FALSE,1,TRUE)`, [][]any{{"a", 30}, {"c", 20}, {"d", 20}, {"b", 10}}},
		{`=B1:B3*2`, [][]any{{float64(60)}, {float64(20)}, {float64(40)}}},
		{`=SUM(FILTER(B1:B4,B1:B4>=20))`, float64(70)},
	}
	for _, test := range tests {
		result := New().Evaluate(test.formula, cells)
		if result.Error != nil || !reflect.DeepEqual(result.Value, test.want) {
			t.Errorf("%s = %#v, %v; want %#v", test.formula, result.Value, result.Error, test.want)
		}
	}
	result := New().Evaluate(`=A1:A2+B1:B3`, cells)
	if result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Fatalf("mismatched array error = %#v", result.Error)
	}
}

func TestGraphPublishesAndConsumesArrayResults(t *testing.T) {
	t.Parallel()
	result, err := New().Recalculate(map[string]CellState{
		"A1": {Value: "a"}, "B1": {Value: 5},
		"A2": {Value: "b"}, "B2": {Value: 15},
		"C1": {Formula: `=FILTER(A1:B2,B1:B2>10)`},
		"D1": {Formula: `=SUM(C1)`},
	}, []string{"B2"})
	if err != nil {
		t.Fatal(err)
	}
	byAddress := make(map[string]RecalculatedCell)
	for _, cell := range result.Cells {
		byAddress[cell.Address] = cell
	}
	if !reflect.DeepEqual(byAddress["C1"].Value, [][]any{{"b", 15}}) || byAddress["D1"].Value != float64(15) {
		t.Fatalf("array graph result = %#v", byAddress)
	}
}
