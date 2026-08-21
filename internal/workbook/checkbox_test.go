package workbook

import (
	"encoding/json"
	"testing"
)

// checkboxAccepts runs one value through the rule the way a cell write does.
func checkboxAccepts(t *testing.T, rule DataValidation, value string) bool {
	t.Helper()
	evaluation, err := EvaluateDataValidation(rule, []Cell{{Row: 2, Column: 2, Value: json.RawMessage(value)}})
	if err != nil {
		t.Fatal(err)
	}
	// The rule covers a whole range, so only the cell under test matters; the
	// other cells in the range are blank and fail for their own reason.
	for _, violation := range evaluation.InvalidCells {
		if violation.Row == 2 && violation.Column == 2 {
			return false
		}
	}
	return true
}

func checkboxRule(options []ValidationOption) DataValidation {
	return DataValidation{Range: "B2:B10", RuleType: "checkbox", Options: options, CreateKey: "key"}
}

// A checkbox is the two value list the grid draws as a box, so it has to come
// with a sensible pair without anybody configuring one.
func TestCheckboxDefaultsToTrueAndFalse(t *testing.T) {
	t.Parallel()
	rule, _, err := NormalizeDataValidation(checkboxRule(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rule.Options) != 2 || string(rule.Options[0].Value) != "true" || string(rule.Options[1].Value) != "false" {
		t.Fatalf("options=%#v", rule.Options)
	}
	if rule.ShowDropdown || rule.DisplayStyle != "plain" {
		t.Fatalf("a checkbox should not draw a dropdown: %#v", rule)
	}
	// Both values pass the check and anything else fails.
	for value, expected := range map[string]bool{"true": true, "false": true, `"예"`: false, "1": false} {
		if valid := checkboxAccepts(t, rule, value); valid != expected {
			t.Errorf("%s valid=%v, want %v", value, valid, expected)
		}
	}
}

// A column of 예/아니오 is still a checkbox, which is why the values are
// configurable rather than fixed to booleans.
func TestCheckboxAcceptsItsOwnPairOfValues(t *testing.T) {
	t.Parallel()
	rule, _, err := NormalizeDataValidation(checkboxRule([]ValidationOption{
		{Value: json.RawMessage(`"예"`)}, {Value: json.RawMessage(`"아니오"`)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if rule.Options[0].Label != "예" || rule.Options[1].Label != "아니오" {
		t.Fatalf("labels=%#v", rule.Options)
	}
	if !checkboxAccepts(t, rule, `"예"`) {
		t.Fatal("the checked value should pass")
	}
	if checkboxAccepts(t, rule, "true") {
		t.Fatal("a value outside the pair should fail")
	}
}

func TestCheckboxRefusesWhatItCannotDraw(t *testing.T) {
	t.Parallel()
	for name, options := range map[string][]ValidationOption{
		"one value":       {{Value: json.RawMessage("true")}},
		"three values":    {{Value: json.RawMessage("true")}, {Value: json.RawMessage("false")}, {Value: json.RawMessage(`"?"`)}},
		"the same twice":  {{Value: json.RawMessage("true")}, {Value: json.RawMessage("true")}},
		"a missing value": {{Value: json.RawMessage("null")}, {Value: json.RawMessage("false")}},
	} {
		if _, _, err := NormalizeDataValidation(checkboxRule(options)); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}
