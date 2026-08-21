package automation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"kanpic/internal/workbook"
)

func TestIdempotencyKeyLimitCountsCharacters(t *testing.T) {
	if !validIdempotencyKey(strings.Repeat("가", 200)) || validIdempotencyKey(strings.Repeat("가", 201)) || validIdempotencyKey(" \t") {
		t.Fatal("idempotency key must contain 1 to 200 Unicode characters")
	}
}

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

func TestFormulaSnapshotNoOpKeepsStableFormulaButRecalculatesVolatileFormula(t *testing.T) {
	style := json.RawMessage(`{"bold":true}`)
	stableBefore := CellSnapshot{Value: json.RawMessage(`6`), Formula: "=A1*2", Style: style}
	stableAfter := CellSnapshot{Formula: "=A1*2", Style: style}
	if !snapshotsEquivalentForAction(stableBefore, stableAfter, ActionSetFormula) {
		t.Fatal("the computed value must not make an unchanged stable formula look modified")
	}
	volatileBefore := CellSnapshot{Value: json.RawMessage(`1`), Formula: "=RAND()", Style: style}
	volatileAfter := CellSnapshot{Formula: "=RAND()", Style: style}
	if snapshotsEquivalentForAction(volatileBefore, volatileAfter, ActionSetFormula) {
		t.Fatal("volatile formulas must still be recalculated")
	}
}

func TestReplayRunPreservesOriginalOperationAndReplaysFailureAsError(t *testing.T) {
	original := workbook.MutationResult{OperationID: "run-operation"}
	undo := workbook.MutationResult{OperationID: "undo-operation"}
	replayed, err := replayRun(Run{Status: StatusUndone, Operation: &original, UndoOperation: &undo})
	if err != nil || !replayed.Run.Duplicate || replayed.Operation.OperationID != original.OperationID {
		t.Fatalf("undone run replay=%#v, %v", replayed, err)
	}
	failed, err := replayRun(Run{Status: StatusFailed, ErrorMessage: "write conflict"})
	if !errors.Is(err, ErrRunFailed) || !failed.Run.Duplicate {
		t.Fatalf("failed run replay=%#v, %v", failed, err)
	}
}
