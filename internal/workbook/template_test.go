package workbook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

func TestTemplateCatalogIsWellFormed(t *testing.T) {
	t.Parallel()
	catalog := TemplateCatalog()
	if len(catalog) < 30 {
		t.Fatalf("catalog has %d templates, want at least 30", len(catalog))
	}
	known := map[string]struct{}{}
	for _, doc := range formula.Catalog() {
		known[doc.Name] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, summary := range catalog {
		if summary.ID == "" || summary.Name == "" || summary.Category == "" || summary.Summary == "" {
			t.Errorf("incomplete catalog entry %#v", summary)
		}
		if _, duplicate := seen[summary.ID]; duplicate {
			t.Errorf("duplicate template id %q", summary.ID)
		}
		seen[summary.ID] = struct{}{}

		template, ok := TemplateByID(summary.ID)
		if !ok {
			t.Fatalf("%s cannot be loaded", summary.ID)
		}
		for _, sheet := range template.Sheets {
			if sheet.Name == "" || len(sheet.Cells) == 0 {
				t.Errorf("%s has an empty sheet", template.ID)
			}
			if len(sheet.Widths) > 0 && len(sheet.Widths) < len(template.Columns) {
				t.Errorf("%s does not size every column", template.ID)
			}
			formulas := 0
			for _, cell := range sheet.Cells {
				if cell.Row < 1 || cell.Column < 1 {
					t.Errorf("%s has a cell outside the grid: %#v", template.ID, cell)
				}
				if cell.Formula == "" {
					continue
				}
				formulas++
				if strings.Contains(cell.Formula, "{") {
					t.Errorf("%s left an unexpanded placeholder: %s", template.ID, cell.Formula)
				}
				for _, name := range formulaNames(cell.Formula) {
					if _, ok := known[name]; !ok {
						t.Errorf("%s uses the unsupported function %s", template.ID, name)
					}
				}
			}
			if formulas == 0 {
				t.Errorf("%s sheet %q has no formula, so it is only a table of text", template.ID, sheet.Name)
			}
		}
	}
}

// formulaNames pulls the function names out of a formula so the test can check
// them against what the engine actually implements.
func formulaNames(text string) []string {
	names := make([]string, 0, 4)
	start := -1
	for index, letter := range text {
		isName := letter >= 'A' && letter <= 'Z'
		switch {
		case isName && start < 0:
			start = index
		case !isName && start >= 0:
			if letter == '(' {
				names = append(names, text[start:index])
			}
			start = -1
		}
	}
	return names
}

func TestTemplateFormulasCalculateWhenApplied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, summary := range TemplateCatalog() {
		template, _ := TemplateByID(summary.ID)
		repository := NewMemoryRepository()
		created, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: template.Name, WorkspaceID: "default", OwnerID: "owner"})
		if err != nil {
			t.Fatalf("%s: create workbook: %v", template.ID, err)
		}
		if err := ApplyTemplate(ctx, repository, created, template, "owner"); err != nil {
			t.Fatalf("%s: apply template: %v", template.ID, err)
		}
		filled, err := repository.GetWorkbook(ctx, created.ID)
		if err != nil {
			t.Fatalf("%s: read workbook: %v", template.ID, err)
		}
		if len(filled.Sheets) != len(template.Sheets) {
			t.Fatalf("%s: %d sheets, want %d", template.ID, len(filled.Sheets), len(template.Sheets))
		}
		for index, sheet := range filled.Sheets {
			if sheet.Name != template.Sheets[index].Name {
				t.Errorf("%s: sheet %d is %q, want %q", template.ID, index, sheet.Name, template.Sheets[index].Name)
			}
			if sheet.Layout.FrozenRows != template.Sheets[index].FrozenRows {
				t.Errorf("%s: frozen rows %d, want %d", template.ID, sheet.Layout.FrozenRows, template.Sheets[index].FrozenRows)
			}
			cells, err := repository.ReadRange(ctx, sheet.ID, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 60, Column: 20}})
			if err != nil {
				t.Fatalf("%s: read range: %v", template.ID, err)
			}
			for _, cell := range cells {
				if cell.Formula == "" {
					continue
				}
				// A formula cell must hold a calculated value, never an error
				// code, so a new workbook never opens showing #NAME? or #REF!.
				var value any
				if len(cell.Value) > 0 {
					_ = json.Unmarshal(cell.Value, &value)
				}
				text, _ := value.(string)
				if strings.HasPrefix(text, "#") {
					t.Errorf("%s: %s returned %s for %s", template.ID, sheet.Name, text, cell.Formula)
				}
			}
		}
	}
}

// One template is checked against numbers worked out by hand, so the harness
// itself cannot silently stop calculating.
func TestInvoiceTemplateTotalsAreCalculated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	template, ok := TemplateByID("invoice")
	if !ok {
		t.Fatal("the invoice template is missing")
	}
	repository := NewMemoryRepository()
	created, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: template.Name, WorkspaceID: "default", OwnerID: "owner"})
	if err != nil {
		t.Fatalf("create workbook: %v", err)
	}
	if err := ApplyTemplate(ctx, repository, created, template, "owner"); err != nil {
		t.Fatalf("apply template: %v", err)
	}
	filled, _ := repository.GetWorkbook(ctx, created.ID)
	cells, err := repository.ReadRange(ctx, filled.Sheets[0].ID, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 20, Column: 8}})
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	values := map[string]float64{}
	for _, cell := range cells {
		var value any
		if len(cell.Value) > 0 {
			_ = json.Unmarshal(cell.Value, &value)
		}
		if number, isNumber := value.(float64); isNumber {
			values[cellAddressOf(cell.Row, cell.Column)] = number
		}
	}
	// 1 × 18,000,000 plus 10% VAT is the first line, and the total row adds all
	// four lines together.
	for address, want := range map[string]float64{"E4": 18_000_000, "F4": 1_800_000, "G4": 19_800_000, "G8": 39_490_000} {
		if got := values[address]; got != want {
			t.Errorf("%s = %v, want %v", address, got, want)
		}
	}
}

func cellAddressOf(row, column int) string {
	letters := ""
	for value := column; value > 0; value = (value - 1) / 26 {
		letters = string(rune('A'+(value-1)%26)) + letters
	}
	return letters + itoa(row)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
