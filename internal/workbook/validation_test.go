package workbook

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func validationRaw(value string) json.RawMessage { return json.RawMessage(value) }

func TestNormalizeDataValidationSupportsListNumberDateAndFormula(t *testing.T) {
	list, selected, err := NewDataValidation("sheet", "owner", CreateDataValidationInput{
		IdempotencyKey: "list", Range: "b2:b5", RuleType: "LIST",
		Options: []ValidationOption{{Value: validationRaw(`"open"`), Color: "#FEF3C7"}, {Value: validationRaw(`2`), Label: "Two"}},
	})
	if err != nil || list.Range != "B2:B5" || list.Operator != "in_list" || list.DisplayStyle != "chip" || !list.AllowBlank || !list.RejectInput || !list.ShowDropdown || list.Options[0].Label != "open" || list.Options[0].Color != "#fef3c7" || !selected.Contains(4, 2) {
		t.Fatalf("normalized list: %#v range=%#v err=%v", list, selected, err)
	}
	for _, rule := range []DataValidation{
		{Range: "A1:A3", RuleType: "number", Operator: "between", Value: validationRaw(`1`), Value2: validationRaw(`10`)},
		{Range: "A1:A3", RuleType: "date", Operator: "greater_or_equal", Value: validationRaw(`"2026-01-01"`)},
		{Range: "A1:A3", RuleType: "custom_formula", Formula: "=A1>0"},
	} {
		if _, _, err := NormalizeDataValidation(rule); err != nil {
			t.Fatalf("normalize %#v: %v", rule, err)
		}
	}
	invalid := []DataValidation{
		{Range: "A1:A3", RuleType: "list", Options: []ValidationOption{{Value: validationRaw(`"x"`)}, {Value: validationRaw(`"x"`)}}},
		{Range: "A1:A3", RuleType: "number", Operator: "between", Value: validationRaw(`10`), Value2: validationRaw(`1`)},
		{Range: "A1:A3", RuleType: "date", Operator: "equal", Value: validationRaw(`"31/12/2026"`)},
		{Range: "A1:A3", RuleType: "custom_formula", Formula: "=SUM("},
	}
	for _, rule := range invalid {
		if _, _, err := NormalizeDataValidation(rule); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid rule %#v, got %v", rule, err)
		}
	}
}

func TestValidateCellInputsRejectsAndWarnsAgainstProspectiveValues(t *testing.T) {
	existing := map[cellKey]Cell{{1, 1}: {Row: 1, Column: 1, Value: validationRaw(`1`)}}
	expanded := []CellInput{{Row: 2, Column: 1, Value: validationRaw(`-1`)}, {Row: 2, Column: 2, Value: validationRaw(`"other"`)}}
	submitted := append([]CellInput(nil), expanded...)
	rules := []DataValidation{
		{ID: "formula", Range: "A1:A3", RuleType: "custom_formula", Operator: "custom", Formula: "=A1>0", AllowBlank: true, RejectInput: true, DisplayStyle: "plain"},
		{ID: "warning", Range: "B1:B3", RuleType: "list", Operator: "in_list", Options: []ValidationOption{{Value: validationRaw(`"open"`)}}, AllowBlank: true, RejectInput: false, ShowDropdown: true, DisplayStyle: "chip"},
	}
	warnings, err := ValidateCellInputs(rules, existing, expanded, submitted)
	var failure *ValidationFailure
	if !errors.As(err, &failure) || len(failure.Violations) != 1 || failure.Violations[0].ValidationID != "formula" {
		t.Fatalf("failure = %#v err=%v", failure, err)
	}
	if len(warnings) != 1 || warnings[0].ValidationID != "warning" {
		t.Fatalf("warnings = %#v", warnings)
	}

	expanded[0].Value = validationRaw(`2`)
	warnings, err = ValidateCellInputs(rules, existing, expanded, submitted)
	if err != nil || len(warnings) != 1 {
		t.Fatalf("valid formula warnings=%#v err=%v", warnings, err)
	}
}

func TestEvaluateDataValidationUsesTypedValuesDatesAndBlankPolicy(t *testing.T) {
	rule := DataValidation{ID: "numbers", Range: "A1:A4", RuleType: "number", Operator: "between", Value: validationRaw(`10`), Value2: validationRaw(`20`), AllowBlank: false, RejectInput: true, DisplayStyle: "plain"}
	result, err := EvaluateDataValidation(rule, []Cell{{Row: 1, Column: 1, Value: validationRaw(`10`)}, {Row: 2, Column: 1, Value: validationRaw(`21`)}, {Row: 4, Column: 1, Value: validationRaw(`15`)}})
	if err != nil || result.CheckedCells != 4 || result.ValidCells != 2 || !reflect.DeepEqual([]int{result.InvalidCells[0].Row, result.InvalidCells[1].Row}, []int{2, 3}) {
		t.Fatalf("number evaluation = %#v err=%v", result, err)
	}
	date := DataValidation{Range: "A1:A1", RuleType: "date", Operator: "between", Value: validationRaw(`"2026-01-01"`), Value2: validationRaw(`"2026-12-31"`), AllowBlank: true, DisplayStyle: "plain"}
	if evaluated, err := EvaluateDataValidation(date, []Cell{{Row: 1, Column: 1, Value: validationRaw(`"2026-07-31T10:00:00Z"`)}}); err != nil || evaluated.ValidCells != 1 {
		t.Fatalf("date evaluation=%#v err=%v", evaluated, err)
	}
}

func TestMemoryDataValidationCRUDOverlapRevisionAndWriteEnforcement(t *testing.T) {
	repository := NewMemoryRepository()
	ctx := t.Context()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "validations"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	created, err := repository.CreateDataValidation(ctx, sheetID, "alice", CreateDataValidationInput{IdempotencyKey: "create", Range: "A1:A3", RuleType: "list", Options: []ValidationOption{{Value: validationRaw(`"open"`)}, {Value: validationRaw(`"closed"`)}}})
	if err != nil || created.Revision != 1 || created.WorkbookVersion != 2 {
		t.Fatalf("create=%#v err=%v", created, err)
	}
	duplicate, err := repository.CreateDataValidation(ctx, sheetID, "bob", CreateDataValidationInput{IdempotencyKey: "create", Range: "invalid"})
	if err != nil || duplicate.ID != created.ID || duplicate.WorkbookVersion != 2 {
		t.Fatalf("duplicate=%#v err=%v", duplicate, err)
	}
	if _, err := repository.CreateDataValidation(ctx, sheetID, "alice", CreateDataValidationInput{IdempotencyKey: "overlap", Range: "A3:B4", RuleType: "list", Options: []ValidationOption{{Value: validationRaw(`1`)}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlap err=%v", err)
	}
	wrongRevision := int64(99)
	nextRange := "B1:B3"
	if _, err := repository.UpdateDataValidation(ctx, created.ID, "alice", UpdateDataValidationInput{Range: &nextRange, ExpectedRevision: &wrongRevision}); !errors.Is(err, ErrRevision) {
		t.Fatalf("revision err=%v", err)
	}
	expected := created.Revision
	updated, err := repository.UpdateDataValidation(ctx, created.ID, "alice", UpdateDataValidationInput{Range: &nextRange, ExpectedRevision: &expected})
	if err != nil || updated.Revision != 2 || updated.WorkbookVersion != 3 || updated.Range != "B1:B3" {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 3, IdempotencyKey: "invalid-write", Cells: []CellInput{{Row: 1, Column: 2, Value: validationRaw(`"other"`)}}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid write err=%v", err)
	}
	accepted, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 3, IdempotencyKey: "valid-write", Cells: []CellInput{{Row: 1, Column: 2, Value: validationRaw(`"open"`)}}})
	if err != nil || accepted.ServerVersion != 4 {
		t.Fatalf("valid write=%#v err=%v", accepted, err)
	}
	stale := int64(1)
	if err := repository.DeleteDataValidation(ctx, created.ID, "alice", &stale); !errors.Is(err, ErrRevision) {
		t.Fatalf("delete revision err=%v", err)
	}
	current := updated.Revision
	if err := repository.DeleteDataValidation(ctx, created.ID, "alice", &current); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetDataValidation(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted get err=%v", err)
	}
}

func TestMemoryDataValidationFollowsSheetWorkbookCopiesAndVersionRestore(t *testing.T) {
	repository := NewMemoryRepository()
	ctx := t.Context()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "validation copies", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	source := book.Sheets[0]
	rule, err := repository.CreateDataValidation(ctx, source.ID, "owner", CreateDataValidationInput{IdempotencyKey: "copy-rule", Range: "C1:C9", RuleType: "date", Operator: "greater_or_equal", Value: validationRaw(`"2026-01-01"`)})
	if err != nil {
		t.Fatal(err)
	}
	version, err := repository.CreateVersion(ctx, book.ID, "with rule", "owner")
	if err != nil {
		t.Fatal(err)
	}
	duplicatedSheet, err := repository.DuplicateSheet(ctx, source.ID, DuplicateSheetInput{})
	if err != nil {
		t.Fatal(err)
	}
	if copied, err := repository.ListDataValidations(ctx, duplicatedSheet.ID); err != nil || len(copied) != 1 || copied[0].ID == rule.ID || copied[0].Range != rule.Range {
		t.Fatalf("sheet copy rules=%#v err=%v", copied, err)
	}
	duplicatedBook, err := repository.DuplicateWorkbook(ctx, book.ID, DuplicateWorkbookInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sheet := range duplicatedBook.Sheets {
		copied, err := repository.ListDataValidations(ctx, sheet.ID)
		if err != nil || len(copied) != 1 {
			t.Fatalf("workbook copy sheet %s rules=%#v err=%v", sheet.Name, copied, err)
		}
	}
	current := rule.Revision
	if err := repository.DeleteDataValidation(ctx, rule.ID, "owner", &current); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RestoreVersion(ctx, version.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	restored, err := repository.GetDataValidation(ctx, rule.ID)
	if err != nil || restored.Range != "C1:C9" || restored.RuleType != "date" {
		t.Fatalf("restored rule=%#v err=%v", restored, err)
	}
}
