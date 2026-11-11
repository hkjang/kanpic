package workbook

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"kanpic/pkg/cellrange"
)

// gridReader is a sheet held as a set of filled coordinates. It counts reads so
// the tests can tell a walk that reads in chunks from one that reads cell by
// cell, which is the difference between a keypress and a frozen editor.
type gridReader struct {
	cells map[[2]int]Cell
	reads int
}

func newGridReader() *gridReader { return &gridReader{cells: map[[2]int]Cell{}} }

func (g *gridReader) fill(row, column int) {
	g.cells[[2]int{row, column}] = Cell{Row: row, Column: column, Value: json.RawMessage(`1`)}
}

func (g *gridReader) fillColumn(column, first, last int) {
	for row := first; row <= last; row++ {
		g.fill(row, column)
	}
}

func (g *gridReader) ReadRange(_ context.Context, _ string, selected cellrange.Range) ([]Cell, error) {
	g.reads++
	result := make([]Cell, 0)
	for _, cell := range g.cells {
		if selected.Contains(cell.Row, cell.Column) {
			result = append(result, cell)
		}
	}
	return result, nil
}

// A column longer than any screen is one block to Ctrl+Down, however many
// reads it takes to see its end. The editor used to stop where its loaded rows
// ended, which is the "moves to a cell in the middle" report.
func TestScanDataLineCoversAColumnLongerThanTheScreen(t *testing.T) {
	t.Parallel()
	grid := newGridReader()
	grid.fillColumn(1, 1, 50_000)
	line, err := ScanDataLine(context.Background(), grid, "sheet", 1, 1, EdgeDown)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(line.Runs, [][2]int{{1, 50_000}}) {
		t.Fatalf("runs=%v", line.Runs)
	}
	if line.Through <= 50_000 {
		t.Fatalf("through=%d, the empty cell after the block must be covered", line.Through)
	}
	if grid.reads > 15 {
		t.Fatalf("reads=%d, a 50,000-row walk must read in chunks", grid.reads)
	}
}

// From inside a block the walk needs one change of state; across a gap it needs
// two. The scan stops after the second so a sheet with data far below is not
// read to the bottom.
func TestScanDataLineStopsAfterTheSecondChange(t *testing.T) {
	t.Parallel()
	grid := newGridReader()
	grid.fillColumn(2, 1, 10)
	grid.fillColumn(2, 20_000, 20_010)
	grid.fillColumn(2, 900_000, 900_001)
	line, err := ScanDataLine(context.Background(), grid, "sheet", 10, 2, EdgeDown)
	if err != nil {
		t.Fatal(err)
	}
	// 10 is filled, 11 empty (first change), 20000 filled (second change).
	if line.From != 10 || len(line.Runs) == 0 || line.Runs[0] != [2]int{10, 10} || line.Runs[1] != [2]int{20_000, 20_010} {
		t.Fatalf("line=%+v", line)
	}
	if line.Through >= 900_000 {
		t.Fatalf("through=%d, the scan should stop once the second block starts", line.Through)
	}
}

// Walking up or left reports Through as the smaller index and keeps runs in
// ascending order, so the client reads every answer the same way.
func TestScanDataLineWalksUpAndLeft(t *testing.T) {
	t.Parallel()
	grid := newGridReader()
	grid.fillColumn(3, 5, 9)
	up, err := ScanDataLine(context.Background(), grid, "sheet", 100, 3, EdgeUp)
	if err != nil {
		t.Fatal(err)
	}
	if up.Through != 1 || up.Limit != 1 || !reflect.DeepEqual(up.Runs, [][2]int{{5, 9}}) {
		t.Fatalf("up=%+v", up)
	}
	for column := 4; column <= 70; column++ {
		grid.fill(7, column)
	}
	left, err := ScanDataLine(context.Background(), grid, "sheet", 7, 70, EdgeLeft)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left.Runs, [][2]int{{3, 70}}) || left.Through != 1 {
		t.Fatalf("left=%+v", left)
	}
}

// An empty sheet below the cell is crossed in a few doubling reads, not one per
// 4,096 rows.
func TestScanDataLineCrossesAnEmptySheetQuickly(t *testing.T) {
	t.Parallel()
	grid := newGridReader()
	line, err := ScanDataLine(context.Background(), grid, "sheet", 1, 1, EdgeDown)
	if err != nil {
		t.Fatal(err)
	}
	if line.Through != cellrange.MaxRows || len(line.Runs) != 0 {
		t.Fatalf("line=%+v", line)
	}
	if grid.reads > 12 {
		t.Fatalf("reads=%d", grid.reads)
	}
}

// A style-only cell is empty to the walk, the same as in the editor.
func TestScanDataLineTreatsStyleOnlyCellsAsEmpty(t *testing.T) {
	t.Parallel()
	grid := newGridReader()
	grid.fillColumn(1, 1, 3)
	grid.cells[[2]int{4, 1}] = Cell{Row: 4, Column: 1, Style: json.RawMessage(`{"bold":true}`)}
	grid.cells[[2]int{5, 1}] = Cell{Row: 5, Column: 1, Value: json.RawMessage(`null`)}
	grid.cells[[2]int{6, 1}] = Cell{Row: 6, Column: 1, Formula: "=A1"}
	line, err := ScanDataLine(context.Background(), grid, "sheet", 1, 1, EdgeDown)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(line.Runs, [][2]int{{1, 3}, {6, 6}}) {
		t.Fatalf("runs=%v", line.Runs)
	}
}

func TestScanDataLineRejectsUnknownDirections(t *testing.T) {
	t.Parallel()
	if _, err := ScanDataLine(context.Background(), newGridReader(), "sheet", 1, 1, "sideways"); err == nil {
		t.Fatal("unknown direction accepted")
	}
	if _, err := ScanDataLine(context.Background(), newGridReader(), "sheet", 0, 1, EdgeDown); err == nil {
		t.Fatal("row 0 accepted")
	}
}

// The region is the whole table, including the rows no screen shows.
func TestCurrentRegionFindsTheWholeTable(t *testing.T) {
	t.Parallel()
	grid := newGridReader()
	for column := 2; column <= 6; column++ {
		grid.fillColumn(column, 3, 10_000)
	}
	// Something unrelated past an empty column stays out.
	grid.fillColumn(8, 1, 10)
	region, err := CurrentRegion(context.Background(), grid, "sheet", 5_000, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := DataRegion{StartRow: 3, StartColumn: 2, EndRow: 10_000, EndColumn: 6}
	if region != want {
		t.Fatalf("region=%+v want %+v", region, want)
	}
	if grid.reads > 200 {
		t.Fatalf("reads=%d", grid.reads)
	}
}

// A cell with nothing around it has no region; the caller selects the sheet.
func TestCurrentRegionReportsAnEmptyCell(t *testing.T) {
	t.Parallel()
	grid := newGridReader()
	grid.fillColumn(1, 1, 5)
	region, err := CurrentRegion(context.Background(), grid, "sheet", 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !region.Empty || region.StartRow != 100 || region.EndColumn != 10 {
		t.Fatalf("region=%+v", region)
	}
	// An empty cell touching data grows into it, like the editor's own rule.
	touching, err := CurrentRegion(context.Background(), grid, "sheet", 6, 1)
	if err != nil {
		t.Fatal(err)
	}
	if touching.Empty || touching.StartRow != 1 || touching.EndRow != 6 {
		t.Fatalf("touching=%+v", touching)
	}
}

// The memory repository answers through the same ReadRange path the server
// uses, so the lookups work on a real store, not only on the test grid.
func TestDataEdgeLookupsReadTheRepository(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "끝 찾기", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	inputs := make([]CellInput, 0, 300)
	for row := 1; row <= 300; row++ {
		inputs = append(inputs, CellInput{Row: row, Column: 1, Value: json.RawMessage(`7`)})
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "tester", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: inputs}); err != nil {
		t.Fatal(err)
	}
	line, err := ScanDataLine(ctx, repository, sheet, 1, 1, EdgeDown)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(line.Runs, [][2]int{{1, 300}}) {
		t.Fatalf("runs=%v", line.Runs)
	}
	region, err := CurrentRegion(ctx, repository, sheet, 150, 1)
	if err != nil {
		t.Fatal(err)
	}
	if region != (DataRegion{StartRow: 1, StartColumn: 1, EndRow: 300, EndColumn: 1}) {
		t.Fatalf("region=%+v", region)
	}
}
