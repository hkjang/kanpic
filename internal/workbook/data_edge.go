package workbook

import (
	"context"
	"fmt"
	"sort"

	"kanpic/pkg/cellrange"
)

// RangeReader is the one repository call the data-edge and current-region
// lookups need. Keeping them on top of ReadRange means every repository gets
// them without a second storage path to keep in step.
type RangeReader interface {
	ReadRange(context.Context, string, cellrange.Range) ([]Cell, error)
}

// Directions a data-edge walk can take, named after the arrow keys.
const (
	EdgeUp    = "up"
	EdgeDown  = "down"
	EdgeLeft  = "left"
	EdgeRight = "right"
)

// DataLine reports which cells along one row or column hold something, over the
// stretch a Ctrl+arrow walk from one cell needs. Indexes are row numbers for up
// and down and column numbers for left and right.
//
// The editor only holds the rows on screen, so walking its own cells stops at
// the edge of what it loaded and lands in the middle of a long column. The
// server answers instead, and the editor still does the walk itself because it
// alone knows which rows a filter or a collapsed group hides.
type DataLine struct {
	Direction string `json:"direction"`
	// From is where the scan started and Through the last index it covered, in
	// walking order: for up and left Through is the smaller of the two.
	From    int `json:"from"`
	Through int `json:"through"`
	// Runs are the occupied stretches inside the covered span, each [first,
	// last] in ascending order. A covered index outside every run is empty.
	Runs [][2]int `json:"runs"`
	// Limit is the last index the sheet has in this direction, so a client can
	// tell "nothing more" from "not looked at yet".
	Limit int `json:"limit"`
}

// dataLineChunk is how far one read reaches along the line. A Postgres block is
// 64 cells on a side, so this is 64 blocks; an empty read doubles the next one,
// which keeps a walk across a large empty stretch to a handful of reads.
const dataLineChunk = 4096

// ScanDataLine reads the line through (row, column) in the given direction,
// starting at that cell, until it has covered what a spreadsheet's Ctrl+arrow
// needs when nothing is hidden, or reached the edge of the sheet: a walk that
// starts inside a block stops at the block's first gap, and any other walk at
// the start of the next block. When hidden rows push the walk further, the
// client asks again from where this answer ends.
func ScanDataLine(ctx context.Context, reader RangeReader, sheetID string, row, column int, direction string) (DataLine, error) {
	if row < 1 || row > cellrange.MaxRows || column < 1 || column > cellrange.MaxColumns {
		return DataLine{}, fmt.Errorf("%w: cell is outside the sheet", ErrInvalid)
	}
	vertical, step, err := edgeDirection(direction)
	if err != nil {
		return DataLine{}, err
	}
	from, limit := column, 1
	if vertical {
		from = row
	}
	if step > 0 {
		limit = cellrange.MaxColumns
		if vertical {
			limit = cellrange.MaxRows
		}
	}
	line := DataLine{Direction: direction, From: from, Through: from, Runs: [][2]int{}, Limit: limit}
	occupied := make([]int, 0)
	// needed drops to one when the walk starts inside a block: it ends at the
	// block's last cell, and nothing past the first gap matters.
	changes, needed, chunk, cursor, seen := 0, 2, dataLineChunk, from, 0
	var state, first bool
	for {
		if err := ctx.Err(); err != nil {
			return DataLine{}, err
		}
		// A chunk ends on a block boundary so the next read starts on a fresh
		// block instead of reading the last one twice.
		end := cursor + step*(chunk-1)
		if step > 0 {
			end = min(alignUp(end), limit)
		} else {
			end = max(alignDown(end), limit)
		}
		low, high := min(cursor, end), max(cursor, end)
		selected := cellrange.Range{Start: cellrange.Position{Row: row, Column: low}, End: cellrange.Position{Row: row, Column: high}}
		if vertical {
			selected = cellrange.Range{Start: cellrange.Position{Row: low, Column: column}, End: cellrange.Position{Row: high, Column: column}}
		}
		cells, err := reader.ReadRange(ctx, sheetID, selected)
		if err != nil {
			return DataLine{}, err
		}
		found := make(map[int]bool, len(cells))
		for _, cell := range cells {
			if !cellHoldsData(cell) {
				continue
			}
			index := cell.Column
			if vertical {
				index = cell.Row
			}
			found[index] = true
		}
		for index := cursor; ; index += step {
			here := found[index]
			if here {
				occupied = append(occupied, index)
			}
			switch seen {
			case 0:
				state, first = here, here
			case 1:
				if first && here {
					needed = 1
				}
			}
			seen++
			if here != state {
				state = here
				changes++
			}
			if index == end {
				break
			}
		}
		line.Through = end
		if changes >= needed || end == limit {
			break
		}
		if len(found) == 0 {
			chunk = min(chunk*2, cellrange.MaxRows)
		} else {
			chunk = dataLineChunk
		}
		cursor = end + step
	}
	sort.Ints(occupied)
	for _, index := range occupied {
		if count := len(line.Runs); count > 0 && line.Runs[count-1][1]+1 == index {
			line.Runs[count-1][1] = index
			continue
		}
		line.Runs = append(line.Runs, [2]int{index, index})
	}
	return line, nil
}

// DataRegion is the block of data around a cell: the rectangle that grows from
// it until every side is bordered by an empty row or column. Select-all and
// Ctrl+Shift+A pick it before the whole sheet, the way spreadsheets do.
type DataRegion struct {
	StartRow    int  `json:"start_row"`
	StartColumn int  `json:"start_column"`
	EndRow      int  `json:"end_row"`
	EndColumn   int  `json:"end_column"`
	Empty       bool `json:"empty"`
}

// regionStrip is how many rows (or columns) one read loads around a cell the
// region has not seen yet: eight blocks deep and one block wide, since a table
// mostly grows downwards.
const regionStrip = 512

// CurrentRegion computes the data region around (row, column) from the stored
// cells. It is the same growth rule the editor's dataRegion uses, so sort and
// clean-up dialogs and this agree on what "the table" is; the difference is
// that this sees every row rather than the ones on screen.
func CurrentRegion(ctx context.Context, reader RangeReader, sheetID string, row, column int) (DataRegion, error) {
	if row < 1 || row > cellrange.MaxRows || column < 1 || column > cellrange.MaxColumns {
		return DataRegion{}, fmt.Errorf("%w: cell is outside the sheet", ErrInvalid)
	}
	grid := &occupancy{ctx: ctx, reader: reader, sheetID: sheetID, loaded: map[[2]int]bool{}, filled: map[[2]int]bool{}}
	region := DataRegion{StartRow: row, StartColumn: column, EndRow: row, EndColumn: column}
	rowHasData := func(r, first, last int) (bool, error) {
		for c := first; c <= last; c++ {
			if ok, err := grid.has(r, c); err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	}
	columnHasData := func(c, first, last int) (bool, error) {
		for r := first; r <= last; r++ {
			if ok, err := grid.has(r, c); err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	}
	for {
		grew := false
		for region.StartRow > 1 {
			ok, err := rowHasData(region.StartRow-1, region.StartColumn, region.EndColumn)
			if err != nil {
				return DataRegion{}, err
			}
			if !ok {
				break
			}
			region.StartRow, grew = region.StartRow-1, true
		}
		for region.EndRow < cellrange.MaxRows {
			ok, err := rowHasData(region.EndRow+1, region.StartColumn, region.EndColumn)
			if err != nil {
				return DataRegion{}, err
			}
			if !ok {
				break
			}
			region.EndRow, grew = region.EndRow+1, true
		}
		for region.StartColumn > 1 {
			ok, err := columnHasData(region.StartColumn-1, region.StartRow, region.EndRow)
			if err != nil {
				return DataRegion{}, err
			}
			if !ok {
				break
			}
			region.StartColumn, grew = region.StartColumn-1, true
		}
		for region.EndColumn < cellrange.MaxColumns {
			ok, err := columnHasData(region.EndColumn+1, region.StartRow, region.EndRow)
			if err != nil {
				return DataRegion{}, err
			}
			if !ok {
				break
			}
			region.EndColumn, grew = region.EndColumn+1, true
		}
		if !grew {
			break
		}
	}
	if region.StartRow == region.EndRow && region.StartColumn == region.EndColumn {
		ok, err := grid.has(row, column)
		if err != nil {
			return DataRegion{}, err
		}
		region.Empty = !ok
	}
	return region, nil
}

// occupancy answers "does this cell hold data" by reading the sheet in strips
// the first time a strip is asked about.
type occupancy struct {
	ctx     context.Context
	reader  RangeReader
	sheetID string
	loaded  map[[2]int]bool
	filled  map[[2]int]bool
}

func (o *occupancy) has(row, column int) (bool, error) {
	strip := [2]int{(row - 1) / regionStrip, (column - 1) / cellBlockSize}
	if !o.loaded[strip] {
		if err := o.ctx.Err(); err != nil {
			return false, err
		}
		selected := cellrange.Range{
			Start: cellrange.Position{Row: strip[0]*regionStrip + 1, Column: strip[1]*cellBlockSize + 1},
			End:   cellrange.Position{Row: min((strip[0]+1)*regionStrip, cellrange.MaxRows), Column: min((strip[1]+1)*cellBlockSize, cellrange.MaxColumns)},
		}
		cells, err := o.reader.ReadRange(o.ctx, o.sheetID, selected)
		if err != nil {
			return false, err
		}
		for _, cell := range cells {
			if cellHoldsData(cell) {
				o.filled[[2]int{cell.Row, cell.Column}] = true
			}
		}
		o.loaded[strip] = true
	}
	return o.filled[[2]int{row, column}], nil
}

// cellHoldsData is the editor's test for a filled cell: a value or a formula.
// A cell that only carries a style, a note or a merge is empty to a walk.
func cellHoldsData(cell Cell) bool {
	if cell.Formula != "" {
		return true
	}
	value := string(cell.Value)
	return value != "" && value != "null"
}

func edgeDirection(direction string) (bool, int, error) {
	switch direction {
	case EdgeUp:
		return true, -1, nil
	case EdgeDown:
		return true, 1, nil
	case EdgeLeft:
		return false, -1, nil
	case EdgeRight:
		return false, 1, nil
	}
	return false, 0, fmt.Errorf("%w: direction must be up, down, left or right", ErrInvalid)
}

func alignUp(index int) int   { return ((index-1)/cellBlockSize + 1) * cellBlockSize }
func alignDown(index int) int { return ((index-1)/cellBlockSize)*cellBlockSize + 1 }
