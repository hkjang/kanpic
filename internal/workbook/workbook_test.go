package workbook

import (
	"context"
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestMemoryWorkbookDuplicateIsIndependentAndPreservesData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	source, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "원본", WorkspaceID: "finance", OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := repository.CreateSheet(ctx, source.ID, CreateSheetInput{Name: "상세", Color: "#2563eb"})
	if err != nil {
		t.Fatal(err)
	}
	hidden := true
	if _, err := repository.UpdateSheet(ctx, detail.ID, UpdateSheetInput{Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: detail.ID, ActorID: "owner-a", BaseVersion: 3, IdempotencyKey: "workbook-copy-seed", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`7`), Style: json.RawMessage(`{"bold":true}`)}, {Row: 2, Column: 1, Formula: "=A1*2"}}}); err != nil {
		t.Fatal(err)
	}
	duplicated, err := repository.DuplicateWorkbook(ctx, source.ID, DuplicateWorkbookInput{OwnerID: "owner-b"})
	if err != nil {
		t.Fatal(err)
	}
	if duplicated.ID == source.ID || duplicated.Title != "원본 복사본" || duplicated.WorkspaceID != "finance" || duplicated.OwnerID != "owner-b" || duplicated.Version != 1 || len(duplicated.Sheets) != 2 {
		t.Fatalf("duplicated workbook: %#v", duplicated)
	}
	if duplicated.Sheets[1].ID == detail.ID || duplicated.Sheets[1].Name != "상세" || duplicated.Sheets[1].Color != "#2563eb" || !duplicated.Sheets[1].Hidden {
		t.Fatalf("duplicated sheet: %#v", duplicated.Sheets[1])
	}
	selected, _ := cellrange.Parse("A1:A2")
	cells, err := repository.ReadRange(ctx, duplicated.Sheets[1].ID, selected)
	var copiedStyle map[string]any
	if len(cells) > 0 {
		_ = json.Unmarshal(cells[0].Style, &copiedStyle)
	}
	if err != nil || len(cells) != 2 || cells[0].SheetID != duplicated.Sheets[1].ID || string(cells[0].Value) != "7" || copiedStyle["bold"] != true || cells[1].Formula != "=A1*2" || string(cells[1].Value) != "14" {
		t.Fatalf("duplicated cells: %#v, %v", cells, err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: detail.ID, ActorID: "owner-a", BaseVersion: 4, IdempotencyKey: "change-original", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`9`)}}}); err != nil {
		t.Fatal(err)
	}
	cells, _ = repository.ReadRange(ctx, duplicated.Sheets[1].ID, selected)
	if string(cells[0].Value) != "7" || string(cells[1].Value) != "14" {
		t.Fatalf("copy changed with source: %#v", cells)
	}
	if err := repository.DeleteWorkbook(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetWorkbook(ctx, duplicated.ID); err != nil {
		t.Fatalf("copy was deleted with source: %v", err)
	}
}
