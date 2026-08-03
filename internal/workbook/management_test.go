package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestFavoritesArePerUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "즐겨찾기", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PutWorkbookShare(ctx, book.ID, ShareInput{PrincipalType: PrincipalUser, PrincipalID: "viewer", Role: RoleViewer}); err != nil {
		t.Fatal(err)
	}
	// A viewer may star a shared workbook without touching anybody else's list.
	if err := repository.SetWorkbookFavorite(ctx, book.ID, "viewer", true); err != nil {
		t.Fatal(err)
	}
	viewerFavorites, err := repository.WorkbookFavorites(ctx, "VIEWER")
	if err != nil || !viewerFavorites[book.ID] {
		t.Fatalf("viewer favorites = %#v, %v", viewerFavorites, err)
	}
	ownerFavorites, err := repository.WorkbookFavorites(ctx, "owner")
	if err != nil || ownerFavorites[book.ID] {
		t.Fatalf("owner favorites = %#v, %v", ownerFavorites, err)
	}
	if err := repository.SetWorkbookFavorite(ctx, book.ID, "viewer", false); err != nil {
		t.Fatal(err)
	}
	if cleared, _ := repository.WorkbookFavorites(ctx, "viewer"); cleared[book.ID] {
		t.Fatal("removing a favorite must clear it")
	}
	if err := repository.SetWorkbookFavorite(ctx, book.ID, " ", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("anonymous favorite error = %v", err)
	}
}

func TestWorkbookTrashRestoreAndPurge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "휴지통", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "owner", BaseVersion: 1, IdempotencyKey: "trash-seed", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`"보존"`)}}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteWorkbook(ctx, book.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetWorkbook(ctx, book.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a trashed workbook must not be readable: %v", err)
	}
	owner := AccessPrincipal{UserID: "owner", Authenticated: true}
	trash, err := repository.ListDeletedWorkbooks(ctx, "", owner)
	if err != nil || len(trash) != 1 || trash[0].DeletedBy != "owner" || trash[0].DeletedAt == nil {
		t.Fatalf("trash = %#v, %v", trash, err)
	}
	stranger, err := repository.ListDeletedWorkbooks(ctx, "", AccessPrincipal{UserID: "stranger", Authenticated: true})
	if err != nil || len(stranger) != 0 {
		t.Fatalf("a stranger must not see the trash: %#v, %v", stranger, err)
	}
	restored, err := repository.RestoreWorkbook(ctx, book.ID, "owner")
	if err != nil || restored.ID != book.ID || len(restored.Sheets) != 1 {
		t.Fatalf("restore = %#v, %v", restored, err)
	}
	area, _ := cellrange.Parse("A1")
	cells, err := repository.ReadRange(ctx, sheetID, area)
	if err != nil || len(cells) != 1 {
		t.Fatalf("restored cells = %#v, %v", cells, err)
	}
	if _, err := repository.RestoreWorkbook(ctx, book.ID, "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restoring an active workbook error = %v", err)
	}
	if err := repository.PurgeWorkbook(ctx, book.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purging an active workbook error = %v", err)
	}
	if err := repository.DeleteWorkbook(ctx, book.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := repository.PurgeWorkbook(ctx, book.ID); err != nil {
		t.Fatal(err)
	}
	if remaining, _ := repository.ListDeletedWorkbooks(ctx, "", owner); len(remaining) != 0 {
		t.Fatalf("purge left %d workbooks", len(remaining))
	}
}

func TestSheetVisibilityKeepsOneSheetVisible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "숨김", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	hidden := true
	if _, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, UpdateSheetInput{Hidden: &hidden}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hiding the only sheet error = %v", err)
	}
	second, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "보조"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, UpdateSheetInput{Hidden: &hidden})
	if err != nil || !updated.Hidden {
		t.Fatalf("hide = %#v, %v", updated, err)
	}
	if _, err := repository.UpdateSheet(ctx, second.ID, UpdateSheetInput{Hidden: &hidden}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hiding the last visible sheet error = %v", err)
	}
	visible := false
	if shown, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, UpdateSheetInput{Hidden: &visible}); err != nil || shown.Hidden {
		t.Fatalf("unhide = %#v, %v", shown, err)
	}
}

func TestSheetStatsAndCopyToAnotherWorkbook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	source, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "원본", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "대상", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := source.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "owner", BaseVersion: 1, IdempotencyKey: "stats", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`10`)},
		{Row: 4, Column: 3, Value: json.RawMessage(`20`)},
		{Row: 5, Column: 3, Formula: "=SUM(C4:C4)"},
	}}); err != nil {
		t.Fatal(err)
	}
	stats, err := repository.SheetStats(ctx, source.ID)
	if err != nil || len(stats) != 1 {
		t.Fatalf("stats = %#v, %v", stats, err)
	}
	if stats[0].NonEmptyCells != 3 || stats[0].FormulaCells != 1 || stats[0].MaxRow != 5 || stats[0].MaxColumn != 3 || stats[0].UpdatedAt == nil {
		t.Fatalf("sheet stats = %#v", stats[0])
	}

	copied, err := repository.CopySheetToWorkbook(ctx, sheetID, CopySheetInput{TargetWorkbookID: target.ID, ActorID: "owner"})
	if err != nil || copied.WorkbookID != target.ID || copied.Name != "Sheet1 (2)" {
		t.Fatalf("copy across workbooks = %#v, %v", copied, err)
	}
	copiedArea, _ := cellrange.Parse("A1:C5")
	copiedCells, err := repository.ReadRange(ctx, copied.ID, copiedArea)
	if err != nil || len(copiedCells) != 3 {
		t.Fatalf("copied cells = %#v, %v", copiedCells, err)
	}
	for _, cell := range copiedCells {
		if cell.SheetID != copied.ID {
			t.Fatalf("copied cell keeps the source sheet: %#v", cell)
		}
	}
	sameWorkbook, err := repository.CopySheetToWorkbook(ctx, sheetID, CopySheetInput{TargetWorkbookID: source.ID, ActorID: "owner"})
	if err != nil || sameWorkbook.Name != "Sheet1 복사본" {
		t.Fatalf("copy inside the workbook = %#v, %v", sameWorkbook, err)
	}
	if _, err := repository.CopySheetToWorkbook(ctx, sheetID, CopySheetInput{TargetWorkbookID: ""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing target error = %v", err)
	}
	if _, err := repository.CopySheetToWorkbook(ctx, sheetID, CopySheetInput{TargetWorkbookID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown target error = %v", err)
	}
}
