package workbook

import (
	"context"
	"encoding/json"
	"testing"
)

// Deleting a row moves every address below it. Nine different things name a
// range — filters, charts, pivots, named ranges, validations, conditional
// formats, protections, comments, notes — and each is stored separately, so
// each has its own chance to be forgotten. They were all correct when this
// test was written; it exists so they stay that way.
func TestEverythingThatNamesARangeFollowsARowDelete(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "추종", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	seed := make([]CellInput, 0, 20)
	for row := 1; row <= 10; row++ {
		seed = append(seed, CellInput{Row: row, Column: 1, Value: json.RawMessage(`"이름"`)}, CellInput{Row: row, Column: 2, Value: json.RawMessage(`10`)})
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "owner", IdempotencyKey: "seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "owner", IdempotencyKey: "note", NotePatch: stringPointer("다섯째 줄"), Cells: []CellInput{{Row: 5, Column: 1}}}); err != nil {
		t.Fatal(err)
	}
	view, err := repository.CreateFilterView(ctx, sheet, "owner", CreateFilterViewInput{IdempotencyKey: "view", Name: "전체", Range: "A1:B10", HeaderRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateChart(ctx, book.ID, "owner", CreateChartInput{IdempotencyKey: "chart", SheetID: sheet, SourceSheetID: sheet, Type: "bar", Title: "차트", SourceRange: "A1:B10"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreatePivot(ctx, book.ID, "owner", CreatePivotInput{IdempotencyKey: "pivot", SheetID: sheet, SourceSheetID: sheet, Name: "요약", SourceRange: "A1:B10",
		Rows: []PivotDimension{{Column: 1}}, Values: []PivotValueField{{Column: 2, Aggregation: "sum"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: "name", Name: "구간", SheetID: sheet, Range: "A5:A8"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateDataValidation(ctx, sheet, "owner", CreateDataValidationInput{IdempotencyKey: "rule", Range: "B5:B8", RuleType: "number", Operator: "greater_than", Value: json.RawMessage(`0`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateConditionalFormat(ctx, sheet, "owner", CreateConditionalFormatInput{IdempotencyKey: "format", Range: "B1:B10", RuleType: "value", Operator: "greater_than", Value: json.RawMessage(`5`), Style: json.RawMessage(`{"background":"#ffeeee"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateProtectedRange(ctx, sheet, "owner", CreateProtectedRangeInput{IdempotencyKey: "protect", Range: "A5:A8", Description: "보호"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCommentThread(ctx, book.ID, "owner", CreateCommentThreadInput{IdempotencyKey: "comment", SheetID: sheet, Range: "A5", Content: "다섯째 줄 확인"}); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet, ActorID: "owner", BaseVersion: current.Version, IdempotencyKey: "delete", Axis: "row", Action: "delete", Index: 3, Count: 1}); err != nil {
		t.Fatal(err)
	}

	views, err := repository.ListFilterViews(ctx, sheet, "owner")
	if err != nil || len(views) != 1 || views[0].Range != "A1:B9" {
		t.Errorf("filter view = %#v, %v", views, err)
	}
	charts, err := repository.ListCharts(ctx, book.ID, "")
	if err != nil || len(charts) != 1 || charts[0].SourceRange != "A1:B9" {
		t.Errorf("chart source = %#v, %v", charts, err)
	}
	pivots, err := repository.ListPivots(ctx, book.ID, "")
	if err != nil || len(pivots) != 1 || pivots[0].SourceRange != "A1:B9" {
		t.Errorf("pivot source = %#v, %v", pivots, err)
	}
	names, err := repository.ListNamedRanges(ctx, book.ID)
	if err != nil || len(names) != 1 || names[0].Range != "A4:A7" {
		t.Errorf("named range = %#v, %v", names, err)
	}
	rules, err := repository.ListDataValidations(ctx, sheet)
	if err != nil || len(rules) != 1 || rules[0].Range != "B4:B7" {
		t.Errorf("validation = %#v, %v", rules, err)
	}
	formats, err := repository.ListConditionalFormats(ctx, sheet)
	if err != nil || len(formats) != 1 || formats[0].Range != "B1:B9" {
		t.Errorf("conditional format = %#v, %v", formats, err)
	}
	protections, err := repository.ListProtectedRanges(ctx, sheet)
	if err != nil || len(protections) != 1 || protections[0].Range != "A4:A7" {
		t.Errorf("protected range = %#v, %v", protections, err)
	}
	threads, err := repository.ListCommentThreads(ctx, book.ID, "", false)
	if err != nil || len(threads) != 1 || threads[0].Range != "A4" {
		t.Errorf("comment thread = %#v, %v", threads, err)
	}
	// The note belongs to the cell and rides along with it.
	moved, err := repository.ReadAllCells(ctx, sheet)
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, cell := range moved {
		if cell.Note != "" {
			found = cell.Note
			if cell.Row != 4 {
				t.Errorf("note landed on row %d, want 4", cell.Row)
			}
		}
	}
	if found != "다섯째 줄" {
		t.Errorf("note = %q", found)
	}
	_ = view
}

// A slicer points at one column by number, which nothing else in the layout
// does. Deleting a column to its left has to renumber it or the control ends
// up filtering a different column than its title says.
func TestASlicerFollowsAColumnDelete(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "슬라이서", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	seed := make([]CellInput, 0, 25)
	for row := 1; row <= 5; row++ {
		for column := 1; column <= 5; column++ {
			seed = append(seed, CellInput{Row: row, Column: column, Value: json.RawMessage(`"값"`)})
		}
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "owner", IdempotencyKey: "seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	view, err := repository.CreateFilterView(ctx, sheet, "owner", CreateFilterViewInput{IdempotencyKey: "view", Name: "전체", Range: "A1:E5", HeaderRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	layout, err := repository.ApplySheetLayout(ctx, SheetLayoutMutation{SheetID: sheet, ActorID: "owner", IdempotencyKey: "slicer", ExpectedRevision: 1, Action: "slicer_add",
		Slicer: &Slicer{FilterViewID: view.ID, Column: 4, Title: "D열", Position: ChartPosition{X: 5, Y: 6, Width: 200, Height: 200}}})
	if err != nil || len(layout.Layout.Slicers) != 1 {
		t.Fatalf("slicer: %#v, %v", layout.Layout.Slicers, err)
	}
	current, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet, ActorID: "owner", BaseVersion: current.Version, IdempotencyKey: "delete", Axis: "column", Action: "delete", Index: 2, Count: 1}); err != nil {
		t.Fatal(err)
	}
	after, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	slicers := after.Sheets[0].Layout.Slicers
	if len(slicers) != 1 || slicers[0].Column != 3 {
		t.Fatalf("slicer column = %#v, want 3", slicers)
	}
}

// Moving a band is the least used of the three and the only one that is a
// permutation rather than a shift, so it gets its own case.
func TestDefinitionsFollowARowMove(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "이동", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	seed := make([]CellInput, 0, 16)
	for row := 1; row <= 8; row++ {
		seed = append(seed, CellInput{Row: row, Column: 1, Value: json.RawMessage(`"값"`)}, CellInput{Row: row, Column: 2, Value: json.RawMessage(`"값"`)})
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "owner", IdempotencyKey: "seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: "name", Name: "둘째줄", SheetID: sheet, Range: "A2:B2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateProtectedRange(ctx, sheet, "owner", CreateProtectedRangeInput{IdempotencyKey: "protect", Range: "A2:B2", Description: "보호"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCommentThread(ctx, book.ID, "owner", CreateCommentThreadInput{IdempotencyKey: "comment", SheetID: sheet, Range: "A2", Content: "둘째 줄"}); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Row 2 moves down to sit before row 6, which leaves it at row 5.
	result, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet, ActorID: "owner", BaseVersion: current.Version, IdempotencyKey: "move", Axis: "row", Action: "move", Index: 2, Count: 1, Destination: 6})
	if err != nil {
		t.Fatal(err)
	}
	// The destination has to be recorded: without it a late write cannot be
	// replayed onto the moved sheet, because a move is not a shift.
	if result.StructuralDestination != 6 {
		t.Errorf("recorded destination = %d", result.StructuralDestination)
	}
	names, err := repository.ListNamedRanges(ctx, book.ID)
	if err != nil || len(names) != 1 || names[0].Range != "A5:B5" {
		t.Errorf("named range = %#v, %v", names, err)
	}
	protections, err := repository.ListProtectedRanges(ctx, sheet)
	if err != nil || len(protections) != 1 || protections[0].Range != "A5:B5" {
		t.Errorf("protected range = %#v, %v", protections, err)
	}
	threads, err := repository.ListCommentThreads(ctx, book.ID, "", false)
	if err != nil || len(threads) != 1 || threads[0].Range != "A5" {
		t.Errorf("comment thread = %#v, %v", threads, err)
	}
}

func stringPointer(value string) *string { return &value }
