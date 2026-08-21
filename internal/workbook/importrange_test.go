package workbook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kanpic/pkg/cellrange"
)

// IMPORTRANGE hands one workbook another's data, so the interesting cases are
// the refusals: the importing workbook's owner governs the read, and a source
// they cannot see must say so rather than come back blank.
func TestMemoryImportRangePermissionAndRefresh(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	source, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "원본", OwnerID: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: source.Sheets[0].ID, ActorID: "bob", BaseVersion: source.Version, IdempotencyKey: "source-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`10`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`20`)},
	}}); err != nil {
		t.Fatal(err)
	}
	report, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "보고서", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := report.Sheets[0].ID
	formulaText := `=SUM(IMPORTRANGE("` + source.ID + `","Sheet1!A1:A2"))`
	applied, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: report.Version, IdempotencyKey: "import-1", Cells: []CellInput{{Row: 1, Column: 1, Formula: formulaText}}})
	if err != nil {
		t.Fatal(err)
	}
	read := func() Cell {
		t.Helper()
		cells, err := repository.ReadRange(ctx, sheet, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 1, Column: 1}})
		if err != nil || len(cells) != 1 {
			t.Fatalf("read = %v, %v", cells, err)
		}
		return cells[0]
	}
	// alice cannot see bob's workbook yet, so the formula has to say why.
	if value := string(read().Value); !strings.Contains(value, "#REF!") {
		t.Fatalf("unshared import = %s", value)
	}
	if len(applied.FormulaErrors) != 1 || !strings.Contains(applied.FormulaErrors[0].Message, "읽기 권한") {
		t.Fatalf("formula errors = %#v", applied.FormulaErrors)
	}
	if _, err := repository.PutWorkbookShare(ctx, source.ID, ShareInput{PrincipalType: "user", PrincipalID: "alice", Role: RoleViewer}); err != nil {
		t.Fatal(err)
	}
	// The next recalculation picks the data up: IMPORTRANGE is volatile, so an
	// unrelated edit is enough to refresh it.
	current, _ := repository.GetWorkbook(ctx, report.ID)
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: current.Version, IdempotencyKey: "import-2", Cells: []CellInput{{Row: 3, Column: 1, Value: json.RawMessage(`1`)}}}); err != nil {
		t.Fatal(err)
	}
	if value := string(read().Value); value != "30" {
		t.Fatalf("shared import = %s", value)
	}
	// A workbook may not import from itself: the sheet reference already does
	// that job and a self-import would recalculate against its own results.
	current, _ = repository.GetWorkbook(ctx, report.ID)
	self, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: current.Version, IdempotencyKey: "import-3", Cells: []CellInput{{Row: 5, Column: 1, Formula: `=IMPORTRANGE("` + report.ID + `","A1")`}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(self.FormulaErrors) != 1 || !strings.Contains(self.FormulaErrors[0].Message, "같은 워크북") {
		t.Fatalf("self import = %#v", self.FormulaErrors)
	}
}

func TestParseImportSourceAcceptsIdentifiersAndEditorLinks(t *testing.T) {
	cases := map[string]string{
		"18030fa8-1b6f-4ee3-b058-ce3ef17de258":                            "18030fa8-1b6f-4ee3-b058-ce3ef17de258",
		"https://kanpic.example.com/workbooks/abc-123":                    "abc-123",
		"https://kanpic.example.com/workbooks/abc-123/sheets/s1?range=A1": "abc-123",
	}
	for input, want := range cases {
		got, ok := parseImportSource(input)
		if !ok || got != want {
			t.Fatalf("parseImportSource(%q) = %q, %v", input, got, ok)
		}
	}
	if _, ok := parseImportSource("   "); ok {
		t.Fatal("blank source must be refused")
	}
	name, selected, ok := parseImportArea("'매출 원본'!B2:D4")
	if !ok || name != "매출 원본" || selected.Start.Column != 2 || selected.End.Row != 4 {
		t.Fatalf("parseImportArea = %q, %#v, %v", name, selected, ok)
	}
	if name, _, ok := parseImportArea("A1:B2"); !ok || name != "" {
		t.Fatalf("sheetless area = %q, %v", name, ok)
	}
}
