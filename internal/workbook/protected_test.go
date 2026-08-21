package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func protectedWorkbook(t *testing.T) (*MemoryRepository, Workbook) {
	t.Helper()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "보호", WorkspaceID: "default", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return repository, book
}

func writeCell(repository *MemoryRepository, book Workbook, actor string, row, column int, key string) error {
	value, _ := json.Marshal("값")
	_, err := repository.ApplyCells(context.Background(), CellMutation{
		SheetID: book.Sheets[0].ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: key,
		Cells: []CellInput{{Row: row, Column: column, Value: value}},
	})
	return err
}

// A protected range is what keeps a shared model's inputs from being edited by
// everyone who can open the workbook.
func TestProtectedRangeBlocksEveryoneButItsEditors(t *testing.T) {
	t.Parallel()
	repository, book := protectedWorkbook(t)
	if _, err := repository.CreateProtectedRange(context.Background(), book.Sheets[0].ID, "owner", CreateProtectedRangeInput{
		IdempotencyKey: "p1", Range: "B2:C5", Description: "요율표", Editors: []string{"analyst@corp.example"},
	}); err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(context.Background(), book.ID)

	// A collaborator with no place on the list is refused with a reason.
	err := writeCell(repository, book, "reader@corp.example", 3, 2, "blocked")
	var failure *ProtectionFailure
	if !errors.As(err, &failure) || len(failure.Violations) != 1 {
		t.Fatalf("write was not blocked: %v", err)
	}
	if failure.Violations[0].Row != 3 || failure.Violations[0].Column != 2 {
		t.Fatalf("violation=%#v", failure.Violations[0])
	}
	if failure.Error() == "" || !contains(failure.Error(), "요율표") {
		t.Fatalf("message does not explain the protection: %s", failure.Error())
	}

	// The listed editor and the owner may write.
	if err := writeCell(repository, book, "analyst@corp.example", 3, 2, "editor"); err != nil {
		t.Fatalf("listed editor was blocked: %v", err)
	}
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	if err := writeCell(repository, book, "owner", 4, 3, "owner"); err != nil {
		t.Fatalf("owner was blocked: %v", err)
	}
	// Outside the range nobody is restricted.
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	if err := writeCell(repository, book, "reader@corp.example", 9, 9, "outside"); err != nil {
		t.Fatalf("an unprotected cell was blocked: %v", err)
	}
}

// A warning-only range is a note to the writer, not a wall.
func TestWarningOnlyProtectionAllowsTheWrite(t *testing.T) {
	t.Parallel()
	repository, book := protectedWorkbook(t)
	if _, err := repository.CreateProtectedRange(context.Background(), book.Sheets[0].ID, "owner", CreateProtectedRangeInput{
		IdempotencyKey: "p1", Range: "A1:A5", WarningOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	if err := writeCell(repository, book, "reader@corp.example", 2, 1, "warned"); err != nil {
		t.Fatalf("a warning should not block: %v", err)
	}
}

// The rule guards cells, so it moves when the cells move.
func TestProtectedRangeFollowsInsertedRows(t *testing.T) {
	t.Parallel()
	repository, book := protectedWorkbook(t)
	rule, err := repository.CreateProtectedRange(context.Background(), book.Sheets[0].ID, "owner", CreateProtectedRangeInput{
		IdempotencyKey: "p1", Range: "B5:B9",
	})
	if err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(context.Background(), book.ID)
	if _, err := repository.ApplyStructure(context.Background(), StructuralMutation{
		SheetID: book.Sheets[0].ID, ActorID: "owner", IdempotencyKey: "insert", BaseVersion: book.Version,
		Axis: "row", Action: "insert", Index: 2, Count: 3,
	}); err != nil {
		t.Fatal(err)
	}
	rules, err := repository.ListProtectedRanges(context.Background(), book.Sheets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Range != "B8:B12" || rules[0].ID != rule.ID {
		t.Fatalf("rules=%#v", rules)
	}
}

func TestProtectedRangeRejectsWhatItCannotStore(t *testing.T) {
	t.Parallel()
	repository, book := protectedWorkbook(t)
	for name, input := range map[string]CreateProtectedRangeInput{
		"no range":       {IdempotencyKey: "a", Range: ""},
		"invalid range":  {IdempotencyKey: "b", Range: "not-a-range"},
		"no idempotency": {Range: "A1:A2"},
	} {
		if _, err := repository.CreateProtectedRange(context.Background(), book.Sheets[0].ID, "owner", input); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}

// A sheet protection locks everything except the ranges it names, which is how
// a locked model keeps its input cells usable.
func TestSheetProtectionLeavesExceptionsEditable(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "시트 보호", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	if _, err := repository.PutWorkbookShare(ctx, book.ID, ShareInput{PrincipalType: "user", PrincipalID: "bob", Role: RoleEditor}); err != nil {
		t.Fatal(err)
	}
	rule, err := repository.CreateProtectedRange(ctx, sheet, "alice", CreateProtectedRangeInput{
		IdempotencyKey: "sheet-lock", Scope: "sheet", Exceptions: []string{"b2:c4", "E1:E1"}, Description: "확정된 모델",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rule.Scope != "sheet" || rule.Range != SheetProtectionRange || len(rule.Exceptions) != 2 || rule.Exceptions[0] != "B2:C4" {
		t.Fatalf("rule = %#v", rule)
	}
	current, _ := repository.GetWorkbook(ctx, book.ID)
	// An input cell inside an exception stays editable for a collaborator.
	applied, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "bob", BaseVersion: current.Version, IdempotencyKey: "inside", Cells: []CellInput{{Row: 3, Column: 3, Value: json.RawMessage(`5`)}}})
	if err != nil {
		t.Fatalf("exception cell should be writable: %v", err)
	}
	// Anywhere else on the sheet is refused with the sheet-wide message.
	_, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "bob", BaseVersion: applied.ServerVersion, IdempotencyKey: "outside", Cells: []CellInput{{Row: 9, Column: 9, Value: json.RawMessage(`1`)}}})
	var failure *ProtectionFailure
	if !errors.As(err, &failure) {
		t.Fatalf("protected write error = %v", err)
	}
	if !strings.Contains(failure.Violations[0].Message, "이 시트는") || !strings.Contains(failure.Violations[0].Message, "확정된 모델") {
		t.Fatalf("message = %q", failure.Violations[0].Message)
	}
	// The owner is never locked out of their own sheet.
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: applied.ServerVersion, IdempotencyKey: "owner", Cells: []CellInput{{Row: 9, Column: 9, Value: json.RawMessage(`1`)}}}); err != nil {
		t.Fatalf("owner write = %v", err)
	}
	// Inserting a row moves the editable window with the rows it protects.
	current, _ = repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet, ActorID: "alice", BaseVersion: current.Version, IdempotencyKey: "shift", Axis: "row", Action: "insert", Index: 1, Count: 1}); err != nil {
		t.Fatal(err)
	}
	rules, _ := repository.ListProtectedRanges(ctx, sheet)
	if len(rules) != 1 || rules[0].Exceptions[0] != "B3:C5" {
		t.Fatalf("exceptions after insert = %#v", rules)
	}
}
