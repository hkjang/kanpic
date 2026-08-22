package workbook

import (
	"context"
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestCrossSheetFormulaRecalculatesAndUndoesWithSource(t *testing.T) {
	repository := NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "cross sheet", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	inputSheet := wb.Sheets[0]
	reportSheet, err := repository.CreateSheet(ctx, wb.ID, CreateSheetInput{Name: "Sales Report"})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(10)
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: inputSheet.ID, ActorID: "alice", BaseVersion: 2, IdempotencyKey: "cross-seed", Cells: []CellInput{{Row: 1, Column: 1, Value: value}}})
	if err != nil {
		t.Fatal(err)
	}
	formulaResult, err := repository.ApplyCells(ctx, CellMutation{SheetID: reportSheet.ID, ActorID: "alice", BaseVersion: seed.ServerVersion, IdempotencyKey: "cross-formula", Cells: []CellInput{{Row: 1, Column: 2, Formula: `='Sheet1'!A1*2`}}})
	if err != nil || len(formulaResult.FormulaErrors) != 0 {
		t.Fatalf("cross formula result=%#v error=%v", formulaResult, err)
	}
	assertCellJSONValue(t, repository, reportSheet.ID, "B1", float64(20))
	newName := "Raw Data"
	if _, err := repository.UpdateSheet(ctx, inputSheet.ID, UpdateSheetInput{Name: &newName}); err != nil {
		t.Fatal(err)
	}
	formulaCells, err := repository.ReadRange(ctx, reportSheet.ID, mustRange(t, "B1"))
	if err != nil || len(formulaCells) != 1 || formulaCells[0].Formula != `='Raw Data'!A1*2` {
		t.Fatalf("formula after sheet rename=%#v error=%v", formulaCells, err)
	}
	latest, err := repository.GetWorkbook(ctx, wb.ID)
	if err != nil {
		t.Fatal(err)
	}

	updatedValue, _ := json.Marshal(25)
	updated, err := repository.ApplyCells(ctx, CellMutation{SheetID: inputSheet.ID, ActorID: "alice", BaseVersion: latest.Version, IdempotencyKey: "cross-update", Cells: []CellInput{{Row: 1, Column: 1, Value: updatedValue}}})
	if err != nil || len(updated.RecalculatedCells) != 1 || updated.RecalculatedCells[0].SheetID != reportSheet.ID {
		t.Fatalf("cross update=%#v error=%v", updated, err)
	}
	assertCellJSONValue(t, repository, reportSheet.ID, "B1", float64(50))

	undone, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: updated.OperationID, ActorID: "alice", IdempotencyKey: "cross-undo"})
	if err != nil || len(undone.RecalculatedCells) != 1 || undone.RecalculatedCells[0].SheetID != reportSheet.ID {
		t.Fatalf("cross undo=%#v error=%v", undone, err)
	}
	assertCellJSONValue(t, repository, inputSheet.ID, "A1", float64(10))
	assertCellJSONValue(t, repository, reportSheet.ID, "B1", float64(20))
}

func TestCrossSheetDynamicArraySpillsOnDependentSheet(t *testing.T) {
	repository := NewMemoryRepository()
	ctx := context.Background()
	wb, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "cross spill", OwnerID: "alice"})
	inputSheet := wb.Sheets[0]
	reportSheet, _ := repository.CreateSheet(ctx, wb.ID, CreateSheetInput{Name: "Report"})
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: inputSheet.ID, ActorID: "alice", BaseVersion: 2, IdempotencyKey: "cross-spill-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: value("a")}, {Row: 1, Column: 2, Value: value(5)},
		{Row: 2, Column: 1, Value: value("b")}, {Row: 2, Column: 2, Value: value(15)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.ApplyCells(ctx, CellMutation{SheetID: reportSheet.ID, ActorID: "alice", BaseVersion: seed.ServerVersion, IdempotencyKey: "cross-spill-formula", Cells: []CellInput{{Row: 1, Column: 4, Formula: `=FILTER(Sheet1!A1:B2,Sheet1!B1:B2>=10)`}}})
	if err != nil || len(created.FormulaErrors) != 0 {
		t.Fatalf("cross spill=%#v error=%v", created, err)
	}
	cells, err := repository.ReadRange(ctx, reportSheet.ID, mustRange(t, "D1:E1"))
	if err != nil || len(cells) != 2 || cells[1].SpillSource != "D1" {
		t.Fatalf("cross spill cells=%#v error=%v", cells, err)
	}
}

func TestDeletingReferencedSheetInvalidatesDependentFormulas(t *testing.T) {
	repository := NewMemoryRepository()
	ctx := context.Background()
	wb, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "delete reference", OwnerID: "alice"})
	inputSheet := wb.Sheets[0]
	reportSheet, _ := repository.CreateSheet(ctx, wb.ID, CreateSheetInput{Name: "Report"})
	created, err := repository.ApplyCells(ctx, CellMutation{SheetID: reportSheet.ID, ActorID: "alice", BaseVersion: 2, IdempotencyKey: "delete-reference-formulas", Cells: []CellInput{
		{Row: 1, Column: 1, Formula: `=Sheet1!A1`},
		{Row: 1, Column: 2, Formula: `=A1+1`},
	}})
	if err != nil || len(created.FormulaErrors) != 0 {
		t.Fatalf("create references=%#v error=%v", created, err)
	}
	if _, err := repository.DeleteSheet(ctx, inputSheet.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	cells, err := repository.ReadRange(ctx, reportSheet.ID, mustRange(t, "A1:B1"))
	if err != nil || len(cells) != 2 || string(cells[0].Value) != `"#REF!"` || string(cells[1].Value) != `"#REF!"` {
		t.Fatalf("invalidated references=%#v error=%v", cells, err)
	}
}

func assertCellJSONValue(t *testing.T, repository Repository, sheetID, address string, want any) {
	t.Helper()
	cells, err := repository.ReadRange(context.Background(), sheetID, mustRange(t, address))
	if err != nil || len(cells) != 1 {
		t.Fatalf("read %s: cells=%#v error=%v", address, cells, err)
	}
	var got any
	if err := json.Unmarshal(cells[0].Value, &got); err != nil || got != want {
		t.Fatalf("%s value=%#v want=%#v error=%v", address, got, want, err)
	}
}

func mustRange(t *testing.T, value string) cellrange.Range {
	t.Helper()
	selected, err := cellrange.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}
