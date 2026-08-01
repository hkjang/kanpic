package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestArrayFormulaSpillsShrinksAndRecalculatesDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "arrays", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	seed := []CellInput{
		{Row: 1, Column: 1, Value: arrayRaw(`"a"`)}, {Row: 1, Column: 2, Value: arrayRaw(`30`)},
		{Row: 2, Column: 1, Value: arrayRaw(`"b"`)}, {Row: 2, Column: 2, Value: arrayRaw(`10`)},
		{Row: 3, Column: 1, Value: arrayRaw(`"c"`)}, {Row: 3, Column: 2, Value: arrayRaw(`20`)},
		{Row: 4, Column: 1, Value: arrayRaw(`"d"`)}, {Row: 4, Column: 2, Value: arrayRaw(`20`)},
	}
	if _, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 1, IdempotencyKey: "array-seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	spilled, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 2, IdempotencyKey: "array-formula", Cells: []CellInput{{Row: 1, Column: 4, Formula: `=FILTER(A1:B4,B1:B4>=20)`}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(spilled.FormulaErrors) != 0 || len(spilled.RecalculatedCells) != 6 {
		t.Fatalf("spill result = %#v", spilled)
	}
	assertSpillCells(t, repository, sheetID, "D1:E3", []string{`"a"`, `30`, `"c"`, `20`, `"d"`, `20`}, "D1")

	if _, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 3, IdempotencyKey: "edit-spill-child", Cells: []CellInput{{Row: 2, Column: 4, Value: arrayRaw(`"blocked"`)}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("editing a spill child error = %v", err)
	}
	if _, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 3, IdempotencyKey: "spill-dependent", Cells: []CellInput{{Row: 1, Column: 7, Formula: `=SUM(E1:E3)`}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 4, IdempotencyKey: "shrink-spill-once", Cells: []CellInput{{Row: 1, Column: 2, Value: arrayRaw(`10`)}}}); err != nil {
		t.Fatal(err)
	}
	assertSpillCells(t, repository, sheetID, "D1:E3", []string{`"c"`, `20`, `"d"`, `20`}, "D1")
	assertCellRaw(t, repository, sheetID, "G1", `40`)

	if _, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 5, IdempotencyKey: "shrink-spill-twice", Cells: []CellInput{{Row: 3, Column: 2, Value: arrayRaw(`5`)}}}); err != nil {
		t.Fatal(err)
	}
	assertSpillCells(t, repository, sheetID, "D1:E3", []string{`"d"`, `20`}, "D1")
	assertCellRaw(t, repository, sheetID, "G1", `20`)
}

func TestBlockedArrayFormulaRecoversWhenBlockerIsCleared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "blocked array", OwnerID: "alice"})
	sheetID := book.Sheets[0].ID
	seed := []CellInput{
		{Row: 1, Column: 1, Value: arrayRaw(`"a"`)}, {Row: 1, Column: 2, Value: arrayRaw(`2`)},
		{Row: 2, Column: 1, Value: arrayRaw(`"b"`)}, {Row: 2, Column: 2, Value: arrayRaw(`3`)},
		{Row: 2, Column: 5, Value: arrayRaw(`"occupied"`)},
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 1, IdempotencyKey: "blocked-seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	blocked, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 2, IdempotencyKey: "blocked-formula", Cells: []CellInput{{Row: 1, Column: 4, Formula: `=FILTER(A1:B2,B1:B2>0)`}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.FormulaErrors) != 1 || blocked.FormulaErrors[0].Code != "#SPILL!" {
		t.Fatalf("blocked formula result = %#v", blocked)
	}
	assertCellRaw(t, repository, sheetID, "D1", `"#SPILL!"`)

	recovered, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 3, IdempotencyKey: "clear-blocker", Cells: []CellInput{{Row: 2, Column: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.FormulaErrors) != 0 {
		t.Fatalf("recovered formula errors = %#v", recovered.FormulaErrors)
	}
	assertSpillCells(t, repository, sheetID, "D1:E2", []string{`"a"`, `2`, `"b"`, `3`}, "D1")
}

func TestUndoArrayFormulaRemovesEverySpillCell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "undo array", OwnerID: "alice"})
	sheetID := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 1, IdempotencyKey: "undo-array-seed", Cells: []CellInput{{Row: 1, Column: 1, Value: arrayRaw(`1`)}, {Row: 2, Column: 1, Value: arrayRaw(`2`)}}}); err != nil {
		t.Fatal(err)
	}
	created, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 2, IdempotencyKey: "undo-array-formula", Cells: []CellInput{{Row: 1, Column: 3, Formula: `=A1:A2*10`}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.UndoOperation(ctx, UndoOperationInput{OperationID: created.OperationID, ActorID: "alice", ClientID: "test", IdempotencyKey: "undo-array"}); err != nil {
		t.Fatal(err)
	}
	cells, err := repository.ReadRange(ctx, sheetID, mustArrayRange(t, "C1:C2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 0 {
		t.Fatalf("spill cells after undo = %#v", cells)
	}
}

func assertSpillCells(t *testing.T, repository Repository, sheetID, selected string, values []string, source string) {
	t.Helper()
	cells, err := repository.ReadRange(context.Background(), sheetID, mustArrayRange(t, selected))
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != len(values) {
		t.Fatalf("%s cells = %#v; want %d", selected, cells, len(values))
	}
	for index, cell := range cells {
		if string(cell.Value) != values[index] {
			t.Fatalf("%s value[%d] = %s; want %s", selected, index, cell.Value, values[index])
		}
		if index == 0 {
			if cell.Formula == "" || cell.SpillSource != "" {
				t.Fatalf("anchor = %#v", cell)
			}
		} else if cell.SpillSource != source || cell.Formula != "" {
			t.Fatalf("spill child[%d] = %#v", index, cell)
		}
	}
}

func assertCellRaw(t *testing.T, repository Repository, sheetID, address, want string) {
	t.Helper()
	cells, err := repository.ReadRange(context.Background(), sheetID, mustArrayRange(t, address))
	if err != nil || len(cells) != 1 || string(cells[0].Value) != want {
		t.Fatalf("%s = %#v, %v; want %s", address, cells, err, want)
	}
}

func arrayRaw(value string) json.RawMessage { return json.RawMessage(value) }

func mustArrayRange(t *testing.T, value string) cellrange.Range {
	t.Helper()
	selected, err := cellrange.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}
