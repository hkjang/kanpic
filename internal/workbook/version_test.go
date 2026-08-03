package workbook

import (
	"context"
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestVersionRestoreRecoversWorkbookSheetStructureAndBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "원본", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	favorite := true
	if _, err := repository.UpdateWorkbook(ctx, book.ID, UpdateWorkbookInput{Favorite: &favorite}); err != nil {
		t.Fatal(err)
	}
	// A second sheet has to exist before the first one can be hidden: a workbook
	// always keeps at least one visible sheet.
	detail, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "상세", Color: "#16a34a"})
	if err != nil {
		t.Fatal(err)
	}
	firstName, firstColor, hidden := "요약", "#2563eb", true
	if _, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, UpdateSheetInput{Name: &firstName, Color: &firstColor, Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: detail.ID, ActorID: "owner", BaseVersion: 4, IdempotencyKey: "detail-value", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`42`)}}}); err != nil {
		t.Fatal(err)
	}
	target, err := repository.CreateVersion(ctx, book.ID, "기준 버전", "owner")
	if err != nil {
		t.Fatal(err)
	}

	changedTitle := "변경됨"
	favorite = false
	if _, err := repository.UpdateWorkbook(ctx, book.ID, UpdateWorkbookInput{Title: &changedTitle, Favorite: &favorite}); err != nil {
		t.Fatal(err)
	}
	changedFirstName := "임시"
	if _, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, UpdateSheetInput{Name: &changedFirstName}); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteSheet(ctx, detail.ID); err != nil {
		t.Fatal(err)
	}
	temporary, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "임시 데이터"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: temporary.ID, ActorID: "owner", BaseVersion: 9, IdempotencyKey: "temporary-value", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`99`)}}}); err != nil {
		t.Fatal(err)
	}

	restored, err := repository.RestoreVersion(ctx, target.ID, "owner")
	if err != nil || restored.ServerVersion != 11 {
		t.Fatalf("restore result: %#v, %v", restored, err)
	}
	after, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "원본" || !after.Favorite || len(after.Sheets) != 2 {
		t.Fatalf("restored workbook: %#v", after)
	}
	if after.Sheets[0].ID != book.Sheets[0].ID || after.Sheets[0].Name != "요약" || after.Sheets[0].Color != "#2563eb" || !after.Sheets[0].Hidden {
		t.Fatalf("restored first sheet: %#v", after.Sheets[0])
	}
	if after.Sheets[1].ID != detail.ID || after.Sheets[1].Name != "상세" || after.Sheets[1].Color != "#16a34a" {
		t.Fatalf("restored detail sheet: %#v", after.Sheets[1])
	}
	selected, _ := cellrange.Parse("A1")
	cells, err := repository.ReadRange(ctx, detail.ID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "42" {
		t.Fatalf("restored detail cells: %#v, %v", cells, err)
	}
	if _, err := repository.ReadRange(ctx, temporary.ID, selected); err != ErrNotFound {
		t.Fatalf("temporary sheet survived restore: %v", err)
	}

	versions, err := repository.ListVersions(ctx, book.ID)
	if err != nil || len(versions) != 2 || versions[0].Name != "복원 전 자동 백업" || versions[0].WorkbookVersion != 10 {
		t.Fatalf("automatic backup: %#v, %v", versions, err)
	}
	if _, err := repository.RestoreVersion(ctx, versions[0].ID, "owner"); err != nil {
		t.Fatalf("restore automatic backup: %v", err)
	}
	reversed, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reversed.Title != "변경됨" || reversed.Favorite || len(reversed.Sheets) != 2 || reversed.Sheets[1].ID != temporary.ID {
		t.Fatalf("restored backup workbook: %#v", reversed)
	}
}
