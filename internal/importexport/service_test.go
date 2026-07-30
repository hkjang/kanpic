package importexport

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"testing"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
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
	styleID, err := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"0F766E"}, Pattern: 1}})
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
	if len(parsed.Sheets[0].Cells[0].Style) == 0 {
		t.Fatal("style not preserved")
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
