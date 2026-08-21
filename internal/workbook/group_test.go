package workbook

import (
	"context"
	"testing"
)

func groupedSheet(t *testing.T) (*MemoryRepository, string, string) {
	t.Helper()
	repository := NewMemoryRepository()
	workbook, err := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "그룹", WorkspaceID: "default", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return repository, workbook.ID, workbook.Sheets[0].ID
}

func applyGroup(t *testing.T, repository *MemoryRepository, sheetID string, revision int64, action string, axis string, start, count int) SheetLayout {
	t.Helper()
	result, err := repository.ApplySheetLayout(context.Background(), SheetLayoutMutation{
		SheetID: sheetID, ActorID: "owner", IdempotencyKey: action + axis + string(rune('a'+revision)),
		ExpectedRevision: revision, Action: action, Axis: axis, Start: start, Count: count,
	})
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return result.Layout
}

// Grouping folds a run of rows away behind one control, and the outline has to
// survive being nested, collapsed and reopened.
func TestRowGroupsCollapseAndNest(t *testing.T) {
	t.Parallel()
	repository, _, sheetID := groupedSheet(t)
	layout := applyGroup(t, repository, sheetID, 1, "group", "row", 3, 8)
	if len(layout.RowGroups) != 1 || layout.RowGroups[0].Start != 3 || layout.RowGroups[0].End != 10 || layout.RowGroups[0].Depth != 0 {
		t.Fatalf("groups=%#v", layout.RowGroups)
	}
	// A group inside another is one level deeper.
	layout = applyGroup(t, repository, sheetID, layout.Revision, "group", "row", 5, 3)
	if len(layout.RowGroups) != 2 {
		t.Fatalf("groups=%#v", layout.RowGroups)
	}
	if layout.RowGroups[0].Depth != 0 || layout.RowGroups[1].Depth != 1 {
		t.Fatalf("depths=%#v", layout.RowGroups)
	}
	// Collapsing acts on the innermost group covering the range.
	layout = applyGroup(t, repository, sheetID, layout.Revision, "collapse", "row", 5, 3)
	if layout.RowGroups[0].Collapsed || !layout.RowGroups[1].Collapsed {
		t.Fatalf("collapse hit the wrong group: %#v", layout.RowGroups)
	}
	layout = applyGroup(t, repository, sheetID, layout.Revision, "expand", "row", 5, 3)
	if layout.RowGroups[1].Collapsed {
		t.Fatalf("expand left it collapsed: %#v", layout.RowGroups)
	}
	// Ungrouping peels one level, leaving the outer group in place.
	layout = applyGroup(t, repository, sheetID, layout.Revision, "ungroup", "row", 5, 3)
	if len(layout.RowGroups) != 1 || layout.RowGroups[0].Start != 3 {
		t.Fatalf("ungroup=%#v", layout.RowGroups)
	}
}

func TestGroupsRefuseWhatCannotBeDrawn(t *testing.T) {
	t.Parallel()
	repository, _, sheetID := groupedSheet(t)
	single := SheetLayoutMutation{SheetID: sheetID, ActorID: "owner", IdempotencyKey: "single", ExpectedRevision: 1, Action: "group", Axis: "row", Start: 2, Count: 1}
	if _, err := repository.ApplySheetLayout(context.Background(), single); err == nil {
		t.Fatal("a one row group should be refused")
	}
	missing := SheetLayoutMutation{SheetID: sheetID, ActorID: "owner", IdempotencyKey: "missing", ExpectedRevision: 1, Action: "collapse", Axis: "row", Start: 2, Count: 3}
	if _, err := repository.ApplySheetLayout(context.Background(), missing); err == nil {
		t.Fatal("collapsing a range with no group should be refused")
	}
}

// The outline wraps rows, so it has to move with them.
func TestGroupsFollowInsertedAndDeletedRows(t *testing.T) {
	t.Parallel()
	repository, workbookID, sheetID := groupedSheet(t)
	layout := applyGroup(t, repository, sheetID, 1, "group", "row", 5, 4)
	workbook, _ := repository.GetWorkbook(context.Background(), workbookID)
	if _, err := repository.ApplyStructure(context.Background(), StructuralMutation{
		SheetID: sheetID, ActorID: "owner", IdempotencyKey: "insert", BaseVersion: workbook.Version,
		Axis: "row", Action: "insert", Index: 2, Count: 3,
	}); err != nil {
		t.Fatal(err)
	}
	workbook, _ = repository.GetWorkbook(context.Background(), workbookID)
	layout = workbook.Sheets[0].Layout
	if len(layout.RowGroups) != 1 || layout.RowGroups[0].Start != 8 || layout.RowGroups[0].End != 11 {
		t.Fatalf("after insert=%#v", layout.RowGroups)
	}
	// Deleting every row a group covers removes the group with them.
	if _, err := repository.ApplyStructure(context.Background(), StructuralMutation{
		SheetID: sheetID, ActorID: "owner", IdempotencyKey: "delete", BaseVersion: workbook.Version,
		Axis: "row", Action: "delete", Index: 8, Count: 4,
	}); err != nil {
		t.Fatal(err)
	}
	workbook, _ = repository.GetWorkbook(context.Background(), workbookID)
	if len(workbook.Sheets[0].Layout.RowGroups) != 0 {
		t.Fatalf("after delete=%#v", workbook.Sheets[0].Layout.RowGroups)
	}
}
