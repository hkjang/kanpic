package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestWorkbookSearchOptionsNarrowMatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "옵션", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	first := book.Sheets[0]
	second, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "두번째"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: first.ID, ActorID: "owner", BaseVersion: 2, IdempotencyKey: "options-first", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"Seoul"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"seoul office"`)},
		{Row: 3, Column: 1, Formula: `=UPPER("seoul")`},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: second.ID, ActorID: "owner", BaseVersion: 3, IdempotencyKey: "options-second", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"SEOUL"`)},
	}}); err != nil {
		t.Fatal(err)
	}

	matchCase, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "Seoul", MatchCase: true})
	if err != nil || len(matchCase.Items) != 1 || matchCase.Items[0].Address != "A1" || matchCase.Items[0].SheetID != first.ID {
		t.Fatalf("match case search = %#v, %v", matchCase, err)
	}
	wholeCell, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "seoul", WholeCell: true})
	if err != nil || len(wholeCell.Items) != 2 {
		t.Fatalf("whole cell search = %#v, %v", wholeCell, err)
	}
	scoped, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "seoul", SheetID: second.ID})
	if err != nil || len(scoped.Items) != 1 || scoped.Items[0].SheetID != second.ID {
		t.Fatalf("sheet scoped search = %#v, %v", scoped, err)
	}
	skipFormulas, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "upper", SkipFormulas: true})
	if err != nil || len(skipFormulas.Items) != 0 {
		t.Fatalf("skip formula search = %#v, %v", skipFormulas, err)
	}
	regex, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: `^seoul\s+office$`, UseRegex: true})
	if err != nil || len(regex.Items) != 1 || regex.Items[0].Address != "A2" {
		t.Fatalf("regex search = %#v, %v", regex, err)
	}
	if _, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "seoul(", UseRegex: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid regex error = %v", err)
	}
}

func TestReplaceWorkbookCellsPreviewsThenAppliesAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "바꾸기", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	first := book.Sheets[0]
	second, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "두번째"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: first.ID, ActorID: "owner", BaseVersion: 2, IdempotencyKey: "replace-first", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"서울 본사"`), Style: json.RawMessage(`{"bold":true}`)},
		{Row: 2, Column: 1, Formula: `=CONCAT("서울"," 지사")`},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: second.ID, ActorID: "owner", BaseVersion: 3, IdempotencyKey: "replace-second", Cells: []CellInput{
		{Row: 5, Column: 2, Value: json.RawMessage(`"서울"`)},
	}}); err != nil {
		t.Fatal(err)
	}

	preview, err := ReplaceWorkbookCells(ctx, repository, book.ID, ReplaceWorkbookInput{Query: "서울", Replacement: "부산", Preview: true, ActorID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.MatchedCells != 3 || preview.PlannedCells != 3 || preview.ReplacedCells != 0 || len(preview.Items) != 3 || preview.Items[0].After != "부산 본사" {
		t.Fatalf("preview = %#v", preview)
	}
	if preview.Items[1].Field != "formula" || preview.Items[1].After != `=CONCAT("부산"," 지사")` {
		t.Fatalf("formula preview = %#v", preview.Items[1])
	}
	unchanged, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "서울"})
	if err != nil || len(unchanged.Items) != 3 {
		t.Fatalf("preview must not mutate: %#v, %v", unchanged, err)
	}

	applied, err := ReplaceWorkbookCells(ctx, repository, book.ID, ReplaceWorkbookInput{Query: "서울", Replacement: "부산", IdempotencyKey: "replace-run", ActorID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if applied.ReplacedCells != 3 || len(applied.Sheets) != 2 || applied.Sheets[0].SheetID != first.ID || applied.ServerVersion <= applied.WorkbookVersion {
		t.Fatalf("applied = %#v", applied)
	}
	remaining, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "서울"})
	if err != nil || len(remaining.Items) != 0 {
		t.Fatalf("remaining matches = %#v, %v", remaining, err)
	}
	replaced, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "부산", SheetID: first.ID})
	if err != nil || len(replaced.Items) != 2 {
		t.Fatalf("replaced matches = %#v, %v", replaced, err)
	}
	if string(replaced.Items[0].Style) != `{"bold":true}` {
		t.Fatalf("replace must preserve style: %s", replaced.Items[0].Style)
	}
	if replaced.Items[1].Formula != `=CONCAT("부산"," 지사")` {
		t.Fatalf("replaced formula = %q", replaced.Items[1].Formula)
	}
}

func TestReplaceWorkbookCellsKeepsValueTypesAndCasing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "형식", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "owner", BaseVersion: 1, IdempotencyKey: "types", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`1200`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"Alpha ALPHA alpha"`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplaceWorkbookCells(ctx, repository, book.ID, ReplaceWorkbookInput{Query: "12", Replacement: "34", IdempotencyKey: "types-number", ActorID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplaceWorkbookCells(ctx, repository, book.ID, ReplaceWorkbookInput{Query: "alpha", Replacement: "베타", IdempotencyKey: "types-text", ActorID: "owner"}); err != nil {
		t.Fatal(err)
	}
	number, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "3400"})
	if err != nil || len(number.Items) != 1 || string(number.Items[0].Value) != "3400" {
		t.Fatalf("numeric replacement = %#v, %v", number, err)
	}
	text, err := repository.SearchWorkbook(ctx, book.ID, SearchWorkbookInput{Query: "베타"})
	if err != nil || len(text.Items) != 1 || string(text.Items[0].Value) != `"베타 베타 베타"` {
		t.Fatalf("case-insensitive replacement = %#v, %v", text, err)
	}
}

func TestReplaceWorkbookCellsRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "검증", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []ReplaceWorkbookInput{
		{Query: "", Replacement: "x", IdempotencyKey: "k"},
		{Query: "x", Replacement: "y"},
		{Query: "x", Replacement: strings.Repeat("가", MaxReplacementRunes+1), IdempotencyKey: "k"},
		{Query: "x(", Replacement: "y", UseRegex: true, IdempotencyKey: "k"},
	} {
		if _, err := ReplaceWorkbookCells(ctx, repository, book.ID, input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("input %#v error = %v", input, err)
		}
	}
}
