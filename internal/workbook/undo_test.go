package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestUndoAndRedoRecalculateDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	wb, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "undo formulas", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	_, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 1, IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`2`)},
		{Row: 1, Column: 2, Formula: "=A1*2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 2, IdempotencyKey: "change", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`3`)}}})
	if err != nil {
		t.Fatal(err)
	}
	undone, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: changed.OperationID, ActorID: "alice", IdempotencyKey: "undo-change"})
	if err != nil || undone.AppliedCells != 1 || len(undone.RecalculatedCells) != 1 {
		t.Fatalf("undo: %#v, %v", undone, err)
	}
	assertRangeValues(t, repository, sheetID, "A1:B1", []string{"2", "4"})
	duplicate, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: changed.OperationID, ActorID: "alice", IdempotencyKey: "undo-change"})
	if err != nil || !duplicate.Duplicate || duplicate.OperationID != undone.OperationID {
		t.Fatalf("idempotent undo: %#v, %v", duplicate, err)
	}

	redone, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: undone.OperationID, ActorID: "alice", IdempotencyKey: "redo-change"})
	if err != nil || redone.AppliedCells != 1 || len(redone.RecalculatedCells) != 1 {
		t.Fatalf("redo: %#v, %v", redone, err)
	}
	assertRangeValues(t, repository, sheetID, "A1:B1", []string{"3", "6"})
}

func TestSelectiveUndoDoesNotOverwriteAnotherActorsChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	wb, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "collaborative undo", OwnerID: "alice"})
	sheetID := wb.Sheets[0].ID
	alice, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 1, IdempotencyKey: "alice-1", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`1`)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "bob", BaseVersion: 2, IdempotencyKey: "bob-1", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`2`)}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: alice.OperationID, ActorID: "alice", IdempotencyKey: "alice-undo"})
	if err != nil || result.AppliedCells != 0 || len(result.Conflicts) != 1 {
		t.Fatalf("selective undo: %#v, %v", result, err)
	}
	assertRangeValues(t, repository, sheetID, "A1", []string{"2"})
	if _, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: alice.OperationID, ActorID: "bob", IdempotencyKey: "bob-undo-alice"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another actor can discover or undo operation: %v", err)
	}
}

func assertRangeValues(t *testing.T, repository Repository, sheetID, selectedRange string, expected []string) {
	t.Helper()
	selected, err := cellrange.Parse(selectedRange)
	if err != nil {
		t.Fatal(err)
	}
	cells, err := repository.ReadRange(context.Background(), sheetID, selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != len(expected) {
		t.Fatalf("%s returned %d cells, want %d", selectedRange, len(cells), len(expected))
	}
	for index, value := range expected {
		if string(cells[index].Value) != value {
			t.Fatalf("%s cell %d = %s, want %s", selectedRange, index, cells[index].Value, value)
		}
	}
}
