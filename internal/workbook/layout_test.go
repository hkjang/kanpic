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
