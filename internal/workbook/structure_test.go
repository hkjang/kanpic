package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestMemoryStructureMutationMovesCellsReferencesAndDefinitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "structure", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	inputSheet := book.Sheets[0]
	reportSheet, err := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "Report"})
	if err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(ctx, book.ID)
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: inputSheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "structure-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`10`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`20`)},
		{Row: 2, Column: 2, Formula: "=A2*2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: reportSheet.ID, ActorID: "alice", BaseVersion: seed.ServerVersion, IdempotencyKey: "structure-report", Cells: []CellInput{{Row: 1, Column: 1, Formula: "=Sheet1!A2"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateNamedRange(ctx, book.ID, "alice", CreateNamedRangeInput{IdempotencyKey: "structure-name", Name: "Sales", SheetID: inputSheet.ID, Range: "A1:A2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateDataValidation(ctx, inputSheet.ID, "alice", CreateDataValidationInput{IdempotencyKey: "structure-validation", Range: "B1:B3", RuleType: "number", Operator: "greater_than", Value: json.RawMessage(`0`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateFilterView(ctx, inputSheet.ID, "alice", CreateFilterViewInput{IdempotencyKey: "structure-filter", Name: "Rows", Range: "A1:B4", HeaderRows: 1, Criteria: []FilterCriterion{{Column: 1, Operator: "is_not_blank"}}, Active: true}); err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(ctx, book.ID)
	inserted, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: inputSheet.ID, ActorID: "alice", ClientID: "browser", BaseVersion: book.Version, IdempotencyKey: "insert-row", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil || inserted.ServerVersion != book.Version+1 || inserted.BackupVersionID == "" || inserted.StructuralAxis != "row" {
		t.Fatalf("inserted = %#v, error=%v", inserted, err)
	}
	duplicate, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: inputSheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "insert-row", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil || !duplicate.Duplicate || duplicate.OperationID != inserted.OperationID {
		t.Fatalf("duplicate = %#v, error=%v", duplicate, err)
	}
	assertStructureCell(t, repository, inputSheet.ID, "A3", "", "20")
	assertStructureCell(t, repository, inputSheet.ID, "B3", "=A3*2", "40")
	assertStructureCell(t, repository, reportSheet.ID, "A1", "=Sheet1!A3", "20")
	ranges, _ := repository.ListNamedRanges(ctx, book.ID)
	if len(ranges) != 1 || ranges[0].Range != "A1:A3" || ranges[0].Revision != 2 {
		t.Fatalf("named ranges after insert = %#v", ranges)
	}
	rules, _ := repository.ListDataValidations(ctx, inputSheet.ID)
	if len(rules) != 1 || rules[0].Range != "B1:B4" || rules[0].Revision != 2 {
		t.Fatalf("validations after insert = %#v", rules)
	}
	views, _ := repository.ListFilterViews(ctx, inputSheet.ID, "alice")
	if len(views) != 1 || views[0].Range != "A1:B5" || views[0].HeaderRows != 1 {
		t.Fatalf("filters after insert = %#v", views)
	}
	versions, _ := repository.ListVersions(ctx, book.ID)
	if len(versions) != 1 || versions[0].ID != inserted.BackupVersionID || versions[0].Name != "행 삽입 전 자동 백업" {
		t.Fatalf("structure versions = %#v", versions)
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: inputSheet.ID, ActorID: "bob", BaseVersion: book.Version, IdempotencyKey: "stale", Axis: "row", Action: "delete", Index: 1, Count: 1}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale structure error = %v", err)
	}
	deleted, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: inputSheet.ID, ActorID: "alice", BaseVersion: inserted.ServerVersion, IdempotencyKey: "delete-rows", Axis: "row", Action: "delete", Index: 1, Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	assertStructureCell(t, repository, reportSheet.ID, "A1", "=#REF!", `"#REF!"`)
	ranges, _ = repository.ListNamedRanges(ctx, book.ID)
	if len(ranges) != 1 || ranges[0].Range != "#REF!" || ranges[0].Revision != 3 {
		t.Fatalf("named ranges after delete = %#v", ranges)
	}
	if _, err := repository.RestoreVersion(ctx, deleted.BackupVersionID, "alice"); err != nil {
		t.Fatal(err)
	}
	assertStructureCell(t, repository, inputSheet.ID, "A3", "", "20")
	ranges, _ = repository.ListNamedRanges(ctx, book.ID)
	if len(ranges) != 1 || ranges[0].Range != "A1:A3" {
		t.Fatalf("restored structural named ranges = %#v", ranges)
	}
	views, _ = repository.ListFilterViews(ctx, inputSheet.ID, "alice")
	if len(views) != 1 || views[0].Range != "A1:B5" || views[0].HeaderRows != 1 {
		t.Fatalf("restored structural filters = %#v", views)
	}
}

func TestStructureMutationExpandsAndContractsMerges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "merge structure"})
	sheetID := book.Sheets[0].ID
	selected, _ := cellrange.Parse("A1:B2")
	mergeInputs, err := BuildMergeCells(nil, selected, true)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, BaseVersion: 1, IdempotencyKey: "merge", Cells: mergeInputs})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheetID, BaseVersion: merged.ServerVersion, IdempotencyKey: "merge-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	cells, _ := repository.ReadRange(ctx, sheetID, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 3, Column: 2}})
	if len(cells) != 6 {
		t.Fatalf("expanded merge cells = %d, %#v", len(cells), cells)
	}
	for _, cell := range cells {
		metadata, ok, metadataErr := CellMerge(cell)
		if metadataErr != nil || !ok || metadata != (MergeMetadata{StartRow: 1, StartColumn: 1, EndRow: 3, EndColumn: 2}) {
			t.Fatalf("expanded merge metadata = %#v, %v, %v", metadata, ok, metadataErr)
		}
	}
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheetID, BaseVersion: inserted.ServerVersion, IdempotencyKey: "merge-delete", Axis: "column", Action: "delete", Index: 2, Count: 1}); err != nil {
		t.Fatal(err)
	}
	cells, _ = repository.ReadRange(ctx, sheetID, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 3, Column: 1}})
	if len(cells) != 3 {
		t.Fatalf("contracted merge cells = %d, %#v", len(cells), cells)
	}
}

func TestStructureMutationDoesNotReviseUnaffectedDefinitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "unaffected structure"})
	sheetID := book.Sheets[0].ID
	name, err := repository.CreateNamedRange(ctx, book.ID, "alice", CreateNamedRangeInput{IdempotencyKey: "unaffected-name", Name: "Header", SheetID: sheetID, Range: "A1"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := repository.CreateDataValidation(ctx, sheetID, "alice", CreateDataValidationInput{IdempotencyKey: "unaffected-rule", Range: "B1", RuleType: "number", Operator: "greater_than", Value: json.RawMessage(`0`)})
	if err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "insert-after-definitions", Axis: "row", Action: "insert", Index: 100, Count: 1}); err != nil {
		t.Fatal(err)
	}
	names, _ := repository.ListNamedRanges(ctx, book.ID)
	rules, _ := repository.ListDataValidations(ctx, sheetID)
	if len(names) != 1 || names[0].Revision != name.Revision || names[0].Range != "A1" {
		t.Fatalf("unaffected named range changed: %#v", names)
	}
	if len(rules) != 1 || rules[0].Revision != rule.Revision || rules[0].Range != "B1" {
		t.Fatalf("unaffected validation changed: %#v", rules)
	}
}

func assertStructureCell(t *testing.T, repository Repository, sheetID, address, formulaText, value string) {
	t.Helper()
	selected, _ := cellrange.Parse(address)
	cells, err := repository.ReadRange(context.Background(), sheetID, selected)
	if err != nil || len(cells) != 1 || cells[0].Formula != formulaText || string(cells[0].Value) != value {
		t.Fatalf("cell %s = %#v, error=%v; want formula=%s value=%s", address, cells, err, formulaText, value)
	}
}
