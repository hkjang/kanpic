package importexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"
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

// A workbook's input rules are part of how it behaves. A file whose dropdowns
// are gone looks identical and accepts anything.
func TestXLSXValidationsSurviveAnExportImportRoundTrip(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "validations", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	raw := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.CreateDataValidation(ctx, sheetID, "tester", workbook.CreateDataValidationInput{
		IdempotencyKey: "list", Range: "A1:A10", RuleType: "list",
		Options: []workbook.ValidationOption{{Value: raw("승인")}, {Value: raw("대기")}, {Value: raw("거절")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateDataValidation(ctx, sheetID, "tester", workbook.CreateDataValidationInput{
		IdempotencyKey: "number", Range: "B1:B10", RuleType: "number", Operator: "greater_or_equal", Value: raw(0),
		HelpText: "0 이상만 입력하세요.",
	}); err != nil {
		t.Fatal(err)
	}

	service := New(repository)
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(exported.Name, exported.Data, 0)
	if err != nil {
		t.Fatal(err)
	}
	rules := parsed.Sheets[0].Validations
	if len(rules) != 2 {
		t.Fatalf("imported validations = %#v", rules)
	}
	byRange := make(map[string]workbook.ImportValidation, len(rules))
	for _, rule := range rules {
		byRange[rule.Range] = rule
	}
	list, found := byRange["A1:A10"]
	if !found || list.RuleType != "list" || len(list.Options) != 3 || list.Options[0] != "승인" {
		t.Fatalf("list rule = %#v", list)
	}
	number, found := byRange["B1:B10"]
	if !found || number.RuleType != "number" || number.Operator != "greater_or_equal" || number.Value != "0" {
		t.Fatalf("number rule = %#v", number)
	}

	// The rules have to reach the stored sheet, not just the parse result.
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "default", Title: "restored", OwnerID: "tester", ActorID: "tester",
		IdempotencyKey: "validation-import", Sheets: parsed.Sheets,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.ListDataValidations(ctx, restored.Sheets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored validations = %#v", stored)
	}
	for _, rule := range stored {
		if rule.Range == "A1:A10" && (rule.RuleType != "list" || len(rule.Options) != 3) {
			t.Fatalf("stored list rule = %#v", rule)
		}
		if rule.Range == "B1:B10" && (rule.RuleType != "number" || rule.Operator != "greater_or_equal") {
			t.Fatalf("stored number rule = %#v", rule)
		}
	}
}

// Conditional formats are how a sheet says which numbers matter. Exporting the
// values without them hands somebody a table where nothing stands out.
func TestXLSXExportCarriesConditionalFormats(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "conditional", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	raw := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	create := func(key string, input workbook.CreateConditionalFormatInput) {
		t.Helper()
		input.IdempotencyKey = key
		if _, err := repository.CreateConditionalFormat(ctx, sheetID, "tester", input); err != nil {
			t.Fatal(err)
		}
	}
	create("value", workbook.CreateConditionalFormatInput{Range: "A1:A20", RuleType: "value", Operator: "greater_than", Value: raw(100), Style: raw(map[string]any{"background": "#fee2e2"})})
	create("scale", workbook.CreateConditionalFormatInput{Range: "B1:B20", RuleType: "color_scale", MinColor: "#dcfce7", MaxColor: "#ef4444"})
	create("bar", workbook.CreateConditionalFormatInput{Range: "C1:C20", RuleType: "data_bar", BarColor: "#38a3a5"})
	create("custom", workbook.CreateConditionalFormatInput{Range: "D1:D20", RuleType: "custom_formula", Formula: `=$A1>100`, Style: raw(map[string]any{"background": "#dbeafe"})})
	create("icons", workbook.CreateConditionalFormatInput{Range: "E1:E20", RuleType: "icon_set", IconStyle: "3Arrows", IconReverse: true})

	exported, err := New(repository).Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenReader(bytes.NewReader(exported.Data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	formats, err := file.GetConditionalFormats(file.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]string, len(formats))
	for area, options := range formats {
		if len(options) != 1 {
			t.Fatalf("range %s carried %d rules", area, len(options))
		}
		kinds[area] = options[0].Type
	}
	expected := map[string]string{"A1:A20": "cell", "B1:B20": "2_color_scale", "C1:C20": "data_bar", "D1:D20": "formula", "E1:E20": "icon_set"}
	for area, kind := range expected {
		if kinds[area] != kind {
			t.Fatalf("range %s exported as %q, want %q (all: %#v)", area, kinds[area], kind, kinds)
		}
	}
	// The comparison itself has to survive, not just the rule type.
	for area, options := range formats {
		if area == "A1:A20" && (options[0].Criteria != "greater than" || options[0].Value != "100") {
			t.Fatalf("cell rule = %#v", options[0])
		}
		if area == "D1:D20" && options[0].Criteria != "$A1>100" {
			t.Fatalf("formula rule = %#v", options[0])
		}
		if area == "E1:E20" && (options[0].IconStyle != "3Arrows" || !options[0].ReverseIcons) {
			t.Fatalf("icon set rule = %#v", options[0])
		}
	}

	// Reading the file back has to produce the same rules, and they have to
	// reach the stored sheet rather than stopping at the parse result.
	parsed, err := Parse(exported.Name, exported.Data, 0)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "default", Title: "restored", OwnerID: "tester", ActorID: "tester",
		IdempotencyKey: "conditional-import", Sheets: parsed.Sheets,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.ListConditionalFormats(ctx, restored.Sheets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 5 {
		t.Fatalf("stored conditional formats = %#v", stored)
	}
	byArea := make(map[string]workbook.ConditionalFormat, len(stored))
	for _, rule := range stored {
		byArea[rule.Range] = rule
	}
	if rule := byArea["A1:A20"]; rule.RuleType != "value" || rule.Operator != "greater_than" || string(rule.Value) != "100" {
		t.Fatalf("restored value rule = %#v", rule)
	}
	if rule := byArea["B1:B20"]; rule.RuleType != "color_scale" || rule.MinColor == "" || rule.MaxColor == "" {
		t.Fatalf("restored colour scale = %#v", rule)
	}
	if rule := byArea["C1:C20"]; rule.RuleType != "data_bar" || rule.BarColor == "" {
		t.Fatalf("restored data bar = %#v", rule)
	}
	if rule := byArea["D1:D20"]; rule.RuleType != "custom_formula" || rule.Formula != "=$A1>100" {
		t.Fatalf("restored custom formula = %#v", rule)
	}
	if rule := byArea["E1:E20"]; rule.RuleType != "icon_set" || rule.IconStyle != "3Arrows" || !rule.IconReverse {
		t.Fatalf("restored icon set = %#v", rule)
	}
}

// A workbook's charts are half of what people look at. Exporting the numbers
// without them hands somebody a file where the report is missing.
func TestXLSXExportCarriesCharts(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "charts", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	raw := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: 1, IdempotencyKey: "chart-cells", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: raw("분기")}, {Row: 1, Column: 2, Value: raw("매출")},
		{Row: 2, Column: 1, Value: raw("Q1")}, {Row: 2, Column: 2, Value: raw(120)},
		{Row: 3, Column: 1, Value: raw("Q2")}, {Row: 3, Column: 2, Value: raw(150)},
	}}); err != nil {
		t.Fatal(err)
	}
	yes := true
	if _, err := repository.CreateChart(ctx, wb.ID, "tester", workbook.CreateChartInput{
		IdempotencyKey: "chart", SheetID: sheetID, SourceSheetID: sheetID, Type: "bar", Title: "분기 매출",
		SourceRange: "A1:B3", FirstRowHeaders: &yes, FirstColumnLabels: &yes, LegendPosition: "bottom",
		Position: &workbook.ChartPosition{X: 432, Y: 108, Width: 480, Height: 300},
	}); err != nil {
		t.Fatal(err)
	}

	exported, err := New(repository).Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	// excelize can write charts but not read them back, so the file itself is
	// inspected: the chart part has to exist and name the source cells.
	archive, err := zip.NewReader(bytes.NewReader(exported.Data), int64(len(exported.Data)))
	if err != nil {
		t.Fatal(err)
	}
	chartXML := ""
	for _, entry := range archive.File {
		if entry.Name != "xl/charts/chart1.xml" {
			continue
		}
		handle, openErr := entry.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		content, readErr := io.ReadAll(handle)
		handle.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		chartXML = string(content)
	}
	if chartXML == "" {
		names := make([]string, 0, len(archive.File))
		for _, entry := range archive.File {
			names = append(names, entry.Name)
		}
		t.Fatalf("no chart part in the exported file: %v", names)
	}
	if !strings.Contains(chartXML, "barChart") {
		t.Fatalf("chart is not a bar chart: %s", chartXML[:min(400, len(chartXML))])
	}
	// The series has to point at the source cells, not at nothing.
	if !strings.Contains(chartXML, "$B$2:$B$3") {
		t.Fatalf("series values missing from chart XML")
	}
	if !strings.Contains(chartXML, "$A$2:$A$3") {
		t.Fatalf("series categories missing from chart XML")
	}
	if !strings.Contains(chartXML, "분기 매출") {
		t.Fatalf("chart title missing from chart XML")
	}
}

// XLSX leaves the type attribute off numbers, and the row reader hands back
// the formatted text. Reading either one carelessly turns a price list into
// words: the column looks right and SUM over it answers zero.
func TestXLSXImportKeepsNumbersNumeric(t *testing.T) {
	t.Parallel()
	file := excelize.NewFile()
	defer file.Close()
	currency := "#,##0원"
	styleID, err := file.NewStyle(&excelize.Style{CustomNumFmt: &currency})
	if err != nil {
		t.Fatal(err)
	}
	_ = file.SetCellValue("Sheet1", "A1", "품목")
	_ = file.SetCellValue("Sheet1", "B1", 12)    // 서식 없는 숫자
	_ = file.SetCellValue("Sheet1", "C1", 18000) // 통화 서식이 붙은 숫자
	_ = file.SetCellStyle("Sheet1", "C1", "C1", styleID)
	_ = file.SetCellValue("Sheet1", "D1", "007") // 앞자리 0은 뜻이 있는 글자다
	_ = file.SetCellValue("Sheet1", "E1", 3.5)
	buffer, err := file.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse("prices.xlsx", buffer.Bytes(), DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int]string, len(parsed.Sheets[0].Cells))
	for _, cell := range parsed.Sheets[0].Cells {
		values[cell.Column] = string(cell.Value)
	}
	for column, want := range map[int]string{1: `"품목"`, 2: `12`, 3: `18000`, 4: `"007"`, 5: `3.5`} {
		if values[column] != want {
			t.Errorf("column %d = %s, want %s", column, values[column], want)
		}
	}
}

// The default row height and column width have to be read from outside the
// used range. Taking them from the first row and column takes whatever those
// happen to be, so a widened column A loses its width and every ordinary
// column after it is recorded as custom.
func TestXLSXImportReadsSizesAgainstTheRealDefault(t *testing.T) {
	t.Parallel()
	file := excelize.NewFile()
	defer file.Close()
	for _, column := range []string{"A", "B", "C"} {
		_ = file.SetCellValue("Sheet1", column+"1", column)
	}
	if err := file.SetColWidth("Sheet1", "A", "A", 30.71); err != nil {
		t.Fatal(err)
	}
	if err := file.SetRowHeight("Sheet1", 1, 40); err != nil {
		t.Fatal(err)
	}
	buffer, err := file.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse("sizes.xlsx", buffer.Bytes(), DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	layout := parsed.Sheets[0].Layout
	if layout == nil {
		t.Fatal("no layout was read")
	}
	if len(layout.ColumnWidths) != 1 || layout.ColumnWidths[0].Index != 1 {
		t.Fatalf("column widths = %#v, want only column 1", layout.ColumnWidths)
	}
	if width := layout.ColumnWidths[0].Size; width < 210 || width > 230 {
		t.Errorf("column 1 width = %.1f px, want about 220", width)
	}
	if len(layout.RowHeights) != 1 || layout.RowHeights[0].Index != 1 {
		t.Fatalf("row heights = %#v, want only row 1", layout.RowHeights)
	}
}

// The apostrophe that keeps a spreadsheet from running =1+1 when the file is
// opened is a wrapper, not part of the value. Leaving it on the way back in
// means kanpic's own export corrupts its own import: every phone number that
// starts with + gains a character.
func TestDelimitedGuardSurvivesItsOwnRoundTrip(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"=1+1", "+82-10-1234-5678", "-보통 글자", "@handle", "'=1+1", "그냥 글자", "'tis"} {
		guarded := value
		if needsDelimitedGuard(value) {
			guarded = "'" + value
		}
		if back := unguardDelimitedValue(guarded); back != value {
			t.Errorf("%q went out as %q and came back as %q", value, guarded, back)
		}
	}
}

// The same thing through the real reader and writer.
func TestCSVRoundTripKeepsFormulaLookingText(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	phone, _ := json.Marshal("+82-10-1234-5678")
	formulaText, _ := json.Marshal("=1+1")
	wb, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{Title: "연락처", ActorID: "tester", OwnerID: "tester", IdempotencyKey: "guard-1", FileName: "contacts.csv", Format: "csv",
		Sheets: []workbook.ImportSheet{{Name: "Sheet1", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: phone}, {Row: 1, Column: 2, Value: formulaText}}}}})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := New(repository).Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	// The guard is still on the wire: opening this file must not run anything.
	if !strings.Contains(string(exported.Data), "'+82-10-1234-5678") || !strings.Contains(string(exported.Data), "'=1+1") {
		t.Fatalf("exported csv lost its guard: %s", exported.Data)
	}
	parsed, err := Parse("contacts.csv", exported.Data, DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int]string, 2)
	for _, cell := range parsed.Sheets[0].Cells {
		values[cell.Column] = string(cell.Value)
	}
	if values[1] != `"+82-10-1234-5678"` || values[2] != `"=1+1"` {
		t.Fatalf("round trip = %#v", values)
	}
}

// A whole number should look like one in the file. Default formatting turns
// 12,345,678,901,234 into 1.2345678901234e+13, which every other spreadsheet
// writes plainly.
func TestDelimitedExportWritesPlainNumbers(t *testing.T) {
	t.Parallel()
	for value, want := range map[float64]string{
		12345678901234: "12345678901234",
		-1234:          "-1234",
		3.14159:        "3.14159",
		0:              "0",
		1e300:          "1e+300",
	} {
		if text := delimitedNumberText(value); text != want {
			t.Errorf("%v wrote as %q, want %q", value, text, want)
		}
	}
}

// excelize answers GetRowVisible with "hidden" for every row past the last one
// the file stores, so a sheet whose used range reaches beyond its last written
// row - here a merge on row 11 of a nine-row sheet - imported with its tail
// hidden and the merged cell nowhere on screen.
func TestXLSXImportDoesNotHideRowsPastTheLastStoredOne(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "tail", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: 1, IdempotencyKey: "tail-cells", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: value("머리글")},
		{Row: 4, Column: 1, Value: value("마지막 값")},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplySheetLayout(ctx, workbook.SheetLayoutMutation{SheetID: sheetID, ActorID: "tester", IdempotencyKey: "tail-hide", ExpectedRevision: 1, Action: "hide", Axis: "row", Start: 2, Count: 1}); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetWorkbook(ctx, wb.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The merge lives past every row the sheet writes down.
	selected, err := cellrange.Parse("A11:C11")
	if err != nil {
		t.Fatal(err)
	}
	merged, err := workbook.BuildMergeCells(nil, selected, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: current.Version, IdempotencyKey: "tail-merge", Cells: merged}); err != nil {
		t.Fatal(err)
	}

	service := New(repository)
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(exported.Name, exported.Data, 0)
	if err != nil {
		t.Fatal(err)
	}
	layout := parsed.Sheets[0].Layout
	if layout == nil || len(layout.HiddenRows) != 1 || layout.HiddenRows[0].Start != 2 || layout.HiddenRows[0].End != 2 {
		t.Fatalf("hidden rows after the round trip = %#v", layout)
	}
}

// A note is the whole point of the cell it hangs on. Neither direction carried
// one: exports arrived in Excel without comments, and an Excel comment was
// dropped on the way in.
func TestXLSXNotesSurviveAnExportImportRoundTrip(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "notes", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: 1, IdempotencyKey: "note-cells", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: value("매출")},
		{Row: 2, Column: 1, Value: value(1200), Note: "회계팀 추정치입니다"},
		// A note with nothing else on the cell still has to travel.
		{Row: 3, Column: 2, Note: "여기에 실적을 채워 주세요"},
	}}); err != nil {
		t.Fatal(err)
	}
	service := New(repository)
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(exported.Name, exported.Data, 0)
	if err != nil {
		t.Fatal(err)
	}
	notes := map[string]string{}
	for _, cell := range parsed.Sheets[0].Cells {
		if cell.Note != "" {
			notes[cellrange.Address(cell.Row, cell.Column)] = cell.Note
		}
	}
	if notes["A2"] != "회계팀 추정치입니다" || notes["B3"] != "여기에 실적을 채워 주세요" || len(notes) != 2 {
		t.Fatalf("notes after the round trip = %#v", notes)
	}
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "default", Title: "restored", OwnerID: "tester", ActorID: "tester",
		IdempotencyKey: "note-import", Sheets: parsed.Sheets,
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A1:C3")
	stored, err := repository.ReadRange(ctx, restored.Sheets[0].ID, selected)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, cell := range stored {
		if cell.Note != "" {
			found[cellrange.Address(cell.Row, cell.Column)] = cell.Note
		}
	}
	if found["A2"] != "회계팀 추정치입니다" || found["B3"] != "여기에 실적을 채워 주세요" {
		t.Fatalf("stored notes = %#v", found)
	}
}

// A name is how a sheet explains itself. Exports carried no definedNames at
// all, so =SUM(단가) opened in Excel as #NAME?.
func TestXLSXExportCarriesNamedRanges(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "names", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: 1, IdempotencyKey: "name-cells", Cells: []workbook.CellInput{
		{Row: 1, Column: 3, Value: value(1500)}, {Row: 2, Column: 3, Value: value(3200)},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateNamedRange(ctx, wb.ID, "tester", workbook.CreateNamedRangeInput{IdempotencyKey: "name-1", SheetID: sheetID, Name: "단가", Range: "C1:C2"}); err != nil {
		t.Fatal(err)
	}
	service := New(repository)
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenReader(bytes.NewReader(exported.Data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	names := file.GetDefinedName()
	if len(names) != 1 || names[0].Name != "단가" || names[0].RefersTo != "Sheet1!$C$1:$C$2" {
		t.Fatalf("defined names = %#v", names)
	}
	parsed, err := Parse(exported.Name, exported.Data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.NamedRanges) != 1 || parsed.NamedRanges[0].Name != "단가" || parsed.NamedRanges[0].Range != "C1:C2" || parsed.NamedRanges[0].SheetName != "Sheet1" {
		t.Fatalf("parsed names = %#v", parsed.NamedRanges)
	}
	if len(parsed.Preview.Warnings) != 0 {
		t.Fatalf("a name the import keeps must not be reported as dropped: %#v", parsed.Preview.Warnings)
	}
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "default", Title: "restored", OwnerID: "tester", ActorID: "tester",
		IdempotencyKey: "name-import", Sheets: parsed.Sheets, NamedRanges: parsed.NamedRanges,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.ListNamedRanges(ctx, restored.ID)
	if err != nil || len(stored) != 1 || stored[0].Name != "단가" || stored[0].Range != "C1:C2" || stored[0].SheetID != restored.Sheets[0].ID {
		t.Fatalf("stored names = %#v, %v", stored, err)
	}
}

// A name has to exist before the import recalculates, or every formula using
// it is evaluated as #NAME? and that answer is what gets stored.
func TestXLSXImportResolvesFormulasThatUseAnImportedName(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	ctx := context.Background()
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "named formula", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := wb.Sheets[0].ID
	if _, err := repository.CreateNamedRange(ctx, wb.ID, "tester", workbook.CreateNamedRangeInput{IdempotencyKey: "nf-1", SheetID: sheetID, Name: "단가", Range: "C1:C2"}); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetWorkbook(ctx, wb.ID)
	if err != nil {
		t.Fatal(err)
	}
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "tester", BaseVersion: current.Version, IdempotencyKey: "nf-cells", Cells: []workbook.CellInput{
		{Row: 1, Column: 3, Value: value(1500)}, {Row: 2, Column: 3, Value: value(3200)},
		{Row: 4, Column: 1, Formula: "=SUM(단가)"},
	}}); err != nil {
		t.Fatal(err)
	}
	service := New(repository)
	exported, err := service.Export(ctx, ExportRequest{WorkbookID: wb.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(exported.Name, exported.Data, 0)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "default", Title: "restored", OwnerID: "tester", ActorID: "tester",
		IdempotencyKey: "nf-import", Sheets: parsed.Sheets, NamedRanges: parsed.NamedRanges,
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A4")
	cells, err := repository.ReadRange(ctx, restored.Sheets[0].ID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "4700" {
		t.Fatalf("named formula after the round trip = %#v, %v", cells, err)
	}
}

// Excel names that kanpic has no equivalent for still have to be reported, or
// the file arrives short of what it carried and nothing says so.
func TestXLSXImportReportsNamesItCannotKeep(t *testing.T) {
	t.Parallel()
	file := excelize.NewFile()
	defer file.Close()
	name := file.GetSheetName(0)
	if err := file.SetCellValue(name, "A1", 1); err != nil {
		t.Fatal(err)
	}
	for _, definition := range []*excelize.DefinedName{
		{Name: "쓸_수_있는_이름", RefersTo: name + "!$A$1:$A$3"},
		{Name: "_xlnm.Print_Area", RefersTo: name + "!$A$1:$B$2"},
		{Name: "시트_전용", RefersTo: name + "!$A$1", Scope: name},
	} {
		if err := file.SetDefinedName(definition); err != nil {
			t.Fatalf("%s: %v", definition.Name, err)
		}
	}
	var buffer bytes.Buffer
	if err := file.Write(&buffer); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse("names.xlsx", buffer.Bytes(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.NamedRanges) != 1 || parsed.NamedRanges[0].Name != "쓸_수_있는_이름" {
		t.Fatalf("kept names = %#v", parsed.NamedRanges)
	}
	if len(parsed.Preview.Warnings) != 1 || !strings.Contains(parsed.Preview.Warnings[0], "이름 정의 2개") {
		t.Fatalf("import warnings = %#v", parsed.Preview.Warnings)
	}
}
