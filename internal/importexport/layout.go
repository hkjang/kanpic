package importexport

import (
	"strings"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// Excel measures row height in points and column width in characters, while
// kanpic stores both in pixels. These are the conversions Excel itself uses at
// the default font, so a sheet keeps the proportions it was designed with.
const (
	pixelsPerPoint     = 4.0 / 3.0
	pixelsPerCharacter = 7.0
	columnPadding      = 5.0
	// A workbook may declare a size for a row far below its data. Applying
	// those to XLSX would write a row record for every one of them.
	maxLayoutRow = 20_000
	// The last column XLSX can address. Probing one past the used range needs
	// somewhere to stop.
	maxLayoutColumn = 16_384
)

func rowHeightPoints(pixels float64) float64  { return pixels / pixelsPerPoint }
func columnWidthChars(pixels float64) float64 { return (pixels - columnPadding) / pixelsPerCharacter }

func columnLetters(column int) string {
	address := cellrange.Address(1, column)
	return strings.TrimRight(address, "0123456789")
}

// applySheetLayout carries the parts of a kanpic sheet layout that XLSX can
// express: sizes, hidden rows and columns, frozen panes and outline groups.
// Without it an exported sheet loses every arrangement decision somebody made,
// and the file looks nothing like the sheet it came from.
func applySheetLayout(file *excelize.File, name string, layout workbook.SheetLayout) error {
	// 인쇄 영역은 엑셀에서 시트에 걸린 _xlnm.Print_Area 라는 이름으로 담긴다.
	// 내보낼 때 함께 적어 주지 않으면, 여기서 정한 인쇄 영역이 엑셀로 갔다가
	// 돌아오는 사이에 사라진다.
	if target, ok := definedNameTarget(name, layout.PrintArea); ok {
		if err := file.SetDefinedName(&excelize.DefinedName{Name: "_xlnm.Print_Area", Scope: name, RefersTo: target}); err != nil {
			return err
		}
	}
	for _, item := range layout.RowHeights {
		if item.Index < 1 || item.Index > maxLayoutRow {
			continue
		}
		if err := file.SetRowHeight(name, item.Index, rowHeightPoints(item.Size)); err != nil {
			return err
		}
	}
	for _, item := range layout.ColumnWidths {
		if item.Index < 1 {
			continue
		}
		letters := columnLetters(item.Index)
		if err := file.SetColWidth(name, letters, letters, columnWidthChars(item.Size)); err != nil {
			return err
		}
	}
	for _, span := range layout.HiddenRows {
		for row := max(1, span.Start); row <= span.End && row <= maxLayoutRow; row++ {
			if err := file.SetRowVisible(name, row, false); err != nil {
				return err
			}
		}
	}
	for _, span := range layout.HiddenColumns {
		if span.Start < 1 || span.End < span.Start {
			continue
		}
		if err := file.SetColVisible(name, columnLetters(span.Start)+":"+columnLetters(span.End), false); err != nil {
			return err
		}
	}
	// Outline levels are what make a folded report fold in Excel too. A
	// collapsed group also hides its rows, which Excel stores separately.
	for _, group := range layout.RowGroups {
		level := group.Depth + 1
		if level > 7 {
			level = 7
		}
		for row := max(1, group.Start); row <= group.End && row <= maxLayoutRow; row++ {
			if err := file.SetRowOutlineLevel(name, row, uint8(level)); err != nil {
				return err
			}
			if group.Collapsed {
				if err := file.SetRowVisible(name, row, false); err != nil {
					return err
				}
			}
		}
	}
	for _, group := range layout.ColumnGroups {
		if group.Start < 1 || group.End < group.Start {
			continue
		}
		level := group.Depth + 1
		if level > 7 {
			level = 7
		}
		span := columnLetters(group.Start) + ":" + columnLetters(group.End)
		if err := file.SetColOutlineLevel(name, columnLetters(group.Start), uint8(level)); err != nil {
			return err
		}
		if group.Collapsed {
			if err := file.SetColVisible(name, span, false); err != nil {
				return err
			}
		}
	}
	if layout.FrozenRows > 0 || layout.FrozenColumns > 0 {
		if err := file.SetPanes(name, &excelize.Panes{
			Freeze: true, Split: false,
			XSplit: layout.FrozenColumns, YSplit: layout.FrozenRows,
			TopLeftCell: cellrange.Address(layout.FrozenRows+1, layout.FrozenColumns+1),
			ActivePane:  "bottomRight",
		}); err != nil {
			return err
		}
	}
	return nil
}
