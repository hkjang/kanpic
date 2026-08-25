package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func tableSeed(t *testing.T) (*MemoryRepository, Workbook, string) {
	t.Helper()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "표", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "table-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: value("지역")}, {Row: 1, Column: 2, Value: value("금액")},
		{Row: 2, Column: 1, Value: value("서울")}, {Row: 2, Column: 2, Value: value(100)},
		{Row: 3, Column: 1, Value: value("부산")}, {Row: 3, Column: 2, Value: value(200)},
	}}); err != nil {
		t.Fatal(err)
	}
	return repository, book, sheet.ID
}

func tableCellValue(t *testing.T, repository *MemoryRepository, sheetID string, row, column int) string {
	t.Helper()
	cells, err := repository.ReadAllCells(context.Background(), sheetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range cells {
		if cell.Row == row && cell.Column == column {
			return string(cell.Value)
		}
	}
	return ""
}

// 표를 만들면 그 이름으로 수식을 적을 수 있고, 행이 끼워져도 그 수식은 그대로
// 맞아야 한다. 범위로 적었으면 사람이 옮겨 적어야 하는 자리다.
func TestMemorySheetTableFollowsStructureAndAnswersFormulas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, book, sheetID := tableSeed(t)
	table, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{
		IdempotencyKey: "t-1", SheetID: sheetID, Name: "매출표", Range: "a1:b3",
	})
	if err != nil || table.Range != "A1:B3" || !table.HeaderRow {
		t.Fatalf("만든 표=%#v, %v", table, err)
	}
	current, _ := repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: current.Version, IdempotencyKey: "t-formula", Cells: []CellInput{
		{Row: 5, Column: 1, Formula: "=SUM(매출표[금액])"},
	}}); err != nil {
		t.Fatal(err)
	}
	if actual := tableCellValue(t, repository, sheetID, 5, 1); actual != "300" {
		t.Fatalf("합계=%s", actual)
	}
	// 위에 행을 끼우면 표가 따라 내려가고 수식은 그대로 맞다.
	current, _ = repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{
		SheetID: sheetID, ActorID: "alice", BaseVersion: current.Version,
		IdempotencyKey: "t-insert", Axis: "row", Action: "insert", Index: 1, Count: 1,
	}); err != nil {
		t.Fatal(err)
	}
	moved, err := repository.GetSheetTable(ctx, table.ID)
	if err != nil || moved.Range != "A2:B4" {
		t.Fatalf("옮긴 표=%#v, %v", moved, err)
	}
	if actual := tableCellValue(t, repository, sheetID, 6, 1); actual != "300" {
		t.Fatalf("행을 끼운 뒤 합계=%s", actual)
	}
}

// 머리글을 고치면 옛 열 이름을 쓰는 수식이 그 자리에서 #REF! 가 되어야 한다.
//
// 풀리지 않는 수식은 의존성 그래프에 오르지 못한다 — 무엇에 기대는지 알아
// 내려면 먼저 풀려야 하기 때문이다. 그래서 다시 셈할 계기를 영영 얻지 못하고,
// 없어진 열의 옛 답이 맞는 답인 양 남는다. 틀린 값이 조용히 남는 것이 가장
// 나쁘므로, 머리글을 건드리면 표를 가리키는 수식을 모두 다시 센다.
func TestMemorySheetTableHeaderRenameInvalidatesStaleFormulas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, book, sheetID := tableSeed(t)
	if _, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{
		IdempotencyKey: "t-1", SheetID: sheetID, Name: "매출표", Range: "A1:B3",
	}); err != nil {
		t.Fatal(err)
	}
	current, _ := repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: current.Version, IdempotencyKey: "t-formula", Cells: []CellInput{
		{Row: 5, Column: 1, Formula: "=SUM(매출표[금액])"},
	}}); err != nil {
		t.Fatal(err)
	}
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	current, _ = repository.GetWorkbook(ctx, book.ID)
	result, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: current.Version, IdempotencyKey: "t-rename", Cells: []CellInput{
		{Row: 1, Column: 2, Value: value("매출액")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if actual := tableCellValue(t, repository, sheetID, 5, 1); actual != `"#REF!"` {
		t.Fatalf("머리글을 고쳤는데 옛 답이 남았다: %s", actual)
	}
	if len(result.FormulaErrors) != 1 || result.FormulaErrors[0].Code != "#REF!" {
		t.Fatalf("오류를 알리지 않았다: %#v", result.FormulaErrors)
	}
	// 새 이름으로 고쳐 적으면 다시 셈한다.
	current, _ = repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: current.Version, IdempotencyKey: "t-fix", Cells: []CellInput{
		{Row: 5, Column: 1, Formula: "=SUM(매출표[매출액])"},
	}}); err != nil {
		t.Fatal(err)
	}
	if actual := tableCellValue(t, repository, sheetID, 5, 1); actual != "300" {
		t.Fatalf("새 이름 합계=%s", actual)
	}
}

// 이름이 겹치거나 표끼리 걸치면 만들 때 막는다. 겹친 이름은 어느 것을
// 가리키는지 알 수 없고, 걸친 표는 한쪽에서 행을 지우면 다른 쪽이 어그러진다.
func TestSheetTableRejectsDuplicatesAndOverlaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, book, sheetID := tableSeed(t)
	if _, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{
		IdempotencyKey: "t-1", SheetID: sheetID, Name: "매출표", Range: "A1:B3",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{
		IdempotencyKey: "t-2", SheetID: sheetID, Name: "매출표", Range: "D1:E3",
	}); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("겹친 이름=%v", err)
	}
	if _, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{
		IdempotencyKey: "t-3", SheetID: sheetID, Name: "다른표", Range: "B2:C5",
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("걸친 표=%v", err)
	}
	// 머리글만 있고 자료가 없으면 가리킬 것이 없다.
	if _, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{
		IdempotencyKey: "t-4", SheetID: sheetID, Name: "빈표", Range: "H1:H1",
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("자료 없는 표=%v", err)
	}
	// 같은 열쇠로 다시 부르면 같은 것이 돌아온다.
	again, err := repository.CreateSheetTable(ctx, book.ID, "alice", CreateSheetTableInput{
		IdempotencyKey: "t-1", SheetID: sheetID, Name: "아무이름", Range: "Z1:Z9",
	})
	if err != nil || again.Name != "매출표" {
		t.Errorf("멱등성=%#v, %v", again, err)
	}
}

// 머리글에 같은 글자가 둘이면 뒤엣것에 번호를 붙인다. 그대로 두면
// 매출표[금액] 이 어느 열인지 정해지지 않는다.
func TestTableColumnsNumberDuplicateHeaders(t *testing.T) {
	t.Parallel()
	columns := TableColumns(SheetTable{Range: "A1:D2", HeaderRow: true}, []string{"금액", "", "금액", "금액2"})
	want := []string{"금액", "열2", "금액2", "금액22"}
	for index, expected := range want {
		if columns[index] != expected {
			t.Errorf("열 %d = %q, want %q (전체 %v)", index, columns[index], expected, columns)
		}
	}
}
