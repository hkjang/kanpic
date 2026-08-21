package workbook

import (
	"context"
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func noteSheet(t *testing.T) (*MemoryRepository, Workbook) {
	t.Helper()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "메모", WorkspaceID: "default", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return repository, book
}

func readCell(t *testing.T, repository *MemoryRepository, sheetID string, row, column int) Cell {
	t.Helper()
	cells, err := repository.ReadRange(context.Background(), sheetID, cellrange.Range{Start: cellrange.Position{Row: row, Column: column}, End: cellrange.Position{Row: row, Column: column}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) == 0 {
		return Cell{}
	}
	return cells[0]
}

// A note annotates a cell without disturbing what the cell holds, which is the
// whole reason it is not a comment.
func TestNotesLeaveTheCellContentAlone(t *testing.T) {
	t.Parallel()
	repository, book := noteSheet(t)
	sheetID := book.Sheets[0].ID
	value, _ := json.Marshal(1200)
	style := json.RawMessage(`{"bold":true}`)
	if _, err := repository.ApplyCells(context.Background(), CellMutation{
		SheetID: sheetID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "write",
		Cells: []CellInput{{Row: 2, Column: 3, Value: value, Style: style}},
	}); err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	note := "협력사 단가 재확인 필요"
	if _, err := repository.ApplyCells(context.Background(), CellMutation{
		SheetID: sheetID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "note",
		Cells: []CellInput{{Row: 2, Column: 3}}, NotePatch: &note, OperationType: "range.note",
	}); err != nil {
		t.Fatal(err)
	}
	cell := readCell(t, repository, sheetID, 2, 3)
	if cell.Note != note {
		t.Fatalf("note=%q", cell.Note)
	}
	if string(cell.Value) != "1200" || string(cell.Style) != `{"bold":true}` {
		t.Fatalf("the note disturbed the cell: %#v", cell)
	}

	// Writing the cell again keeps whatever the writer sent, so a plain edit
	// from a client that knows the note preserves it.
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	updated, _ := json.Marshal(1500)
	if _, err := repository.ApplyCells(context.Background(), CellMutation{
		SheetID: sheetID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "edit",
		Cells: []CellInput{{Row: 2, Column: 3, Value: updated, Style: style, Note: note}},
	}); err != nil {
		t.Fatal(err)
	}
	if cell := readCell(t, repository, sheetID, 2, 3); cell.Note != note || string(cell.Value) != "1500" {
		t.Fatalf("after edit=%#v", cell)
	}
}

// A note is content in its own right: an otherwise empty cell that carries one
// must survive, and clearing the note must remove the cell again.
func TestANoteKeepsAnEmptyCellAndClearingItRemovesTheCell(t *testing.T) {
	t.Parallel()
	repository, book := noteSheet(t)
	sheetID := book.Sheets[0].ID
	note := "여기에 실적을 입력하세요"
	if _, err := repository.ApplyCells(context.Background(), CellMutation{
		SheetID: sheetID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "note",
		Cells: []CellInput{{Row: 4, Column: 1}}, NotePatch: &note, OperationType: "range.note",
	}); err != nil {
		t.Fatal(err)
	}
	if cell := readCell(t, repository, sheetID, 4, 1); cell.Note != note {
		t.Fatalf("empty cell lost its note: %#v", cell)
	}
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	empty := ""
	if _, err := repository.ApplyCells(context.Background(), CellMutation{
		SheetID: sheetID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "clear",
		Cells: []CellInput{{Row: 4, Column: 1}}, NotePatch: &empty, OperationType: "range.note",
	}); err != nil {
		t.Fatal(err)
	}
	if cell := readCell(t, repository, sheetID, 4, 1); cell.Note != "" || len(cell.Value) != 0 {
		t.Fatalf("clearing left %#v", cell)
	}
}

// Notes belong to the cell, so they move when rows move.
func TestNotesMoveWithInsertedRows(t *testing.T) {
	t.Parallel()
	repository, book := noteSheet(t)
	sheetID := book.Sheets[0].ID
	note := "분기 마감 확인"
	if _, err := repository.ApplyCells(context.Background(), CellMutation{
		SheetID: sheetID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "note",
		Cells: []CellInput{{Row: 5, Column: 2}}, NotePatch: &note, OperationType: "range.note",
	}); err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	if _, err := repository.ApplyStructure(context.Background(), StructuralMutation{
		SheetID: sheetID, ActorID: "owner", IdempotencyKey: "insert", BaseVersion: book.Version,
		Axis: "row", Action: "insert", Index: 2, Count: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if cell := readCell(t, repository, sheetID, 7, 2); cell.Note != note {
		t.Fatalf("the note did not move: %#v", cell)
	}
	if cell := readCell(t, repository, sheetID, 5, 2); cell.Note != "" {
		t.Fatalf("the note stayed behind: %#v", cell)
	}
}
