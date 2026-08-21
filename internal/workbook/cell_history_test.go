package workbook

import (
	"context"
	"encoding/json"
	"testing"
)

// A cell's history has to name the person, keep the value it replaced, and skip
// the operations that swept over the cell without changing it.
func TestMemoryCellHistoryReportsEditsNewestFirst(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "이력", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	first, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "h1", Cells: []CellInput{{Row: 2, Column: 3, Value: json.RawMessage(`100`)}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "bob", BaseVersion: first.ServerVersion, IdempotencyKey: "h2", Cells: []CellInput{{Row: 2, Column: 3, Formula: "=50*4"}}})
	if err != nil {
		t.Fatal(err)
	}
	// The same value written again is not an edit anybody is looking for.
	third, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "bob", BaseVersion: second.ServerVersion, IdempotencyKey: "h3", Cells: []CellInput{{Row: 2, Column: 3, Formula: "=50*4"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: third.ServerVersion, IdempotencyKey: "h4", Cells: []CellInput{{Row: 9, Column: 9, Value: json.RawMessage(`1`)}}}); err != nil {
		t.Fatal(err)
	}
	history, err := repository.CellHistory(ctx, sheet, 2, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if history.Address != "C2" || len(history.Items) != 2 {
		t.Fatalf("history = %#v", history)
	}
	newest := history.Items[0]
	if newest.ActorID != "bob" || newest.After.Formula != "=50*4" || string(newest.Before.Value) != "100" {
		t.Fatalf("newest entry = %#v", newest)
	}
	oldest := history.Items[1]
	if oldest.ActorID != "alice" || !oldest.Before.Empty || string(oldest.After.Value) != "100" {
		t.Fatalf("oldest entry = %#v", oldest)
	}
	if history.Items[0].ServerVersion <= history.Items[1].ServerVersion {
		t.Fatal("history must be newest first")
	}
}
