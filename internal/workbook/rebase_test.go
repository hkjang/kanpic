package workbook

import (
	"context"
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

// Two people work at once: one deletes a row, the other saves a cell that was
// below it. The second write names a row number from before the delete, so
// applying it as written puts the value one row too low and destroys a value
// nobody touched. This is silent: no error, no conflict, wrong data.
func TestAWriteComposedBeforeARowDeleteLandsOnTheRightRow(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "동시 편집", OwnerID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	seed := make([]CellInput, 0, 6)
	for row := 1; row <= 6; row++ {
		seed = append(seed, CellInput{Row: row, Column: 1, Value: json.RawMessage(`"행` + string(rune('0'+row)) + `"`)})
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "a", IdempotencyKey: "seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	stale, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet, ActorID: "a", BaseVersion: stale.Version, IdempotencyKey: "delete", Axis: "row", Action: "delete", Index: 3, Count: 1}); err != nil {
		t.Fatal(err)
	}
	// B still sees 행5 at A5 and saves there.
	result, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "b", BaseVersion: stale.Version, IdempotencyKey: "late",
		Cells: []CellInput{{Row: 5, Column: 1, Value: json.RawMessage(`"B가 고친 값"`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RebasedCells != 1 {
		t.Errorf("rebased cells: %d", result.RebasedCells)
	}
	selected, _ := cellrange.Parse("A1:A5")
	read, err := repository.ReadRange(ctx, sheet, selected)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int]string, len(read))
	for _, cell := range read {
		values[cell.Row] = string(cell.Value)
	}
	// 행5 moved up to row 4, so that is where B's edit belongs.
	if values[4] != `"B가 고친 값"` {
		t.Errorf("A4 = %s, want B's edit", values[4])
	}
	// 행6 was never touched by anybody and has to survive.
	if values[5] != `"행6"` {
		t.Errorf("A5 = %s, want 행6 untouched", values[5])
	}
}

// A write aimed at a row that no longer exists has nowhere to land. Putting it
// on the row that took its place would be worse than not writing it at all.
func TestAWriteToADeletedRowIsDroppedAndReported(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "사라진 행", OwnerID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "a", IdempotencyKey: "seed",
		Cells: []CellInput{{Row: 3, Column: 1, Value: json.RawMessage(`"셋"`)}, {Row: 4, Column: 1, Value: json.RawMessage(`"넷"`)}}}); err != nil {
		t.Fatal(err)
	}
	stale, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet, ActorID: "a", BaseVersion: stale.Version, IdempotencyKey: "delete", Axis: "row", Action: "delete", Index: 3, Count: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "b", BaseVersion: stale.Version, IdempotencyKey: "late",
		Cells: []CellInput{{Row: 3, Column: 1, Value: json.RawMessage(`"사라진 행에 쓰기"`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.AppliedCells != 0 || len(result.DroppedCells) != 1 || result.DroppedCells[0].Row != 3 {
		t.Fatalf("dropped write: applied=%d dropped=%#v", result.AppliedCells, result.DroppedCells)
	}
	selected, _ := cellrange.Parse("A3")
	read, err := repository.ReadRange(ctx, sheet, selected)
	if err != nil || len(read) != 1 || string(read[0].Value) != `"넷"` {
		t.Fatalf("A3 = %#v, want 넷 unchanged", read)
	}
}

// Inserting rows moves addresses the other way.
func TestAWriteComposedBeforeARowInsertMovesDown(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "삽입", OwnerID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "a", IdempotencyKey: "seed",
		Cells: []CellInput{{Row: 2, Column: 1, Value: json.RawMessage(`"둘"`)}}}); err != nil {
		t.Fatal(err)
	}
	stale, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet, ActorID: "a", BaseVersion: stale.Version, IdempotencyKey: "insert", Axis: "row", Action: "insert", Index: 1, Count: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "b", BaseVersion: stale.Version, IdempotencyKey: "late",
		Cells: []CellInput{{Row: 2, Column: 1, Value: json.RawMessage(`"B가 고친 둘"`)}}}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A1:A5")
	read, err := repository.ReadRange(ctx, sheet, selected)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int]string, len(read))
	for _, cell := range read {
		values[cell.Row] = string(cell.Value)
	}
	if values[4] != `"B가 고친 둘"` {
		t.Errorf("A4 = %s, want B's edit where 둘 moved to", values[4])
	}
}
