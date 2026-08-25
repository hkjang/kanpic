package workbook

import (
	"context"
	"encoding/json"
	"testing"
)

// 워크북에 딸린 것은 저마다 복제와 되돌리기를 따라가거나 따라가지 않는다.
// 그 표가 여기 있다.
//
// 이 표를 세 판에 걸쳐 다시 유도했다. 종류를 하나 더할 때마다 구조 변경은
// 챙기면서 복제와 되돌리기를 빠뜨렸고, 그때마다 사람이 먼저 부딪히기 전에
// 겨우 찾았다. 값은 남는데 그 값을 만든 정의가 없거나, 막으라고 걸어 둔
// 보호가 조용히 사라지는 식이었다.
//
// 새 종류를 더한다면 여기에 한 줄을 더하라. 따라가지 않는 쪽을 고른다면
// 까닭을 함께 적으라 — 잊어서 빠진 것과 뜻이 있어 뺀 것은 코드만 봐서는
// 갈리지 않는다.
func TestWhatTravelsWithADuplicateAndARestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "표", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: value("지역")}, {Row: 1, Column: 2, Value: value("금액")},
		{Row: 2, Column: 1, Value: value("서울")}, {Row: 2, Column: 2, Value: value(100)},
		{Row: 3, Column: 1, Value: value("부산")}, {Row: 3, Column: 2, Value: value(200)},
	}}); err != nil {
		t.Fatal(err)
	}
	// 한 종류씩 만들어 둔다. 지우는 함수는 되돌리기를 보기 위한 것이다.
	kinds := []struct {
		name     string
		reason   string
		travels  bool
		restores bool
		create   func() error
		remove   func() error
		countIn  func(workbookID, sheetID string) int
	}{
		{
			name: "이름 범위", travels: true, restores: true,
			reason: "시트가 자기를 설명하는 방법이다. 사본에도 그대로 있어야 =SUM(단가) 가 맞는다.",
			create: func() error {
				_, err := repository.CreateNamedRange(ctx, book.ID, "alice", CreateNamedRangeInput{IdempotencyKey: "nr", SheetID: sheetID, Name: "금액열", Range: "B2:B3"})
				return err
			},
			remove: func() error {
				items, _ := repository.ListNamedRanges(ctx, book.ID)
				return repository.DeleteNamedRange(ctx, items[0].ID, "alice", nil)
			},
			countIn: func(workbookID, _ string) int {
				items, _ := repository.ListNamedRanges(ctx, workbookID)
				return len(items)
			},
		},
		{
			name: "이름 있는 수식", travels: true, restores: true,
			reason: "두고 가면 =마진율(A1,B1) 이 #NAME? 이 된다. 칸에는 옛 값이 남아 더 헷갈린다.",
			create: func() error {
				_, err := repository.CreateNamedFunction(ctx, book.ID, "alice", CreateNamedFunctionInput{IdempotencyKey: "nf", Name: "두배", Parameters: []string{"x"}, Body: "x*2"})
				return err
			},
			remove: func() error {
				items, _ := repository.ListNamedFunctions(ctx, book.ID)
				return repository.DeleteNamedFunction(ctx, items[0].ID, "alice", nil)
			},
			countIn: func(workbookID, _ string) int {
				items, _ := repository.ListNamedFunctions(ctx, workbookID)
				return len(items)
			},
		},
		{
			name: "표", travels: true, restores: true,
			reason: "이름 있는 수식과 같다. =SUM(매출표[금액]) 이 가리킬 곳을 잃는다.",
			create: func() error {
				_, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{IdempotencyKey: "st", SheetID: sheetID, Name: "매출표", Range: "A1:B3"})
				return err
			},
			remove: func() error {
				items, _ := repository.ListSheetTables(ctx, book.ID)
				return repository.DeleteSheetTable(ctx, items[0].ID, "alice", nil)
			},
			countIn: func(workbookID, _ string) int {
				items, _ := repository.ListSheetTables(ctx, workbookID)
				return len(items)
			},
		},
		{
			name: "보호 범위", travels: true, restores: true,
			reason: "잃으면 아무 표시도 나지 않는다. 지켜지고 있다고 믿는 칸을 아무나 고치게 된다.",
			create: func() error {
				_, err := repository.CreateProtectedRange(ctx, sheetID, "alice", CreateProtectedRangeInput{IdempotencyKey: "pr", SheetID: sheetID, Range: "A1:B1", Description: "머리글", Editors: []string{"alice"}})
				return err
			},
			remove: func() error {
				items, _ := repository.ListProtectedRanges(ctx, sheetID)
				return repository.DeleteProtectedRange(ctx, items[0].ID)
			},
			countIn: func(_, sheet string) int {
				items, _ := repository.ListProtectedRanges(ctx, sheet)
				return len(items)
			},
		},
		{
			name: "필터 보기", travels: true, restores: true,
			reason: "시트를 어떻게 보기로 했는지도 그 시트의 일부다.",
			create: func() error {
				_, err := repository.CreateFilterView(ctx, sheetID, "alice", CreateFilterViewInput{IdempotencyKey: "fv", Name: "보기", Range: "A1:B3"})
				return err
			},
			remove: func() error {
				items, _ := repository.ListFilterViews(ctx, sheetID, "alice")
				return repository.DeleteFilterView(ctx, items[0].ID, "alice")
			},
			countIn: func(_, sheet string) int {
				items, _ := repository.ListFilterViews(ctx, sheet, "alice")
				return len(items)
			},
		},
		{
			name: "댓글", travels: false, restores: false,
			reason: "대화는 그 워크북에서 오간 것이다. 사본에 옮기면 사람들이 하지 않은 말을 한 것이 된다.",
			create: func() error {
				_, err := repository.CreateCommentThread(ctx, book.ID, "alice", CreateCommentThreadInput{IdempotencyKey: "cm", SheetID: sheetID, Range: "A1:A1", Content: "확인 바랍니다"})
				return err
			},
			remove: func() error {
				items, _ := repository.ListCommentThreads(ctx, book.ID, "", true)
				return repository.DeleteCommentThread(ctx, items[0].ID, "alice")
			},
			countIn: func(workbookID, _ string) int {
				items, _ := repository.ListCommentThreads(ctx, workbookID, "", true)
				return len(items)
			},
		},
		{
			name: "변경 알림", travels: false, restores: false,
			reason: "누가 무엇을 지켜볼지는 그 사람의 설정이다. 사본에 옮기면 부탁한 적 없는 메일을 받게 된다.",
			create: func() error {
				_, err := repository.CreateWatchRule(ctx, book.ID, "alice", CreateWatchRuleInput{IdempotencyKey: "wr", SheetID: sheetID, Range: "A1:A2", Label: "머리글"})
				return err
			},
			remove: func() error {
				items, _ := repository.ListWatchRules(ctx, book.ID, "alice")
				return repository.DeleteWatchRule(ctx, items[0].ID, "alice", nil)
			},
			countIn: func(workbookID, _ string) int {
				items, _ := repository.ListWatchRules(ctx, workbookID, "alice")
				return len(items)
			},
		},
	}
	for _, kind := range kinds {
		if err := kind.create(); err != nil {
			t.Fatalf("%s 만들기: %v", kind.name, err)
		}
	}
	copied, err := repository.DuplicateWorkbook(ctx, book.ID, DuplicateWorkbookInput{Title: "사본", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range kinds {
		count := kind.countIn(copied.ID, copied.Sheets[0].ID)
		if kind.travels && count != 1 {
			t.Errorf("복제: %s 가 따라가지 않았다 (%d개). %s", kind.name, count, kind.reason)
		}
		if !kind.travels && count != 0 {
			t.Errorf("복제: %s 가 따라갔다 (%d개). %s", kind.name, count, kind.reason)
		}
	}
	version, err := repository.CreateVersion(ctx, book.ID, "다 있는 상태", "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range kinds {
		if err := kind.remove(); err != nil {
			t.Fatalf("%s 지우기: %v", kind.name, err)
		}
	}
	if _, err := repository.RestoreVersion(ctx, version.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range kinds {
		count := kind.countIn(book.ID, sheetID)
		if kind.restores && count != 1 {
			t.Errorf("되돌리기: %s 가 살아나지 않았다 (%d개). %s", kind.name, count, kind.reason)
		}
		if !kind.restores && count != 0 {
			t.Errorf("되돌리기: %s 가 살아났다 (%d개). %s", kind.name, count, kind.reason)
		}
	}
}
