package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestValidateStylePatch(t *testing.T) {
	t.Parallel()
	valid := []string{
		`{"bold":true,"italic":false,"underline":true}`,
		`{"color":"#123aBC","background":null,"font_size":14}`,
		`{"font_family":"Arial","horizontal_align":"center","vertical_align":"middle","number_format":"#,##0.00"}`,
		`{"text_mode":"wrap","borders":{"top":{"style":"thin","color":"#123abc"},"bottom":null}}`,
	}
	for _, patch := range valid {
		if err := ValidateStylePatch(json.RawMessage(patch)); err != nil {
			t.Errorf("valid patch %s: %v", patch, err)
		}
	}
	invalid := []string{`{}`, `null`, `{"bold":"yes"}`, `{"color":"red"}`, `{"font_size":100}`, `{"horizontal_align":"justify"}`, `{"text_mode":"truncate"}`, `{"borders":{}}`, `{"borders":{"diagonal":{"style":"thin","color":"#000000"}}}`, `{"borders":{"top":{"style":"hair","color":"#000000"}}}`, `{"unknown":true}`}
	for _, patch := range invalid {
		if err := ValidateStylePatch(json.RawMessage(patch)); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid patch %s: %v", patch, err)
		}
	}
}

func TestValidateCompleteCellStyleIncludingMergeMetadata(t *testing.T) {
	t.Parallel()
	valid := CellInput{Row: 2, Column: 2, Style: json.RawMessage(`{"bold":true,"borders":{"right":{"style":"double","color":"#123456"}},"merge":{"start_row":1,"start_column":1,"end_row":2,"end_column":2}}`)}
	if err := ValidateCellStyle(valid); err != nil {
		t.Fatalf("valid complete style: %v", err)
	}
	invalid := []CellInput{
		{Row: 1, Column: 1, Style: json.RawMessage(`{"unknown":true}`)},
		{Row: 3, Column: 3, Style: json.RawMessage(`{"merge":{"start_row":1,"start_column":1,"end_row":2,"end_column":2}}`)},
	}
	for _, input := range invalid {
		if err := ValidateCellStyle(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid complete style %s: %v", input.Style, err)
		}
	}
}

func TestBorderCommandMaterializesOuterAndInnerBorders(t *testing.T) {
	t.Parallel()
	outer := BorderCommand{Preset: "outer", Style: "medium", Color: "#2563eb", StartRow: 2, StartColumn: 2, EndRow: 3, EndColumn: 3}
	corner, err := applyBorderCommand(json.RawMessage(`{"borders":{"left":{"style":"thin","color":"#111111"}}}`), outer, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	var style struct {
		Borders map[string]BorderSide `json:"borders"`
	}
	if err := json.Unmarshal(corner, &style); err != nil {
		t.Fatal(err)
	}
	if style.Borders["top"].Style != "medium" || style.Borders["left"].Color != "#2563eb" {
		t.Fatalf("outer corner: %#v", style.Borders)
	}
	center, err := applyBorderCommand(nil, outer, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	style.Borders = nil
	if err := json.Unmarshal(center, &style); err != nil || style.Borders["left"].Style != "medium" || style.Borders["bottom"].Style != "medium" || len(style.Borders) != 2 {
		t.Fatalf("outer lower-left: %s %#v %v", center, style.Borders, err)
	}
	inner := BorderCommand{Preset: "inner", Style: "dotted", Color: "#dc2626", StartRow: 2, StartColumn: 2, EndRow: 3, EndColumn: 3}
	inside, err := applyBorderCommand(nil, inner, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	style.Borders = nil
	if err := json.Unmarshal(inside, &style); err != nil || style.Borders["top"].Style != "dotted" || style.Borders["left"].Style != "dotted" || len(style.Borders) != 2 {
		t.Fatalf("inner: %s %#v %v", inside, style.Borders, err)
	}
}

func TestMergeStylePatchPreservesAndRemovesProperties(t *testing.T) {
	t.Parallel()
	merged, err := mergeStylePatch(json.RawMessage(`{"bold":true,"background":"#ffffff","font_size":12}`), json.RawMessage(`{"background":null,"italic":true,"font_size":14}`))
	if err != nil {
		t.Fatal(err)
	}
	var style map[string]any
	if err := json.Unmarshal(merged, &style); err != nil {
		t.Fatal(err)
	}
	if style["bold"] != true || style["italic"] != true || style["font_size"] != float64(14) {
		t.Fatalf("merged style: %#v", style)
	}
	if _, exists := style["background"]; exists {
		t.Fatalf("removed background survived: %#v", style)
	}
}

func TestMemoryStylePatchPreservesContentAndSupportsUndo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "formatting"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "owner", BaseVersion: 1, IdempotencyKey: "format-seed", Cells: []CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`5`)}, {Row: 2, Column: 1, Formula: "=A1*2"}}}); err != nil {
		t.Fatal(err)
	}
	formatted, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "owner", BaseVersion: 2, IdempotencyKey: "format-range", OperationType: "range.format", StylePatch: json.RawMessage(`{"bold":true,"background":"#fef3c7","horizontal_align":"center"}`), Cells: []CellInput{{Row: 1, Column: 1}, {Row: 2, Column: 1}, {Row: 3, Column: 1}}})
	if err != nil || formatted.ServerVersion != 3 || formatted.AppliedCells != 3 || len(formatted.RecalculatedCells) != 0 {
		t.Fatalf("format result: %#v, %v", formatted, err)
	}
	selected, _ := cellrange.Parse("A1:A3")
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 3 || string(cells[0].Value) != "5" || cells[1].Formula != "=A1*2" || string(cells[1].Value) != "10" || len(cells[2].Value) != 0 {
		t.Fatalf("formatted cells: %#v, %v", cells, err)
	}
	for _, cell := range cells {
		var style map[string]any
		if json.Unmarshal(cell.Style, &style) != nil || style["bold"] != true || style["background"] != "#fef3c7" || style["horizontal_align"] != "center" {
			t.Fatalf("cell style: %s", cell.Style)
		}
	}
	noChange, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "owner", BaseVersion: 3, IdempotencyKey: "same-format", OperationType: "range.format", StylePatch: json.RawMessage(`{"bold":true}`), Cells: []CellInput{{Row: 1, Column: 1}}})
	if err != nil || noChange.ServerVersion != 3 || noChange.AppliedCells != 0 {
		t.Fatalf("no-op format: %#v, %v", noChange, err)
	}
	undone, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: formatted.OperationID, ActorID: "owner", IdempotencyKey: "undo-format"})
	if err != nil || undone.ServerVersion != 4 || undone.AppliedCells != 3 {
		t.Fatalf("undo format: %#v, %v", undone, err)
	}
	cells, _ = repository.ReadRange(ctx, sheetID, selected)
	if len(cells) != 2 || len(cells[0].Style) != 0 || len(cells[1].Style) != 0 || string(cells[0].Value) != "5" || cells[1].Formula != "=A1*2" || string(cells[1].Value) != "10" {
		t.Fatalf("cells after format undo: %#v", cells)
	}
}
