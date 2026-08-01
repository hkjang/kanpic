package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestNamedRangeRecalculatesRenamesVersionsAndDuplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "named", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	inputSheet := book.Sheets[0]
	reportSheet, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "Report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: inputSheet.ID, ActorID: "owner", BaseVersion: 2, IdempotencyKey: "seed", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`10`)}, {Row: 2, Column: 1, Value: json.RawMessage(`20`)}}}); err != nil {
		t.Fatal(err)
	}
	missing, err := repository.ApplyCells(ctx, CellMutation{SheetID: reportSheet.ID, ActorID: "owner", BaseVersion: 3, IdempotencyKey: "formula", Cells: []CellInput{{Row: 1, Column: 2, Formula: "=SUM(Sales_Data)"}}})
	if err != nil || len(missing.FormulaErrors) != 1 || missing.FormulaErrors[0].Code != "#NAME?" {
		t.Fatalf("missing named formula = %#v, error=%v", missing, err)
	}
	created, err := repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: "range-create", Name: "Sales_Data", SheetID: inputSheet.ID, Range: "A1:A2"})
	if err != nil || created.WorkbookVersion != 5 || created.Revision != 1 {
		t.Fatalf("created named range = %#v, error=%v", created, err)
	}
	duplicate, err := repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: "range-create", Name: "ignored", SheetID: reportSheet.ID, Range: "B1"})
	if err != nil || duplicate.ID != created.ID || duplicate.WorkbookVersion != 5 {
		t.Fatalf("idempotent named range = %#v, error=%v", duplicate, err)
	}
	assertNamedFormulaCell(t, repository, reportSheet.ID, "B1", "=SUM(Sales_Data)", "30")

	updated, err := repository.UpdateNamedRange(ctx, created.ID, "owner", UpdateNamedRangeInput{Range: testPointer("A1"), ExpectedRevision: testPointer(int64(1))})
	if err != nil || updated.Revision != 2 || updated.WorkbookVersion != 6 {
		t.Fatalf("updated named range = %#v, error=%v", updated, err)
	}
	assertNamedFormulaCell(t, repository, reportSheet.ID, "B1", "=SUM(Sales_Data)", "10")

	renamed, err := repository.UpdateNamedRange(ctx, created.ID, "owner", UpdateNamedRangeInput{Name: testPointer("Revenue"), ExpectedRevision: testPointer(int64(2))})
	if err != nil || renamed.Revision != 3 || renamed.WorkbookVersion != 7 {
		t.Fatalf("renamed named range = %#v, error=%v", renamed, err)
	}
	assertNamedFormulaCell(t, repository, reportSheet.ID, "B1", "=SUM(Revenue)", "10")
	version, err := repository.CreateVersion(ctx, book.ID, "with name", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteNamedRange(ctx, created.ID, "owner", testPointer(int64(3))); err != nil {
		t.Fatal(err)
	}
	assertNamedFormulaCell(t, repository, reportSheet.ID, "B1", "=SUM(Revenue)", `"#NAME?"`)
	restored, err := repository.RestoreVersion(ctx, version.ID, "owner")
	if err != nil || restored.ServerVersion != 9 {
		t.Fatalf("restore named range = %#v, error=%v", restored, err)
	}
	ranges, err := repository.ListNamedRanges(ctx, book.ID)
	if err != nil || len(ranges) != 1 || ranges[0].Name != "Revenue" || ranges[0].Range != "A1" {
		t.Fatalf("restored named ranges = %#v, error=%v", ranges, err)
	}
	assertNamedFormulaCell(t, repository, reportSheet.ID, "B1", "=SUM(Revenue)", "10")

	copy, err := repository.DuplicateWorkbook(ctx, book.ID, DuplicateWorkbookInput{OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	copyRanges, err := repository.ListNamedRanges(ctx, copy.ID)
	if err != nil || len(copyRanges) != 1 || copyRanges[0].SheetID == inputSheet.ID {
		t.Fatalf("duplicated named ranges = %#v, error=%v", copyRanges, err)
	}
	var copyReport Sheet
	for _, sheet := range copy.Sheets {
		if sheet.Name == "Report" {
			copyReport = sheet
		}
	}
	assertNamedFormulaCell(t, repository, copyReport.ID, "B1", "=SUM(Revenue)", "10")
}

func TestNamedRangeValidationAndRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "named validation"})
	other, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "other"})
	_, err := repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: "invalid-name", Name: "A1", SheetID: book.Sheets[0].ID, Range: "A1"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("cell-like name error = %v", err)
	}
	_, err = repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: "foreign", Name: "Foreign", SheetID: other.Sheets[0].ID, Range: "A1"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign sheet error = %v", err)
	}
	for index, target := range []string{"XFE1", "A1048577"} {
		_, err = repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: fmt.Sprintf("invalid-target-%d", index), Name: fmt.Sprintf("InvalidTarget%d", index), SheetID: book.Sheets[0].ID, Range: target})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid target %q error = %v", target, err)
		}
	}
	created, err := repository.CreateNamedRange(ctx, book.ID, "owner", CreateNamedRangeInput{IdempotencyKey: "valid", Name: "Amount", SheetID: book.Sheets[0].ID, Range: "$A$1:b2"})
	if err != nil || created.Range != "A1:B2" {
		t.Fatalf("normalized range = %#v, error=%v", created, err)
	}
	_, err = repository.UpdateNamedRange(ctx, created.ID, "owner", UpdateNamedRangeInput{Range: testPointer("C1"), ExpectedRevision: testPointer(int64(9))})
	if !errors.Is(err, ErrRevision) {
		t.Fatalf("revision error = %v", err)
	}
	for _, validName := range []string{"A0", "A00", "XFE1", "Sales2026", "A1048577", "_A1"} {
		if _, err := normalizeNamedRangeName(validName); err != nil {
			t.Errorf("valid name %q: %v", validName, err)
		}
	}
	for _, invalidName := range []string{"A1", "XFD1048576", "true", "FALSE"} {
		if _, err := normalizeNamedRangeName(invalidName); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid name %q error = %v", invalidName, err)
		}
	}
}

func testPointer[T any](value T) *T { return &value }

func assertNamedFormulaCell(t *testing.T, repository Repository, sheetID, address, formula, value string) {
	t.Helper()
	selected, err := cellrange.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	cells, err := repository.ReadRange(context.Background(), sheetID, selected)
	if err != nil || len(cells) != 1 || cells[0].Formula != formula || string(cells[0].Value) != value {
		t.Fatalf("formula cell %s = %#v, error=%v; want formula=%s value=%s", address, cells, err, formula, value)
	}
}
