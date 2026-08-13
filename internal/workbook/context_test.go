package workbook

import (
	"context"
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestBuildAgentContextPrioritizesSelectionAndProfilesColumns(t *testing.T) {
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "2026 매출", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	_, err = repository.ApplyCells(context.Background(), CellMutation{
		SheetID: sheet.ID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "seed-context",
		Cells: []CellInput{
			{Row: 1, Column: 1, Value: json.RawMessage(`"날짜"`)},
			{Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
			{Row: 2, Column: 1, Value: json.RawMessage(`"2026-08-01"`)},
			{Row: 2, Column: 2, Value: json.RawMessage(`1200`)},
			{Row: 3, Column: 1, Value: json.RawMessage(`"2026-08-02"`)},
			{Row: 3, Column: 2, Formula: "=600*3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	contextView, err := BuildAgentContext(context.Background(), repository, book.ID, sheet.ID, "A1:B3")
	if err != nil {
		t.Fatal(err)
	}
	if contextView.WorkbookTitle != "2026 매출" || contextView.ActiveSheet.ID != sheet.ID || contextView.Selection != "A1:B3" {
		t.Fatalf("unexpected workbook context: %#v", contextView)
	}
	if contextView.SelectedRange.CellCount != 6 || contextView.SelectedRange.FormulaCount != 1 || contextView.SelectedRange.BlankCount != 0 {
		t.Fatalf("unexpected range profile: %#v", contextView.SelectedRange)
	}
	if got := contextView.SemanticMap[0]; got.Header != "날짜" || got.SemanticType != "date" || got.DataType != "date" {
		t.Fatalf("date column profile = %#v", got)
	}
	if got := contextView.SemanticMap[1]; got.Header != "매출" || got.SemanticType != "revenue" || got.FormulaCount != 1 {
		t.Fatalf("revenue column profile = %#v", got)
	}
	if len(contextView.SuggestedPrompts) < 3 {
		t.Fatalf("suggestions = %#v", contextView.SuggestedPrompts)
	}
}

func TestBuildAgentContextRejectsForeignSheet(t *testing.T) {
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "one", OwnerID: "owner"})
	other, _ := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "two", OwnerID: "owner"})
	_, err := BuildAgentContext(context.Background(), repository, book.ID, other.Sheets[0].ID, "A1")
	if err != ErrNotFound {
		t.Fatalf("foreign sheet error = %v", err)
	}
}

func TestNormalizedAgentRange(t *testing.T) {
	selected, err := cellrange.Parse("b2:a1")
	if err == nil {
		// cellrange intentionally rejects reversed ranges; keep this assertion
		// here to document that the context receives canonical editor ranges.
		t.Fatalf("reversed range unexpectedly parsed as %s", normalizedRange(selected))
	}
}
