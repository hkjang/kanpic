package automation

import (
	"encoding/json"
	"testing"

	"kanpic/internal/workbook"
)

func TestValidScalarAcceptsOnlyBoundedPrimitiveValues(t *testing.T) {
	tests := []struct {
		name  string
		value json.RawMessage
		valid bool
	}{
		{name: "string", value: json.RawMessage(`"완료"`), valid: true},
		{name: "number", value: json.RawMessage(`42.5`), valid: true},
		{name: "boolean", value: json.RawMessage(`true`), valid: true},
		{name: "null", value: json.RawMessage(`null`)},
		{name: "array", value: json.RawMessage(`[1]`)},
		{name: "object", value: json.RawMessage(`{"value":1}`)},
		{name: "invalid", value: json.RawMessage(`oops`)},
		{name: "empty", value: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := validScalar(test.value); actual != test.valid {
				t.Fatalf("validScalar(%s)=%v, want %v", test.value, actual, test.valid)
			}
		})
	}
}

func TestTriggerMatchesUsesInclusiveA1Range(t *testing.T) {
	cells := []workbook.CellInput{{Row: 4, Column: 3}, {Row: 10, Column: 10}}
	if !triggerMatches("B2:C4", cells) {
		t.Fatal("expected C4 to match inclusive trigger range")
	}
	if triggerMatches("A1:B3", cells) {
		t.Fatal("unexpected out-of-range match")
	}
	if triggerMatches("invalid", cells) {
		t.Fatal("invalid A1 range must not match")
	}
}

func TestRequiredActionScope(t *testing.T) {
	if scope := RequiredActionScope(ActionSetFormula); scope != "formula.write" {
		t.Fatalf("formula scope=%q", scope)
	}
	for _, action := range []string{ActionSetValue, ActionClear} {
		if scope := RequiredActionScope(action); scope != "range.write" {
			t.Fatalf("%s scope=%q", action, scope)
		}
	}
}
