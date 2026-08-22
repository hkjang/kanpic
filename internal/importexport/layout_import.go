package importexport

import (
	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
)

// readSheetLayout recovers the arrangement an XLSX sheet describes. Excel
// stores a default for every row and column, so only the ones that differ are
// carried over: recording the defaults would fill the layout with entries that
// mean nothing.
// storedRowCount is how many <row> elements the sheet actually carries.
// excelize answers GetRowVisible for anything past that with "hidden" rather
// than an error, so a sheet whose used range reaches beyond its last row - a
// merge, a validation, a conditional format - imported with its tail hidden.
// The row iterator stops at the last stored element, trailing empty rows
// included, which is exactly the bound GetRowVisible is meaningful within.
func storedRowCount(file *excelize.File, name string) int {
	rows, err := file.Rows(name)
	if err != nil {
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	return count
}

func readSheetLayout(file *excelize.File, name string, maxRow, maxColumn int) *workbook.SheetLayout {
	if maxRow < 1 || maxColumn < 1 {
		return nil
	}
	if maxRow > maxLayoutRow {
		maxRow = maxLayoutRow
	}
	layout := workbook.SheetLayout{Revision: 1}
	// The defaults have to come from outside the used range. Reading them from
	// the first row and column takes whatever those happen to be: a sheet whose
	// column A is widened loses that width and every ordinary column after it
	// is recorded as custom instead.
	defaultHeight, _ := file.GetRowHeight(name, min(maxRow+1, maxLayoutRow))
	defaultWidth, _ := file.GetColWidth(name, columnLetters(min(maxColumn+1, maxLayoutColumn)))

	stored := storedRowCount(file, name)
	hiddenRun := 0
	for row := 1; row <= maxRow; row++ {
		visible, err := file.GetRowVisible(name, row)
		if row > stored {
			// A row the file never wrote down cannot be hidden, whatever
			// excelize answers for it.
			visible, err = true, nil
		}
		if err == nil && !visible {
			if hiddenRun == 0 {
				hiddenRun = row
			}
		} else if hiddenRun > 0 {
			layout.HiddenRows = append(layout.HiddenRows, workbook.DimensionRange{Start: hiddenRun, End: row - 1})
			hiddenRun = 0
		}
		if height, err := file.GetRowHeight(name, row); err == nil && height > 0 && height != defaultHeight {
			layout.RowHeights = append(layout.RowHeights, workbook.DimensionSize{Index: row, Size: height * pixelsPerPoint})
		}
	}
	if hiddenRun > 0 {
		layout.HiddenRows = append(layout.HiddenRows, workbook.DimensionRange{Start: hiddenRun, End: maxRow})
	}

	hiddenColumn := 0
	for column := 1; column <= maxColumn; column++ {
		letters := columnLetters(column)
		visible, err := file.GetColVisible(name, letters)
		if err == nil && !visible {
			if hiddenColumn == 0 {
				hiddenColumn = column
			}
		} else if hiddenColumn > 0 {
			layout.HiddenColumns = append(layout.HiddenColumns, workbook.DimensionRange{Start: hiddenColumn, End: column - 1})
			hiddenColumn = 0
		}
		if width, err := file.GetColWidth(name, letters); err == nil && width > 0 && width != defaultWidth {
			layout.ColumnWidths = append(layout.ColumnWidths, workbook.DimensionSize{Index: column, Size: width*pixelsPerCharacter + columnPadding})
		}
	}
	if hiddenColumn > 0 {
		layout.HiddenColumns = append(layout.HiddenColumns, workbook.DimensionRange{Start: hiddenColumn, End: maxColumn})
	}

	layout.RowGroups = outlineGroups(maxRow, func(index int) int {
		level, err := file.GetRowOutlineLevel(name, index)
		if err != nil {
			return 0
		}
		return int(level)
	})
	layout.ColumnGroups = outlineGroups(maxColumn, func(index int) int {
		level, err := file.GetColOutlineLevel(name, columnLetters(index))
		if err != nil {
			return 0
		}
		return int(level)
	})

	if panes, err := file.GetPanes(name); err == nil && panes.Freeze {
		layout.FrozenRows, layout.FrozenColumns = panes.YSplit, panes.XSplit
	}
	if len(layout.HiddenRows) == 0 && len(layout.HiddenColumns) == 0 && len(layout.RowHeights) == 0 &&
		len(layout.ColumnWidths) == 0 && len(layout.RowGroups) == 0 && len(layout.ColumnGroups) == 0 &&
		layout.FrozenRows == 0 && layout.FrozenColumns == 0 {
		return nil
	}
	return &layout
}

// outlineGroups turns runs of equal outline level into the spans kanpic folds.
// Excel records a level per row; kanpic records the span, so consecutive rows
// at the same level are one group.
func outlineGroups(limit int, levelAt func(index int) int) []workbook.DimensionGroup {
	groups := make([]workbook.DimensionGroup, 0)
	start, current := 0, 0
	for index := 1; index <= limit; index++ {
		level := levelAt(index)
		if level == current {
			continue
		}
		if current > 0 && start > 0 {
			groups = append(groups, workbook.DimensionGroup{Start: start, End: index - 1, Depth: current - 1})
		}
		start, current = index, level
	}
	if current > 0 && start > 0 {
		groups = append(groups, workbook.DimensionGroup{Start: start, End: limit, Depth: current - 1})
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}
