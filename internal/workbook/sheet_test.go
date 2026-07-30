package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestMemorySheetLifecyclePreservesCellsAndPositionInvariant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "sheet lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "Data", Color: "#2563eb"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "Tail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: data.ID, ActorID: "owner", BaseVersion: 3, IdempotencyKey: "seed-sheet", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`5`)},
		{Row: 2, Column: 1, Formula: "=A1*2", Style: json.RawMessage(`{"background":"#fef3c7"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	duplicated, err := repository.DuplicateSheet(ctx, data.ID, DuplicateSheetInput{})
	if err != nil {
		t.Fatal(err)
	}
	if duplicated.Name != "Data 복사본" || duplicated.Position != 2 || duplicated.Color != data.Color {
		t.Fatalf("duplicated sheet: %#v", duplicated)
	}
	selected, _ := cellrange.Parse("A1:A2")
	cells, err := repository.ReadRange(ctx, duplicated.ID, selected)
	if err != nil || len(cells) != 2 || cells[0].SheetID != duplicated.ID || string(cells[0].Value) != "5" || cells[1].Formula != "=A1*2" || string(cells[1].Value) != "10" || string(cells[1].Style) != `{"background":"#fef3c7"}` {
		t.Fatalf("duplicated cells: %#v, %v", cells, err)
	}
	position := 0
	moved, err := repository.UpdateSheet(ctx, duplicated.ID, UpdateSheetInput{Position: &position})
	if err != nil || moved.Position != 0 {
		t.Fatalf("move: %#v, %v", moved, err)
	}
	afterMove, _ := repository.GetWorkbook(ctx, book.ID)
	assertSheetPositions(t, afterMove, []string{"Data 복사본", "Sheet1", "Data", "Tail"}, 6)
	if _, err := repository.UpdateSheet(ctx, duplicated.ID, UpdateSheetInput{}); err != nil {
		t.Fatal(err)
	}
	afterNoop, _ := repository.GetWorkbook(ctx, book.ID)
	if afterNoop.Version != afterMove.Version {
		t.Fatalf("no-op changed version from %d to %d", afterMove.Version, afterNoop.Version)
	}
	duplicateName := "data"
	if _, err := repository.UpdateSheet(ctx, duplicated.ID, UpdateSheetInput{Name: &duplicateName}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("case-insensitive duplicate name: %v", err)
	}
	if _, err := repository.DuplicateSheet(ctx, data.ID, DuplicateSheetInput{Name: "TAIL"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("explicit duplicate name: %v", err)
	}
	if err := repository.DeleteSheet(ctx, book.Sheets[0].ID); err != nil {
		t.Fatal(err)
	}
	afterDelete, _ := repository.GetWorkbook(ctx, book.ID)
	assertSheetPositions(t, afterDelete, []string{"Data 복사본", "Data", "Tail"}, 7)
}

func assertSheetPositions(t *testing.T, book Workbook, names []string, version int64) {
	t.Helper()
	if book.Version != version || len(book.Sheets) != len(names) {
		t.Fatalf("workbook: %#v", book)
	}
	for index, name := range names {
		if book.Sheets[index].Name != name || book.Sheets[index].Position != index {
			t.Fatalf("sheet %d: %#v", index, book.Sheets[index])
		}
	}
}
