package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestMemoryWorkbookSearchMatchesValuesAndFormulasWithStablePagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "검색", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	firstSheet := book.Sheets[0]
	secondSheet, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "두번째"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: firstSheet.ID, ActorID: "owner", BaseVersion: 2, IdempotencyKey: "search-first", Cells: []CellInput{
		{Row: 2, Column: 2, Value: json.RawMessage(`"분기 매출"`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`42`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: secondSheet.ID, ActorID: "owner", BaseVersion: 3, IdempotencyKey: "search-second", Cells: []CellInput{
		{Row: 1, Column: 1, Formula: `=CONCAT("매출", " 합계")`},
	}}); err != nil {
		t.Fatal(err)
	}

	first, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: " 매출 ", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.Query != "매출" || len(first.Items) != 1 || first.Items[0].SheetID != firstSheet.ID || first.Items[0].Address != "B2" || len(first.Items[0].MatchedFields) != 1 || first.Items[0].MatchedFields[0] != "value" || first.NextOffset == nil || *first.NextOffset != 1 {
		t.Fatalf("first search page = %#v", first)
	}
	second, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "매출", Limit: 1, Offset: *first.NextOffset})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].SheetID != secondSheet.ID || second.Items[0].Address != "A1" || second.Items[0].Formula == "" || second.NextOffset != nil || len(second.Items[0].MatchedFields) != 2 || second.Items[0].MatchedFields[1] != "formula" {
		t.Fatalf("second search page = %#v", second)
	}
	number, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "42"})
	if err != nil || len(number.Items) != 1 || number.Items[0].Address != "A3" {
		t.Fatalf("numeric search = %#v, %v", number, err)
	}
}

func TestWorkbookSearchRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "검색", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []SearchWorkbookInput{{}, {Query: "x", Limit: MaxSearchLimit + 1}, {Query: "x", Offset: -1}, {Query: "x", Offset: MaxSearchOffset + 1}} {
		if _, err := repository.SearchWorkbook(context.Background(), book.ID, input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("input %#v error = %v", input, err)
		}
	}
}
