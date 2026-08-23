package workbook

import (
	"context"
	"errors"
	"testing"
)

func TestMemorySheetLayoutLifecycleAndStructureTransform(t *testing.T) {
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "layout", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	if sheet.Layout.Revision != 1 {
		t.Fatalf("initial layout = %#v", sheet.Layout)
	}
	apply := func(input SheetLayoutMutation) SheetLayoutResult {
		t.Helper()
		input.SheetID, input.ActorID = sheet.ID, "alice"
		result, applyErr := repository.ApplySheetLayout(context.Background(), input)
		if applyErr != nil {
			t.Fatal(applyErr)
		}
		return result
	}
	resized := apply(SheetLayoutMutation{ExpectedRevision: 1, IdempotencyKey: "row-height", Action: "resize", Axis: "row", Start: 2, Count: 2, Size: 44})
	if resized.Layout.Revision != 2 || len(resized.Layout.RowHeights) != 2 || resized.Layout.RowHeights[0] != (DimensionSize{Index: 2, Size: 44}) {
		t.Fatalf("resized layout = %#v", resized.Layout)
	}
	duplicate := apply(SheetLayoutMutation{ExpectedRevision: 1, IdempotencyKey: "row-height", Action: "resize", Axis: "row", Start: 2, Count: 2, Size: 44})
	if !duplicate.Duplicate || duplicate.OperationID != resized.OperationID || duplicate.ServerVersion != resized.ServerVersion {
		t.Fatalf("duplicate result = %#v", duplicate)
	}
	if _, err := repository.ApplySheetLayout(context.Background(), SheetLayoutMutation{SheetID: sheet.ID, ActorID: "bob", ExpectedRevision: 1, IdempotencyKey: "stale", Action: "hide", Axis: "row", Start: 2, Count: 1}); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale revision error = %v", err)
	}
	hidden := apply(SheetLayoutMutation{ExpectedRevision: 2, IdempotencyKey: "hide", Action: "hide", Axis: "row", Start: 3, Count: 3})
	frozen := apply(SheetLayoutMutation{ExpectedRevision: 3, IdempotencyKey: "freeze", Action: "freeze", FrozenRows: 4, FrozenColumns: 2})
	if len(hidden.Layout.HiddenRows) != 1 || frozen.Layout.FrozenRows != 4 || frozen.Layout.FrozenColumns != 2 {
		t.Fatalf("hidden/frozen layout = %#v", frozen.Layout)
	}
	structured, err := repository.ApplyStructure(context.Background(), StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: frozen.ServerVersion, IdempotencyKey: "insert-layout", Axis: "row", Action: "insert", Index: 2, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.GetWorkbook(context.Background(), book.ID)
	if err != nil {
		t.Fatal(err)
	}
	layout := updated.Sheets[0].Layout
	if layout.Revision != 5 || layout.FrozenRows != 6 || len(layout.RowHeights) != 2 || layout.RowHeights[0].Index != 4 || layout.HiddenRows[0] != (DimensionRange{Start: 5, End: 7}) {
		t.Fatalf("transformed layout = %#v", layout)
	}
	if _, err := repository.RestoreVersion(context.Background(), structured.BackupVersionID, "alice"); err != nil {
		t.Fatal(err)
	}
	restored, _ := repository.GetWorkbook(context.Background(), book.ID)
	restoredLayout := restored.Sheets[0].Layout
	if restoredLayout.Revision != 4 || restoredLayout.FrozenRows != 4 || restoredLayout.HiddenRows[0] != (DimensionRange{Start: 3, End: 5}) {
		t.Fatalf("restored layout = %#v", restoredLayout)
	}
}

func TestNormalizeSheetLayoutMutationLimits(t *testing.T) {
	tests := []SheetLayoutMutation{
		{ExpectedRevision: 1, IdempotencyKey: "x", Action: "resize", Axis: "row", Start: 1, Count: 1, Size: 15},
		{ExpectedRevision: 1, IdempotencyKey: "x", Action: "resize", Axis: "column", Start: 1, Count: 1, Size: 601},
		{ExpectedRevision: 1, IdempotencyKey: "x", Action: "hide", Axis: "row", Start: 1, Count: MaxLayoutSpan + 1},
		{ExpectedRevision: 0, IdempotencyKey: "x", Action: "freeze"},
	}
	for _, input := range tests {
		if _, err := normalizeSheetLayoutMutation(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("input %#v error = %v", input, err)
		}
	}
}

func TestTransformDimensionRangesForDeletion(t *testing.T) {
	input := []DimensionRange{{Start: 10, End: 20}}
	result, changed := transformDimensionRanges(input, StructuralMutation{Action: "delete", Index: 5, Count: 8})
	if !changed || len(result) != 1 || result[0] != (DimensionRange{Start: 5, End: 12}) {
		t.Fatalf("result = %#v, changed = %v", result, changed)
	}
}

// A slicer is stored with the sheet layout, so it has to survive the same
// column edits the rest of the layout does: follow a moved column and leave
// with a deleted one.
func TestMemorySlicerLifecycleAndStructureTransform(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "slicer", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	view, err := repository.CreateFilterView(ctx, sheet.ID, "alice", CreateFilterViewInput{IdempotencyKey: "slicer-filter", Name: "지역", Range: "A1:B4", HeaderRows: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	added, err := repository.ApplySheetLayout(ctx, SheetLayoutMutation{SheetID: sheet.ID, ActorID: "alice", IdempotencyKey: "slicer-add", ExpectedRevision: 1, Action: "slicer_add", Slicer: &Slicer{FilterViewID: view.ID, Column: 3, Title: "지역", Position: ChartPosition{X: 40, Y: 40, Width: 220, Height: 260}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(added.Layout.Slicers) != 1 || added.Layout.Slicers[0].ID == "" || added.Layout.Slicers[0].Column != 3 {
		t.Fatalf("added slicers = %#v", added.Layout.Slicers)
	}
	id := added.Layout.Slicers[0].ID
	// A slicer that is too small to show a value list is refused rather than
	// stored as an unusable card.
	if _, err := repository.ApplySheetLayout(ctx, SheetLayoutMutation{SheetID: sheet.ID, ActorID: "alice", IdempotencyKey: "slicer-tiny", ExpectedRevision: added.Layout.Revision, Action: "slicer_update", Slicer: &Slicer{ID: id, FilterViewID: view.ID, Column: 3, Position: ChartPosition{X: 0, Y: 0, Width: 20, Height: 20}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tiny slicer error = %v", err)
	}
	moved, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: added.ServerVersion, IdempotencyKey: "slicer-move", Axis: "column", Action: "move", Index: 3, Count: 1, Destination: 1})
	if err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Sheets[0].Layout.Slicers; len(got) != 1 || got[0].Column != 1 {
		t.Fatalf("slicers after move = %#v", got)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: moved.ServerVersion, IdempotencyKey: "slicer-delete-column", Axis: "column", Action: "delete", Index: 1, Count: 1}); err != nil {
		t.Fatal(err)
	}
	current, _ = repository.GetWorkbook(ctx, book.ID)
	if got := current.Sheets[0].Layout.Slicers; len(got) != 0 {
		t.Fatalf("slicers after deleting their column = %#v", got)
	}
}

// 인쇄 영역은 "이 범위만 종이에 낸다" 는 시트의 성질이다. 정해 두지 않으면
// 내용이 있는 곳 전체가 나간다 — 사람이 따로 말하기 전까지는 그쪽이
// 기대하는 바다.
//
// 엑셀은 이것을 _xlnm.Print_Area 라는 이름으로 담는다. 예전에는 가져올 때
// "인쇄 영역 1개를 빼고 가져왔습니다" 라고만 알리고 버렸다.
func TestPrintAreaIsKeptOnTheSheet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "print", OwnerID: "alice"})
	sheet := book.Sheets[0]

	// 사람이 적은 모양을 그대로 두지 않고 A1:D20 꼴로 다듬는다.
	applied, err := repository.ApplySheetLayout(ctx, SheetLayoutMutation{
		SheetID: sheet.ID, ActorID: "alice", IdempotencyKey: "pa-1",
		ExpectedRevision: sheet.Layout.Revision, Action: "print_area_set", Range: "b2:d10",
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if applied.Layout.PrintArea != "B2:D10" {
		t.Fatalf("print area = %q, want B2:D10", applied.Layout.PrintArea)
	}

	cleared, err := repository.ApplySheetLayout(ctx, SheetLayoutMutation{
		SheetID: sheet.ID, ActorID: "alice", IdempotencyKey: "pa-2",
		ExpectedRevision: applied.Layout.Revision, Action: "print_area_clear",
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.Layout.PrintArea != "" {
		t.Fatalf("print area after clear = %q, want empty", cleared.Layout.PrintArea)
	}

	// 범위가 아닌 값은 받지 않는다. 받아 두면 인쇄할 때가 되어서야 드러난다.
	if _, err := repository.ApplySheetLayout(ctx, SheetLayoutMutation{
		SheetID: sheet.ID, ActorID: "alice", IdempotencyKey: "pa-3",
		ExpectedRevision: cleared.Layout.Revision, Action: "print_area_set", Range: "말이 안 되는 값",
	}); err == nil {
		t.Error("범위가 아닌 인쇄 영역이 받아들여졌다")
	}
}
