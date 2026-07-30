package formula

import "testing"

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
