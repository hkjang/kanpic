package importexport

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"testing"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

func TestParseCSVPreservesIdentifiersAndDoesNotExecuteFormulas(t *testing.T) {
	t.Parallel()
	parsed, err := Parse("customers.csv", []byte("id,amount,note\n00123,42,=2+2\n"), DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Title != "customers" || parsed.Preview.TotalCells != 6 || len(parsed.Sheets) != 1 {
		t.Fatalf("parsed metadata: %#v", parsed.Preview)
	}
	cells := parsed.Sheets[0].Cells
	assertRawJSON(t, cells[3].Value, "00123")
	assertRawJSON(t, cells[4].Value, float64(42))
	assertRawJSON(t, cells[5].Value, "=2+2")
	if cells[5].Formula != "" {
		t.Fatalf("CSV formula text was executed: %#v", cells[5])
	}
}

func TestXLSXParseAndRoundTrip(t *testing.T) {
	t.Parallel()
	file := excelize.NewFile()
	defer file.Close()
	if err := file.SetSheetName("Sheet1", "매출"); err != nil {
		t.Fatal(err)
	}
	_ = file.SetCellValue("매출", "A1", "항목")
	_ = file.SetCellValue("매출", "A2", 10)
	_ = file.SetCellValue("매출", "A3", 20)
	_ = file.SetCellFormula("매출", "A4", "=SUM(A2:A3)")
	numberFormat := "#,##0.00"
	styleID, err := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"0F766E"}, Pattern: 1}, Alignment: &excelize.Alignment{Horizontal: "center", WrapText: true}, Border: []excelize.Border{{Type: "bottom", Color: "2563EB", Style: 2}}, CustomNumFmt: &numberFormat})
	if err != nil {
		t.Fatal(err)
	}
	_ = file.SetCellStyle("매출", "A1", "A1", styleID)
	_ = file.MergeCell("매출", "B1", "C1")
	buffer, err := file.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse("report.xlsx", buffer.Bytes(), DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sheets) != 1 || parsed.Preview.TotalCells != 6 || len(parsed.Preview.Warnings) != 0 {
		t.Fatalf("preview: %#v", parsed.Preview)
	}
	formula := findInput(parsed.Sheets[0].Cells, 4, 1)
	if formula.Formula != "=SUM(A2:A3)" {
		t.Fatalf("formula not preserved: %#v", formula)
	}
	header := findInput(parsed.Sheets[0].Cells, 1, 1)
	var headerStyle map[string]any
	if json.Unmarshal(header.Style, &headerStyle) != nil || headerStyle["bold"] != true || headerStyle["color"] != "#FFFFFF" || headerStyle["background"] != "#0F766E" || headerStyle["horizontal_align"] != "center" || headerStyle["text_mode"] != "wrap" || headerStyle["number_format"] != "#,##0.00" {
		t.Fatalf("canonical imported style: %s", header.Style)
	}
	for column := 2; column <= 3; column++ {
		input := findInput(parsed.Sheets[0].Cells, 1, column)
		metadata, exists, mergeErr := workbook.CellMerge(workbook.Cell{Row: input.Row, Column: input.Column, Style: input.Style})
		if mergeErr != nil || !exists || metadata.StartColumn != 2 || metadata.EndColumn != 3 {
			t.Fatalf("imported merge at column %d: metadata=%#v exists=%v err=%v", column, metadata, exists, mergeErr)
		}
	}

	repository := workbook.NewMemoryRepository()
	service := New(repository)
	ctx := context.Background()
	created, err := service.Import(ctx, ImportRequest{FileName: "report.xlsx", Data: buffer.Bytes(), ActorID: "tester", IdempotencyKey: "xlsx-1", MaxExpandedBytes: DefaultMaxExpandedBytes})
	if err != nil {
		t.Fatal(err)
	}
	calculated, err := repository.ReadRange(ctx, created.Sheets[0].ID, mustCellRange(t, "A4"))
	if err != nil || len(calculated) != 1 || string(calculated[0].Value) != "30" {
		t.Fatalf("imported formula was not server-calculated: cells=%#v err=%v", calculated, err)
	}
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: created.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := Parse(exported.Name, exported.Data, DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Sheets) != 1 || findInput(roundTrip.Sheets[0].Cells, 4, 1).Formula != "=SUM(A2:A3)" {
		t.Fatalf("round trip: %#v", roundTrip.Preview)
	}
	if _, exists, mergeErr := workbook.CellMerge(workbook.Cell{Row: 1, Column: 3, Style: findInput(roundTrip.Sheets[0].Cells, 1, 3).Style}); mergeErr != nil || !exists {
		t.Fatalf("merge was not preserved by XLSX round trip: exists=%v err=%v", exists, mergeErr)
	}
	roundTripHeader := findInput(roundTrip.Sheets[0].Cells, 1, 1)
	var roundTripStyle map[string]any
	if json.Unmarshal(roundTripHeader.Style, &roundTripStyle) != nil || roundTripStyle["bold"] != true || roundTripStyle["background"] != "#0F766E" || roundTripStyle["text_mode"] != "wrap" || roundTripStyle["number_format"] != "#,##0.00" {
		t.Fatalf("canonical round-trip style: %s", roundTripHeader.Style)
	}
}

func TestCanonicalStyleXLSXRoundTrip(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"bold":true,"italic":true,"underline":true,"strike":true,"font_family":"Arial","font_size":14,"color":"#FFFFFF","background":"#0F766E","horizontal_align":"center","vertical_align":"bottom","text_mode":"wrap","text_rotation":30,"number_format":"₩#,##0.00","borders":{"top":{"style":"thin","color":"#2563EB"},"right":{"style":"double","color":"#DC2626"}}}`)
	xlsx := xlsxStyle(raw)
	if xlsx == nil || xlsx.Font == nil || !xlsx.Font.Bold || xlsx.Font.Underline != "single" || xlsx.Fill.Pattern != 1 || xlsx.Alignment == nil || !xlsx.Alignment.WrapText || xlsx.CustomNumFmt == nil || len(xlsx.Border) != 2 {
		t.Fatalf("xlsx style: %#v", xlsx)
	}
	canonical := canonicalStyleFromXLSX(xlsx)
	var style map[string]any
	if err := json.Unmarshal(canonical, &style); err != nil {
		t.Fatal(err)
	}
	borders, _ := style["borders"].(map[string]any)
	if style["bold"] != true || style["background"] != "#0F766E" || style["vertical_align"] != "bottom" || style["text_mode"] != "wrap" || style["number_format"] != "₩#,##0.00" || len(borders) != 2 {
		t.Fatalf("canonical style: %s", canonical)
	}
}

func TestCSVExportEscapesFormulaInjection(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	stringValue, _ := json.Marshal("=cmd|' /C calc'!A0")
	numberValue, _ := json.Marshal(-12.0)
	wb, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{Title: "safe", ActorID: "tester", OwnerID: "tester", IdempotencyKey: "safe-1", FileName: "safe.csv", Format: "csv", Sheets: []workbook.ImportSheet{{Name: "Sheet1", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: stringValue}, {Row: 1, Column: 2, Value: numberValue}}}}})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := New(repository).Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(exported.Data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if records[0][0] != "'=cmd|' /C calc'!A0" {
		t.Fatalf("dangerous value was not escaped: %q", records[0][0])
	}
	if records[0][1] != "-12" {
		t.Fatalf("numeric value was incorrectly escaped: %q", records[0][1])
	}
}

func TestXLSXExportOmitsDynamicArrayChildValues(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "arrays", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	_, err = repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: 1, IdempotencyKey: "spill-export", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: value("a")}, {Row: 1, Column: 2, Value: value(30)},
		{Row: 2, Column: 1, Value: value("b")}, {Row: 2, Column: 2, Value: value(10)},
		{Row: 3, Column: 1, Value: value("c")}, {Row: 3, Column: 2, Value: value(20)},
		{Row: 1, Column: 4, Formula: "=FILTER(A1:B3,B1:B3>=20)"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := New(repository).Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenReader(bytes.NewReader(exported.Data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sheet := file.GetSheetName(0)
	if formula, _ := file.GetCellFormula(sheet, "D1"); formula != "=FILTER(A1:B3,B1:B3>=20)" {
		t.Fatalf("anchor formula = %q", formula)
	}
	for _, coordinate := range []string{"E1", "D2", "E2"} {
		if cellValue, _ := file.GetCellValue(sheet, coordinate); cellValue != "" {
			t.Fatalf("spill child %s was exported as a blocking value %q", coordinate, cellValue)
		}
	}
}

func assertRawJSON(t *testing.T, raw json.RawMessage, want any) {
	t.Helper()
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("value = %#v, want %#v", got, want)
	}
}

func findInput(cells []workbook.CellInput, row, column int) workbook.CellInput {
	for _, cell := range cells {
		if cell.Row == row && cell.Column == column {
			return cell
		}
	}
	return workbook.CellInput{}
}

func mustCellRange(t *testing.T, value string) cellrange.Range {
	t.Helper()
	selected, err := cellrange.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

// A sheet carries arrangement as well as data: what is hidden, how wide the
// columns are, where the panes are frozen, which rows fold away. Exporting the
// cells alone hands somebody a file that looks nothing like the sheet.
func TestXLSXExportCarriesSheetLayout(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "layout", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: 1, IdempotencyKey: "layout-cells", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: value("머리글")},
		{Row: 2, Column: 1, Value: value("숨긴 행")},
		{Row: 3, Column: 1, Value: value("보이는 행")},
	}}); err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	apply := func(key string, mutation workbook.SheetLayoutMutation) {
		t.Helper()
		mutation.SheetID, mutation.ActorID, mutation.IdempotencyKey, mutation.ExpectedRevision = sheetID, "tester", key, revision
		result, err := repository.ApplySheetLayout(ctx, mutation)
		if err != nil {
			t.Fatal(err)
		}
		revision = result.Layout.Revision
	}
	apply("hide", workbook.SheetLayoutMutation{Action: "hide", Axis: "row", Start: 2, Count: 1})
	apply("width", workbook.SheetLayoutMutation{Action: "resize", Axis: "column", Start: 1, Count: 1, Size: 208})
	apply("height", workbook.SheetLayoutMutation{Action: "resize", Axis: "row", Start: 3, Count: 1, Size: 48})
	apply("freeze", workbook.SheetLayoutMutation{Action: "freeze", FrozenRows: 1, FrozenColumns: 0})
	apply("group", workbook.SheetLayoutMutation{Action: "group", Axis: "row", Start: 2, Count: 2})

	exported, err := New(repository).Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenReader(bytes.NewReader(exported.Data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sheet := file.GetSheetName(0)

	if visible, err := file.GetRowVisible(sheet, 2); err != nil || visible {
		t.Fatalf("hidden row exported as visible: %v, %v", visible, err)
	}
	if visible, err := file.GetRowVisible(sheet, 3); err != nil || !visible {
		t.Fatalf("visible row exported as hidden: %v, %v", visible, err)
	}
	// 208px is about 29 characters at the default font, and 48px is 36 points.
	if width, err := file.GetColWidth(sheet, "A"); err != nil || width < 28 || width > 30 {
		t.Fatalf("column width = %v, %v", width, err)
	}
	if height, err := file.GetRowHeight(sheet, 3); err != nil || height < 35 || height > 37 {
		t.Fatalf("row height = %v, %v", height, err)
	}
	if level, err := file.GetRowOutlineLevel(sheet, 2); err != nil || level != 1 {
		t.Fatalf("outline level = %v, %v", level, err)
	}
	if level, err := file.GetRowOutlineLevel(sheet, 1); err != nil || level != 0 {
		t.Fatalf("header row should not be part of the group: %v, %v", level, err)
	}
	panes, err := file.GetPanes(sheet)
	if err != nil || !panes.Freeze || panes.YSplit != 1 {
		t.Fatalf("frozen panes = %#v, %v", panes, err)
	}
}

// Exporting the arrangement and then ignoring it on the way back in would mean
// a kanpic workbook loses its layout by making a round trip through its own
// file format.
func TestXLSXLayoutSurvivesAnExportImportRoundTrip(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "round trip", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: 1, IdempotencyKey: "rt-cells", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: value("머리글")}, {Row: 1, Column: 2, Value: value("값")},
		{Row: 2, Column: 1, Value: value("숨긴 행")}, {Row: 2, Column: 2, Value: value(1)},
		{Row: 3, Column: 1, Value: value("보이는 행")}, {Row: 3, Column: 2, Value: value(2)},
	}}); err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	apply := func(key string, mutation workbook.SheetLayoutMutation) {
		t.Helper()
		mutation.SheetID, mutation.ActorID, mutation.IdempotencyKey, mutation.ExpectedRevision = sheetID, "tester", key, revision
		result, err := repository.ApplySheetLayout(ctx, mutation)
		if err != nil {
			t.Fatal(err)
		}
		revision = result.Layout.Revision
	}
	apply("hide", workbook.SheetLayoutMutation{Action: "hide", Axis: "row", Start: 2, Count: 1})
	apply("freeze", workbook.SheetLayoutMutation{Action: "freeze", FrozenRows: 1, FrozenColumns: 1})
	apply("width", workbook.SheetLayoutMutation{Action: "resize", Axis: "column", Start: 2, Count: 1, Size: 208})

	service := New(repository)
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(exported.Name, exported.Data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sheets) != 1 || parsed.Sheets[0].Layout == nil {
		t.Fatalf("imported sheets = %#v", parsed.Sheets)
	}
	layout := *parsed.Sheets[0].Layout
	if len(layout.HiddenRows) != 1 || layout.HiddenRows[0].Start != 2 || layout.HiddenRows[0].End != 2 {
		t.Fatalf("hidden rows = %#v", layout.HiddenRows)
	}
	if layout.FrozenRows != 1 || layout.FrozenColumns != 1 {
		t.Fatalf("frozen panes = %d rows, %d columns", layout.FrozenRows, layout.FrozenColumns)
	}
	var width float64
	for _, item := range layout.ColumnWidths {
		if item.Index == 2 {
			width = item.Size
		}
	}
	if width < 200 || width > 216 {
		t.Fatalf("column width came back as %v pixels", width)
	}

	// The layout has to reach the stored sheet, not just the parse result.
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "default", Title: "restored", OwnerID: "tester", ActorID: "tester",
		IdempotencyKey: "rt-import", Sheets: parsed.Sheets,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored := restored.Sheets[0].Layout
	if len(stored.HiddenRows) != 1 || stored.FrozenRows != 1 || stored.FrozenColumns != 1 {
		t.Fatalf("stored layout = %#v", stored)
	}
}
