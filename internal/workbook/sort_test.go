package workbook

import (
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestBuildSortCellsUsesStableMultiKeyAndMovesFormulasAndStyles(t *testing.T) {
	selected, _ := cellrange.Parse("A1:C4")
	existing := []Cell{
		{Row: 1, Column: 1, Value: json.RawMessage(`"Name"`)},
		{Row: 1, Column: 2, Value: json.RawMessage(`"Quantity"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"beta"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`2`)}, {Row: 2, Column: 3, Value: json.RawMessage(`20`), Formula: "=B2*10", Style: json.RawMessage(`{"background":"#fff000"}`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"Alpha"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`10`)}, {Row: 3, Column: 3, Value: json.RawMessage(`100`), Formula: "=B3*10", Style: json.RawMessage(`{"bold":true}`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"alpha"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`5`)}, {Row: 4, Column: 3, Value: json.RawMessage(`50`), Formula: "=B4*10"},
	}
	inputs, err := BuildSortCells(existing, selected, SortOptions{HeaderRows: 1, Keys: []SortKey{{Column: 1, Direction: "asc"}, {Column: 2, Direction: "desc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 9 {
		t.Fatalf("sorted inputs = %d", len(inputs))
	}
	if string(inputs[0].Value) != `"Alpha"` || string(inputs[3].Value) != `"alpha"` || string(inputs[6].Value) != `"beta"` {
		t.Fatalf("row order: %#v", inputs)
	}
	if inputs[2].Formula != "=B2*10" || inputs[5].Formula != "=B3*10" || inputs[8].Formula != "=B4*10" {
		t.Fatalf("shifted formulas: %q %q %q", inputs[2].Formula, inputs[5].Formula, inputs[8].Formula)
	}
	if string(inputs[2].Style) != `{"bold":true}` || string(inputs[8].Style) != `{"background":"#fff000"}` {
		t.Fatalf("styles did not move with rows: %#v", inputs)
	}
}

func TestBuildSortCellsKeepsBlanksLastForDescendingSort(t *testing.T) {
	selected, _ := cellrange.Parse("A1:A4")
	inputs, err := BuildSortCells([]Cell{{Row: 1, Column: 1, Value: json.RawMessage(`2`)}, {Row: 3, Column: 1, Value: json.RawMessage(`9`)}, {Row: 4, Column: 1, Value: json.RawMessage(`1`)}}, selected, SortOptions{Keys: []SortKey{{Column: 1, Direction: "desc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(inputs[0].Value) != "9" || string(inputs[1].Value) != "2" || string(inputs[2].Value) != "1" || len(inputs[3].Value) != 0 {
		t.Fatalf("descending order with blank: %#v", inputs)
	}
}

func TestBuildSortCellsRejectsInvalidKeysMergedCellsAndOversizedRange(t *testing.T) {
	selected, _ := cellrange.Parse("A1:B3")
	if _, err := BuildSortCells(nil, selected, SortOptions{Keys: []SortKey{{Column: 3, Direction: "asc"}}}); err == nil {
		t.Fatal("expected outside key rejection")
	}
	merged, err := BuildMergeCells(nil, selected, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSortCells(inputsAsCells(merged), selected, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}}); err == nil {
		t.Fatal("expected merged range rejection")
	}
	if _, err := BuildSortCells([]Cell{{Row: 2, Column: 1, Value: json.RawMessage(`2`), SpillSource: "A1"}}, selected, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}}); err == nil {
		t.Fatal("expected array result sort rejection")
	}
	large, _ := cellrange.Parse("A1:B5001")
	if _, err := BuildSortCells(nil, large, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}}); err == nil {
		t.Fatal("expected operation limit rejection")
	}
}
