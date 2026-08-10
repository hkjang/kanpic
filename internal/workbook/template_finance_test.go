package workbook

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"kanpic/pkg/cellrange"
)

// The finance templates are only useful if their arithmetic is right, so a few
// results are checked against figures worked out independently.
func TestFinanceTemplatesCalculateCorrectValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		template string
		expected map[string]float64
	}{
		// 300,000,000 at 4.5% over 120 months: the standard annuity payment,
		// then the first month's interest and the remaining balance.
		{"mortgage-schedule", map[string]float64{"C4": 3_109_152, "D4": 1_125_000, "F4": 298_015_848}},
		// 300M at 4.1% over 360 months, and the same amount at 3.8% over 240.
		{"loan-comparison", map[string]float64{"E4": 1_449_595, "E5": 1_786_481, "G5": 128_755_440}},
		// A 9% discount rate gives 1/1.09 and 1/1.09^5 as the year factors.
		{"dcf-valuation", map[string]float64{"D4": 0.9174, "D8": 0.6499, "E4": 11_008_800_000}},
		// Weighted scorecard: 0.35*92 + 0.25*74 + 0.15*88 + 0.10*65 + 0.15*80.
		{"credit-scorecard", map[string]float64{"D9": 82.4}},
		// Rent 1,900,000 a month against a 520,000,000 purchase is 4.38%.
		{"rental-yield", map[string]float64{"B12": 22_800_000, "B14": 20_400_000, "B16": 6_000_000}},
	}
	ctx := context.Background()
	for _, testCase := range cases {
		template, ok := TemplateByID(testCase.template)
		if !ok {
			t.Errorf("%s is missing", testCase.template)
			continue
		}
		repository := NewMemoryRepository()
		created, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: template.Name, WorkspaceID: "default", OwnerID: "owner"})
		if err != nil {
			t.Fatalf("%s: create: %v", testCase.template, err)
		}
		if err := ApplyTemplate(ctx, repository, created, template, "owner"); err != nil {
			t.Fatalf("%s: apply: %v", testCase.template, err)
		}
		filled, _ := repository.GetWorkbook(ctx, created.ID)
		cells, err := repository.ReadRange(ctx, filled.Sheets[0].ID, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 40, Column: 12}})
		if err != nil {
			t.Fatalf("%s: read: %v", testCase.template, err)
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
		for address, want := range testCase.expected {
			got, found := values[address]
			if !found {
				t.Errorf("%s: %s has no numeric value", testCase.template, address)
				continue
			}
			if math.Abs(got-want) > math.Max(1, math.Abs(want)*0.001) {
				t.Errorf("%s: %s = %.2f, want %.2f", testCase.template, address, got, want)
			}
		}
	}
}
