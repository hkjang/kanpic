package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestCellConflictLifecycleComparesAndRestoresWithoutSilentOverwrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "conflict", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	first, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", ClientID: "alice-browser", BaseVersion: 1, IdempotencyKey: "alice-first", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`"first"`), Style: json.RawMessage(`{"bold":true}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "bob", ClientID: "bob-browser", BaseVersion: 1, IdempotencyKey: "bob-stale", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`"second"`), Style: json.RawMessage(`{"italic":true}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if first.ServerVersion != 2 || second.ServerVersion != 3 || len(second.Conflicts) != 1 {
		t.Fatalf("unexpected versions/conflicts: first=%#v second=%#v", first, second)
	}
	conflict := second.Conflicts[0]
	if conflict.ID == "" || conflict.OperationID != second.OperationID || conflict.Status != ConflictStatusOpen || conflict.Revision != 1 || conflict.ConflictingActorID != "alice" {
		t.Fatalf("incomplete conflict metadata: %#v", conflict)
	}
	if string(conflict.BaseCell.Value) != "" || string(conflict.ConflictingCell.Value) != `"first"` || string(conflict.SubmittedCell.Value) != `"second"` || string(conflict.AppliedCell.Value) != `"second"` {
		t.Fatalf("incorrect conflict timeline: %#v", conflict)
	}
	if string(conflict.ConflictingCell.Style) != `{"bold":true}` || string(conflict.SubmittedCell.Style) != `{"italic":true}` {
		t.Fatalf("styles were not retained: %#v", conflict)
	}

	open, err := repository.ListCellConflicts(ctx, book.ID, false)
	if err != nil || len(open) != 1 || string(open[0].CurrentCell.Value) != `"second"` {
		t.Fatalf("open conflict list: %#v, %v", open, err)
	}
	resolved, err := repository.ResolveCellConflict(ctx, conflict.ID, ResolveCellConflictInput{ActorID: "bob", ClientID: "bob-browser", IdempotencyKey: "resolve-restore", ExpectedRevision: 1, Resolution: ConflictRestorePrevious})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Conflict.Status != ConflictStatusResolved || resolved.Conflict.Resolution != ConflictRestorePrevious || resolved.Conflict.Revision != 2 || resolved.Operation.ServerVersion != 4 || resolved.Conflict.ResolutionOperationID != resolved.Operation.OperationID {
		t.Fatalf("resolution result: %#v", resolved)
	}
	cells, err := repository.ReadRange(ctx, sheetID, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 1, Column: 1}})
	if err != nil || len(cells) != 1 || string(cells[0].Value) != `"first"` || string(cells[0].Style) != `{"bold":true}` {
		t.Fatalf("restored cell: %#v, %v", cells, err)
	}
	open, err = repository.ListCellConflicts(ctx, book.ID, false)
	if err != nil || len(open) != 0 {
		t.Fatalf("resolved conflict remained open: %#v, %v", open, err)
	}
	history, err := repository.ListCellConflicts(ctx, book.ID, true)
	if err != nil || len(history) != 1 || history[0].ResolutionServerVersion != 4 {
		t.Fatalf("conflict history: %#v, %v", history, err)
	}
	duplicate, err := repository.ResolveCellConflict(ctx, conflict.ID, ResolveCellConflictInput{ActorID: "bob", IdempotencyKey: "resolve-restore", ExpectedRevision: 1, Resolution: ConflictRestorePrevious})
	if err != nil || !duplicate.Operation.Duplicate || duplicate.Operation.OperationID != resolved.Operation.OperationID {
		t.Fatalf("idempotent resolution: %#v, %v", duplicate, err)
	}
}

func TestCellConflictRestoreRejectsAChangedCurrentCell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "safe restore", OwnerID: "owner"})
	sheetID := book.Sheets[0].ID
	_, _ = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", ClientID: "a", BaseVersion: 1, IdempotencyKey: "one", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`1`)}}})
	stale, _ := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "bob", ClientID: "b", BaseVersion: 1, IdempotencyKey: "two", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`2`)}}})
	_, _ = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "carol", ClientID: "c", BaseVersion: 3, IdempotencyKey: "three", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`3`)}}})
	_, err := repository.ResolveCellConflict(ctx, stale.Conflicts[0].ID, ResolveCellConflictInput{ActorID: "bob", IdempotencyKey: "unsafe-restore", ExpectedRevision: 1, Resolution: ConflictRestorePrevious})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	item, err := repository.GetCellConflict(ctx, stale.Conflicts[0].ID)
	if err != nil || item.Status != ConflictStatusOpen || string(item.CurrentCell.Value) != `3` {
		t.Fatalf("conflict/current state changed after rejected restore: %#v, %v", item, err)
	}
}
