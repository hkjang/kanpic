package workbook

import (
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestBuildMergeCellsPreservesContentAndFormatting(t *testing.T) {
	selected, _ := cellrange.Parse("A1:B2")
	existing := []Cell{
		{Row: 1, Column: 1, Value: json.RawMessage(`"title"`), Style: json.RawMessage(`{"bold":true}`)},
		{Row: 2, Column: 2, Formula: "=1+1", Value: json.RawMessage(`2`), Style: json.RawMessage(`{"background":"#ffffff"}`)},
	}
	merged, err := BuildMergeCells(existing, selected, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 4 || string(merged[0].Value) != `"title"` || merged[3].Formula != "=1+1" {
		t.Fatalf("merged inputs: %#v", merged)
	}
	for _, input := range merged {
		metadata, exists, err := CellMerge(Cell{Row: input.Row, Column: input.Column, Style: input.Style})
		if err != nil || !exists || metadata != (MergeMetadata{StartRow: 1, StartColumn: 1, EndRow: 2, EndColumn: 2}) {
			t.Fatalf("cell %d:%d metadata=%#v exists=%v err=%v", input.Row, input.Column, metadata, exists, err)
		}
	}
	unmerged, err := BuildMergeCells(inputsAsCells(merged), selected, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range unmerged {
		_, exists, err := CellMerge(Cell{Row: input.Row, Column: input.Column, Style: input.Style})
		if err != nil || exists {
			t.Fatalf("unmerged cell %d:%d style=%s err=%v", input.Row, input.Column, input.Style, err)
		}
	}
	if string(unmerged[0].Style) != `{"bold":true}` || string(unmerged[3].Style) != `{"background":"#ffffff"}` {
		t.Fatalf("ordinary styles were not preserved: %#v", unmerged)
	}
}

func TestBuildMergeCellsRejectsSingletonAndOverlap(t *testing.T) {
	single, _ := cellrange.Parse("A1")
	if _, err := BuildMergeCells(nil, single, true); err == nil {
		t.Fatal("expected singleton merge rejection")
	}
	first, _ := cellrange.Parse("A1:B2")
	merged, err := BuildMergeCells(nil, first, true)
	if err != nil {
		t.Fatal(err)
	}
	overlap, _ := cellrange.Parse("B2:C3")
	if _, err := BuildMergeCells(inputsAsCells(merged), overlap, true); err == nil {
		t.Fatal("expected overlapping merge rejection")
	}
}

func inputsAsCells(inputs []CellInput) []Cell {
	result := make([]Cell, len(inputs))
	for index, input := range inputs {
		result[index] = Cell{Row: input.Row, Column: input.Column, Value: input.Value, Formula: input.Formula, Style: input.Style}
	}
	return result
}
