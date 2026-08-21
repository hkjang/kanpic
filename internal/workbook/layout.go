package workbook

import (
	"fmt"
	"sort"
	"strings"

	"kanpic/internal/formula"
)

const (
	DefaultRowHeight   = 27.0
	DefaultColumnWidth = 108.0
	MinRowHeight       = 16.0
	MaxRowHeight       = 400.0
	MinColumnWidth     = 32.0
	MaxColumnWidth     = 600.0
	MaxLayoutSpan      = 10_000
	MaxLayoutEntries   = 10_000
	MaxFrozenRows      = 100
	MaxFrozenColumns   = 50
)

func defaultSheetLayout() SheetLayout { return SheetLayout{Revision: 1} }

func cloneSheetLayout(layout SheetLayout) SheetLayout {
	layout.RowHeights = append([]DimensionSize(nil), layout.RowHeights...)
	layout.ColumnWidths = append([]DimensionSize(nil), layout.ColumnWidths...)
	layout.HiddenRows = append([]DimensionRange(nil), layout.HiddenRows...)
	layout.HiddenColumns = append([]DimensionRange(nil), layout.HiddenColumns...)
	layout.RowGroups = append([]DimensionGroup(nil), layout.RowGroups...)
	layout.ColumnGroups = append([]DimensionGroup(nil), layout.ColumnGroups...)
	return layout
}

func cloneSheet(sheet Sheet) Sheet {
	sheet.Layout = cloneSheetLayout(sheet.Layout)
	return sheet
}

func normalizeSheetLayout(layout SheetLayout) SheetLayout {
	if layout.Revision < 1 {
		layout.Revision = 1
	}
	layout.RowHeights = normalizeDimensionSizes(layout.RowHeights, formula.MaxRows, MinRowHeight, MaxRowHeight, DefaultRowHeight)
	layout.ColumnWidths = normalizeDimensionSizes(layout.ColumnWidths, formula.MaxColumns, MinColumnWidth, MaxColumnWidth, DefaultColumnWidth)
	layout.HiddenRows = normalizeDimensionRanges(layout.HiddenRows, formula.MaxRows)
	layout.HiddenColumns = normalizeDimensionRanges(layout.HiddenColumns, formula.MaxColumns)
	layout.RowGroups = normalizeDimensionGroups(layout.RowGroups, formula.MaxRows)
	layout.ColumnGroups = normalizeDimensionGroups(layout.ColumnGroups, formula.MaxColumns)
	if layout.FrozenRows < 0 || layout.FrozenRows > formula.MaxRows {
		layout.FrozenRows = 0
	}
	if layout.FrozenColumns < 0 || layout.FrozenColumns > formula.MaxColumns {
		layout.FrozenColumns = 0
	}
	return layout
}

func normalizeDimensionSizes(input []DimensionSize, limit int, minimum, maximum, defaultSize float64) []DimensionSize {
	byIndex := make(map[int]float64, len(input))
	for _, item := range input {
		if item.Index < 1 || item.Index > limit || item.Size < minimum || item.Size > maximum || item.Size == defaultSize {
			continue
		}
		byIndex[item.Index] = item.Size
	}
	indexes := make([]int, 0, len(byIndex))
	for index := range byIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	if len(indexes) > MaxLayoutEntries {
		indexes = indexes[:MaxLayoutEntries]
	}
	result := make([]DimensionSize, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, DimensionSize{Index: index, Size: byIndex[index]})
	}
	return result
}

func normalizeDimensionRanges(input []DimensionRange, limit int) []DimensionRange {
	merged := mergeDimensionRanges(input, limit)
	if len(merged) > MaxLayoutEntries {
		merged = merged[:MaxLayoutEntries]
	}
	return merged
}

func mergeDimensionRanges(input []DimensionRange, limit int) []DimensionRange {
	items := make([]DimensionRange, 0, len(input))
	for _, item := range input {
		if item.Start < 1 {
			item.Start = 1
		}
		if item.End > limit {
			item.End = limit
		}
		if item.Start <= item.End {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Start == items[j].Start {
			return items[i].End < items[j].End
		}
		return items[i].Start < items[j].Start
	})
	merged := make([]DimensionRange, 0, len(items))
	for _, item := range items {
		if len(merged) == 0 || item.Start > merged[len(merged)-1].End+1 {
			merged = append(merged, item)
			continue
		}
		if item.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = item.End
		}
	}
	return merged
}

func normalizeSheetLayoutMutation(input SheetLayoutMutation) (SheetLayoutMutation, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.Axis = strings.ToLower(strings.TrimSpace(input.Axis))
	if input.IdempotencyKey == "" {
		return SheetLayoutMutation{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	if input.ExpectedRevision < 1 {
		return SheetLayoutMutation{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalid)
	}
	switch input.Action {
	case "freeze":
		if input.FrozenRows < 0 || input.FrozenRows > MaxFrozenRows || input.FrozenColumns < 0 || input.FrozenColumns > MaxFrozenColumns {
			return SheetLayoutMutation{}, fmt.Errorf("%w: frozen rows must be at most %d and frozen columns at most %d", ErrInvalid, MaxFrozenRows, MaxFrozenColumns)
		}
		return input, nil
	case "resize", "reset_size", "hide", "show", "show_all", "group", "ungroup", "collapse", "expand":
	default:
		return SheetLayoutMutation{}, fmt.Errorf("%w: unsupported layout action", ErrInvalid)
	}
	if input.Axis != "row" && input.Axis != "column" {
		return SheetLayoutMutation{}, fmt.Errorf("%w: axis must be row or column", ErrInvalid)
	}
	if input.Action == "show_all" {
		return input, nil
	}
	limit := formula.MaxRows
	if input.Axis == "column" {
		limit = formula.MaxColumns
	}
	if input.Start < 1 || input.Count < 1 || input.Count > MaxLayoutSpan || input.Start+input.Count-1 > limit {
		return SheetLayoutMutation{}, fmt.Errorf("%w: layout range exceeds spreadsheet bounds or the %d-item limit", ErrInvalid, MaxLayoutSpan)
	}
	if input.Action == "resize" {
		minimum, maximum := MinRowHeight, MaxRowHeight
		if input.Axis == "column" {
			minimum, maximum = MinColumnWidth, MaxColumnWidth
		}
		if input.Size < minimum || input.Size > maximum {
			return SheetLayoutMutation{}, fmt.Errorf("%w: size must be between %.0f and %.0f pixels", ErrInvalid, minimum, maximum)
		}
	}
	return input, nil
}

func applySheetLayoutMutation(current SheetLayout, input SheetLayoutMutation) (SheetLayout, error) {
	next := cloneSheetLayout(normalizeSheetLayout(current))
	if input.Action == "freeze" {
		next.FrozenRows, next.FrozenColumns = input.FrozenRows, input.FrozenColumns
		next.Revision++
		return next, nil
	}
	switch input.Action {
	case "group", "ungroup", "collapse", "expand":
		limit, groups := formula.MaxRows, next.RowGroups
		if input.Axis == "column" {
			limit, groups = formula.MaxColumns, next.ColumnGroups
		}
		updated, err := applyGroupMutation(groups, input, limit)
		if err != nil {
			return SheetLayout{}, err
		}
		if input.Axis == "row" {
			next.RowGroups = updated
		} else {
			next.ColumnGroups = updated
		}
	default:
		if input.Axis == "row" {
			next.RowHeights, next.HiddenRows = applyDimensionMutation(next.RowHeights, next.HiddenRows, input, DefaultRowHeight)
		} else {
			next.ColumnWidths, next.HiddenColumns = applyDimensionMutation(next.ColumnWidths, next.HiddenColumns, input, DefaultColumnWidth)
		}
	}
	if len(next.RowHeights) > MaxLayoutEntries || len(next.ColumnWidths) > MaxLayoutEntries || len(mergeDimensionRanges(next.HiddenRows, formula.MaxRows)) > MaxLayoutEntries || len(mergeDimensionRanges(next.HiddenColumns, formula.MaxColumns)) > MaxLayoutEntries {
		return SheetLayout{}, fmt.Errorf("%w: sheet layout exceeds the %d-entry limit", ErrInvalid, MaxLayoutEntries)
	}
	next.Revision++
	return normalizeSheetLayout(next), nil
}

func applyDimensionMutation(sizes []DimensionSize, hidden []DimensionRange, input SheetLayoutMutation, defaultSize float64) ([]DimensionSize, []DimensionRange) {
	if input.Action == "show_all" {
		return sizes, nil
	}
	end := input.Start + input.Count - 1
	switch input.Action {
	case "resize", "reset_size":
		byIndex := make(map[int]float64, len(sizes)+input.Count)
		for _, item := range sizes {
			byIndex[item.Index] = item.Size
		}
		for index := input.Start; index <= end; index++ {
			if input.Action == "reset_size" || input.Size == defaultSize {
				delete(byIndex, index)
			} else {
				byIndex[index] = input.Size
			}
		}
		result := make([]DimensionSize, 0, len(byIndex))
		for index, size := range byIndex {
			result = append(result, DimensionSize{Index: index, Size: size})
		}
		return result, hidden
	case "hide":
		return sizes, append(hidden, DimensionRange{Start: input.Start, End: end})
	case "show":
		result := make([]DimensionRange, 0, len(hidden)+1)
		for _, interval := range hidden {
			if interval.End < input.Start || interval.Start > end {
				result = append(result, interval)
				continue
			}
			if interval.Start < input.Start {
				result = append(result, DimensionRange{Start: interval.Start, End: input.Start - 1})
			}
			if interval.End > end {
				result = append(result, DimensionRange{Start: end + 1, End: interval.End})
			}
		}
		return sizes, result
	}
	return sizes, hidden
}

func transformLayoutForStructure(layout SheetLayout, input StructuralMutation) SheetLayout {
	layout = normalizeSheetLayout(layout)
	changed := false
	if input.Axis == "row" {
		layout.RowHeights, changed = transformDimensionSizes(layout.RowHeights, input)
		var rangeChanged bool
		layout.HiddenRows, rangeChanged = transformDimensionRanges(layout.HiddenRows, input)
		changed = changed || rangeChanged
		var groupChanged bool
		layout.RowGroups, groupChanged = transformDimensionGroups(layout.RowGroups, input)
		changed = changed || groupChanged
		frozen := transformFrozenCount(layout.FrozenRows, input)
		changed = changed || frozen != layout.FrozenRows
		layout.FrozenRows = frozen
	} else {
		layout.ColumnWidths, changed = transformDimensionSizes(layout.ColumnWidths, input)
		var rangeChanged bool
		layout.HiddenColumns, rangeChanged = transformDimensionRanges(layout.HiddenColumns, input)
		changed = changed || rangeChanged
		var groupChanged bool
		layout.ColumnGroups, groupChanged = transformDimensionGroups(layout.ColumnGroups, input)
		changed = changed || groupChanged
		frozen := transformFrozenCount(layout.FrozenColumns, input)
		changed = changed || frozen != layout.FrozenColumns
		layout.FrozenColumns = frozen
	}
	if changed {
		layout.Revision++
	}
	return normalizeSheetLayout(layout)
}

func transformDimensionSizes(input []DimensionSize, mutation StructuralMutation) ([]DimensionSize, bool) {
	result := make([]DimensionSize, 0, len(input))
	changed := false
	for _, item := range input {
		index, remains := structuralPosition(item.Index, mutation)
		if !remains {
			changed = true
			continue
		}
		if index != item.Index {
			changed = true
		}
		item.Index = index
		result = append(result, item)
	}
	return result, changed
}

func transformDimensionRanges(input []DimensionRange, mutation StructuralMutation) ([]DimensionRange, bool) {
	result := make([]DimensionRange, 0, len(input))
	changed := false
	for _, interval := range input {
		original := interval
		if mutation.Action == "insert" {
			if mutation.Index <= interval.Start {
				interval.Start += mutation.Count
				interval.End += mutation.Count
			} else if mutation.Index <= interval.End {
				interval.End += mutation.Count
			}
		} else {
			deleteStart, deleteEnd := mutation.Index, mutation.Index+mutation.Count-1
			if deleteEnd < interval.Start {
				interval.Start -= mutation.Count
				interval.End -= mutation.Count
			} else if deleteStart <= interval.End {
				removed := min(interval.End, deleteEnd) - max(interval.Start, deleteStart) + 1
				interval.End -= removed
				if deleteStart < interval.Start {
					shift := min(mutation.Count, interval.Start-deleteStart)
					interval.Start -= shift
					interval.End -= shift
				}
			}
		}
		if interval.Start <= interval.End {
			result = append(result, interval)
		}
		if interval != original {
			changed = true
		}
	}
	return result, changed
}

func transformFrozenCount(count int, mutation StructuralMutation) int {
	if count == 0 || mutation.Index > count {
		return count
	}
	if mutation.Action == "insert" {
		return count + mutation.Count
	}
	deletedInside := min(count, mutation.Index+mutation.Count-1) - mutation.Index + 1
	if deletedInside < 0 {
		deletedInside = 0
	}
	return max(0, count-deletedInside)
}

// MaxGroupDepth matches what a spreadsheet outline is usable at: past a few
// levels the controls stop being readable.
const MaxGroupDepth = 8

// MaxGroups bounds one sheet's outline so a runaway client cannot fill the
// layout blob with grouping.
const MaxGroups = 200

// normalizeDimensionGroups drops empty and out-of-bounds groups, removes exact
// duplicates, and works out how deeply each one is nested so the client can
// indent the controls without repeating the calculation.
func normalizeDimensionGroups(input []DimensionGroup, limit int) []DimensionGroup {
	seen := make(map[[2]int]DimensionGroup, len(input))
	for _, group := range input {
		if group.Start < 1 {
			group.Start = 1
		}
		if group.End > limit {
			group.End = limit
		}
		if group.Start >= group.End {
			// A single row or column is not worth an outline control.
			continue
		}
		key := [2]int{group.Start, group.End}
		if existing, duplicate := seen[key]; duplicate {
			group.Collapsed = group.Collapsed || existing.Collapsed
		}
		seen[key] = group
	}
	groups := make([]DimensionGroup, 0, len(seen))
	for _, group := range seen {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Start == groups[j].Start {
			return groups[i].End > groups[j].End
		}
		return groups[i].Start < groups[j].Start
	})
	if len(groups) > MaxGroups {
		groups = groups[:MaxGroups]
	}
	for index := range groups {
		depth := 0
		for other := range groups {
			if other == index {
				continue
			}
			if encloses(groups[other], groups[index]) {
				depth++
			}
		}
		groups[index].Depth = depth
	}
	return groups
}

// encloses reports whether outer wholly contains inner without being the same
// range, which is what makes one group a level deeper than another.
func encloses(outer, inner DimensionGroup) bool {
	if outer.Start == inner.Start && outer.End == inner.End {
		return false
	}
	return outer.Start <= inner.Start && outer.End >= inner.End
}

// applyGroupMutation adds, removes, collapses or expands one outline group.
func applyGroupMutation(groups []DimensionGroup, input SheetLayoutMutation, limit int) ([]DimensionGroup, error) {
	end := input.Start + input.Count - 1
	switch input.Action {
	case "group":
		if input.Count < 2 {
			return nil, fmt.Errorf("%w: a group needs at least two %ss", ErrInvalid, input.Axis)
		}
		next := normalizeDimensionGroups(append(append([]DimensionGroup(nil), groups...), DimensionGroup{Start: input.Start, End: end}), limit)
		for _, group := range next {
			if group.Depth >= MaxGroupDepth {
				return nil, fmt.Errorf("%w: groups can be nested %d levels deep", ErrInvalid, MaxGroupDepth)
			}
		}
		if len(next) > MaxGroups {
			return nil, fmt.Errorf("%w: a sheet can hold %d groups", ErrInvalid, MaxGroups)
		}
		return next, nil
	case "ungroup":
		// The innermost group covering the range is the one being removed, so
		// ungrouping a nested selection peels one level at a time.
		target, found := innermostGroup(groups, input.Start, end)
		if !found {
			return nil, fmt.Errorf("%w: no group covers that range", ErrInvalid)
		}
		next := make([]DimensionGroup, 0, len(groups))
		for _, group := range groups {
			if group.Start == target.Start && group.End == target.End {
				continue
			}
			next = append(next, group)
		}
		return normalizeDimensionGroups(next, limit), nil
	case "collapse", "expand":
		target, found := innermostGroup(groups, input.Start, end)
		if !found {
			return nil, fmt.Errorf("%w: no group covers that range", ErrInvalid)
		}
		next := append([]DimensionGroup(nil), groups...)
		for index := range next {
			if next[index].Start == target.Start && next[index].End == target.End {
				next[index].Collapsed = input.Action == "collapse"
			}
		}
		return normalizeDimensionGroups(next, limit), nil
	}
	return groups, nil
}

// innermostGroup finds the smallest group that covers the range, which is what
// a click on a control inside nested groups should act on.
func innermostGroup(groups []DimensionGroup, start, end int) (DimensionGroup, bool) {
	var best DimensionGroup
	found := false
	for _, group := range groups {
		if group.Start > start || group.End < end {
			continue
		}
		if !found || group.End-group.Start < best.End-best.Start {
			best, found = group, true
		}
	}
	return best, found
}

// transformDimensionGroups moves the outline with the rows or columns it wraps,
// dropping a group whose range is deleted entirely.
func transformDimensionGroups(input []DimensionGroup, mutation StructuralMutation) ([]DimensionGroup, bool) {
	result := make([]DimensionGroup, 0, len(input))
	changed := false
	for _, group := range input {
		start, end, exists := transformGroupInterval(group.Start, group.End, mutation)
		if !exists {
			changed = true
			continue
		}
		if start != group.Start || end != group.End {
			changed = true
		}
		group.Start, group.End = start, end
		result = append(result, group)
	}
	return result, changed
}

func transformGroupInterval(start, end int, mutation StructuralMutation) (int, int, bool) {
	change := formula.StructuralChange{Axis: mutation.Axis, Action: mutation.Action, Index: mutation.Index, Count: mutation.Count}
	return formula.TransformInterval(start, end, change)
}
