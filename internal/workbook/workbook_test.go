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
	if err := repository.DeleteWorkbook(ctx, source.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetWorkbook(ctx, duplicated.ID); err != nil {
		t.Fatalf("copy was deleted with source: %v", err)
	}
}

// 목록 화면은 이제 한 페이지씩 받는다. 검색과 필터도 서버가 하므로, 메모리
// 저장소와 PostgreSQL 저장소가 같은 답을 내야 화면이 흔들리지 않는다.
func TestMemoryBrowseWorkbooksPagesSearchesAndFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	titles := []string{"분기 보고", "분기 예산", "월간 보고", "연간 계획", "주간 회의"}
	created := make([]Workbook, 0, len(titles))
	for _, title := range titles {
		book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: title, OwnerID: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, book)
	}
	if err := repository.SetWorkbookFavorite(ctx, created[0].ID, "alice", true); err != nil {
		t.Fatal(err)
	}
	principal := AccessPrincipal{UserID: "alice", Authenticated: true}
	browse := func(query WorkbookQuery) WorkbookPage {
		t.Helper()
		page, err := repository.BrowseWorkbooks(ctx, principal, query)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}

	all := browse(WorkbookQuery{})
	if all.Total != 5 || len(all.Items) != 5 || all.HasMore {
		t.Fatalf("unlimited browse = %d items of %d, more=%v", len(all.Items), all.Total, all.HasMore)
	}
	first := browse(WorkbookQuery{Limit: 2})
	if len(first.Items) != 2 || first.Total != 5 || !first.HasMore {
		t.Fatalf("first page = %d items of %d, more=%v", len(first.Items), first.Total, first.HasMore)
	}
	second := browse(WorkbookQuery{Limit: 2, Offset: 2})
	if len(second.Items) != 2 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second page repeated the first: %#v", second.Items[0].Title)
	}
	// 마지막 페이지에서는 더 없다고 해야 "더 보기" 가 사라진다.
	last := browse(WorkbookQuery{Limit: 2, Offset: 4})
	if len(last.Items) != 1 || last.HasMore {
		t.Fatalf("last page = %d items, more=%v", len(last.Items), last.HasMore)
	}
	// 페이지를 이어 붙이면 전체와 같아야 한다. 빠지거나 겹치면 안 된다.
	joined := append(append([]Workbook{}, first.Items...), append(second.Items, browse(WorkbookQuery{Limit: 2, Offset: 4}).Items...)...)
	if len(joined) != 5 {
		t.Fatalf("pages joined to %d workbooks", len(joined))
	}
	seen := map[string]bool{}
	for index, item := range joined {
		if seen[item.ID] || item.ID != all.Items[index].ID {
			t.Fatalf("joined pages differ from the whole list at %d", index)
		}
		seen[item.ID] = true
	}

	search := browse(WorkbookQuery{Search: "분기"})
	if search.Total != 2 {
		t.Fatalf("search found %d", search.Total)
	}
	if lower := browse(WorkbookQuery{Search: "보고"}); lower.Total != 2 {
		t.Fatalf("search for 보고 found %d", lower.Total)
	}
	if none := browse(WorkbookQuery{Search: "없는 이름"}); none.Total != 0 || len(none.Items) != 0 {
		t.Fatalf("search for a missing title found %d", none.Total)
	}
	// 검색과 페이지는 함께 쓰인다. 전체 수는 검색 결과의 수여야 한다.
	if paged := browse(WorkbookQuery{Search: "분기", Limit: 1}); paged.Total != 2 || len(paged.Items) != 1 || !paged.HasMore {
		t.Fatalf("searched page = %d of %d, more=%v", len(paged.Items), paged.Total, paged.HasMore)
	}
	if favorite := browse(WorkbookQuery{Filter: "favorite"}); favorite.Total != 1 || favorite.Items[0].ID != created[0].ID {
		t.Fatalf("favorite filter = %#v", favorite)
	}
	if owned := browse(WorkbookQuery{Filter: "owned"}); owned.Total != 5 {
		t.Fatalf("owned filter = %d", owned.Total)
	}
	if shared := browse(WorkbookQuery{Filter: "shared"}); shared.Total != 0 {
		t.Fatalf("shared filter = %d", shared.Total)
	}
}
