package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const MaxStructuralCount = 1_000

func normalizeStructuralMutation(input StructuralMutation) (StructuralMutation, error) {
	input.Axis = strings.ToLower(strings.TrimSpace(input.Axis))
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		return StructuralMutation{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	if input.Axis != "row" && input.Axis != "column" {
		return StructuralMutation{}, fmt.Errorf("%w: axis must be row or column", ErrInvalid)
	}
	if input.Action != "insert" && input.Action != "delete" {
		return StructuralMutation{}, fmt.Errorf("%w: action must be insert or delete", ErrInvalid)
	}
	limit := formula.MaxRows
	if input.Axis == "column" {
		limit = formula.MaxColumns
	}
	if input.Index < 1 || input.Index > limit || input.Count < 1 || input.Count > MaxStructuralCount || input.Index+input.Count-1 > limit {
		return StructuralMutation{}, fmt.Errorf("%w: index and count exceed spreadsheet bounds or the %d-item operation limit", ErrInvalid, MaxStructuralCount)
	}
	return input, nil
}

func formulaStructuralChange(input StructuralMutation, currentSheet, targetSheet string) formula.StructuralChange {
	return formula.StructuralChange{Axis: input.Axis, Action: input.Action, Index: input.Index, Count: input.Count, CurrentSheet: currentSheet, TargetSheet: targetSheet}
}

func structuralPosition(position int, input StructuralMutation) (int, bool) {
	if input.Action == "insert" {
		if position >= input.Index {
			position += input.Count
		}
		return position, true
	}
	deletedEnd := input.Index + input.Count - 1
	if position >= input.Index && position <= deletedEnd {
		return 0, false
	}
	if position > deletedEnd {
		position -= input.Count
	}
	return position, true
}

func transformStructureCells(sheets map[string]Sheet, source map[string]map[cellKey]Cell, targetSheetID string, input StructuralMutation) (map[string]map[cellKey]Cell, error) {
	target, found := sheets[targetSheetID]
	if !found {
		return nil, ErrNotFound
	}
	mergeRanges := make(map[string]MergeMetadata)
	for _, cell := range source[targetSheetID] {
		metadata, merged, err := CellMerge(cell)
		if err != nil {
			return nil, err
		}
		if merged {
			key := fmt.Sprintf("%d:%d:%d:%d", metadata.StartRow, metadata.StartColumn, metadata.EndRow, metadata.EndColumn)
			mergeRanges[key] = metadata
		}
	}
	next := make(map[string]map[cellKey]Cell, len(sheets))
	for sheetID, sheet := range sheets {
		next[sheetID] = make(map[cellKey]Cell, len(source[sheetID]))
		change := formulaStructuralChange(input, sheet.Name, target.Name)
		for _, original := range source[sheetID] {
			if original.SpillSource != "" {
				continue
			}
			cell := cloneCell(original)
			if sheetID == targetSheetID {
				position := cell.Row
				if input.Axis == "column" {
					position = cell.Column
				}
				position, exists := structuralPosition(position, input)
				if !exists {
					continue
				}
				if input.Axis == "row" {
					cell.Row = position
				} else {
					cell.Column = position
				}
				if cell.Row > formula.MaxRows || cell.Column > formula.MaxColumns {
					return nil, fmt.Errorf("%w: insertion would move cells outside spreadsheet bounds", ErrInvalid)
				}
				cell.Style, _ = setMergeMetadata(cell.Style, MergeMetadata{}, false)
			}
			if cell.Formula != "" {
				cell.Formula = formula.TransformStructuralReferences(cell.Formula, change)
				cell.Value, cell.SpillSource = nil, ""
			}
			next[sheetID][cellKey{cell.Row, cell.Column}] = cell
		}
	}
	mergeKeys := make([]string, 0, len(mergeRanges))
	for key := range mergeRanges {
		mergeKeys = append(mergeKeys, key)
	}
	sort.Strings(mergeKeys)
	for _, key := range mergeKeys {
		metadata := mergeRanges[key]
		address := cellrange.Address(metadata.StartRow, metadata.StartColumn) + ":" + cellrange.Address(metadata.EndRow, metadata.EndColumn)
		transformed, exists, err := formula.TransformRangeAddress(address, formulaStructuralChange(input, target.Name, target.Name))
		if err != nil {
			return nil, fmt.Errorf("%w: merged range exceeds spreadsheet bounds", ErrInvalid)
		}
		if !exists {
			continue
		}
		selected, _ := cellrange.Parse(transformed)
		count := int64(selected.End.Row-selected.Start.Row+1) * int64(selected.End.Column-selected.Start.Column+1)
		if count < 2 {
			continue
		}
		if count > MaxPasteCells {
			return nil, fmt.Errorf("%w: transformed merged range exceeds %d cells", ErrInvalid, MaxPasteCells)
		}
		updatedMetadata := mergeMetadataFor(selected)
		for row := selected.Start.Row; row <= selected.End.Row; row++ {
			for column := selected.Start.Column; column <= selected.End.Column; column++ {
				coordinate := cellKey{row, column}
				cell := next[targetSheetID][coordinate]
				cell.SheetID, cell.Row, cell.Column = targetSheetID, row, column
				cell.Style, err = setMergeMetadata(cell.Style, updatedMetadata, true)
				if err != nil {
					return nil, err
				}
				next[targetSheetID][coordinate] = cell
			}
		}
	}
	return next, nil
}

func transformNamedRangeForStructure(item NamedRange, targetSheetID string, input StructuralMutation, actor string, now time.Time) (NamedRange, error) {
	if item.SheetID != targetSheetID || item.Range == "#REF!" {
		return item, nil
	}
	originalRange := item.Range
	selected, exists, err := formula.TransformRangeAddress(item.Range, formulaStructuralChange(input, "", ""))
	if err != nil {
		return NamedRange{}, fmt.Errorf("%w: named range exceeds spreadsheet bounds", ErrInvalid)
	}
	if exists {
		item.Range = selected
	} else {
		item.Range = "#REF!"
	}
	if item.Range == originalRange {
		return item, nil
	}
	item.Revision++
	item.UpdatedBy, item.UpdatedAt = actor, now
	return item, nil
}

func transformValidationForStructure(rule DataValidation, target Sheet, input StructuralMutation, actor string, now time.Time) (DataValidation, bool, error) {
	if rule.SheetID != target.ID {
		return rule, true, nil
	}
	original := cloneDataValidation(rule)
	selected, exists, err := formula.TransformRangeAddress(rule.Range, formulaStructuralChange(input, "", ""))
	if err != nil {
		return DataValidation{}, false, fmt.Errorf("%w: data validation range exceeds spreadsheet bounds", ErrInvalid)
	}
	if !exists {
		return DataValidation{}, false, nil
	}
	rule.Range = selected
	if rule.Formula != "" {
		rule.Formula = formula.TransformStructuralReferences(rule.Formula, formulaStructuralChange(input, target.Name, target.Name))
	}
	normalized, _, err := NormalizeDataValidation(rule)
	if err != nil {
		// A deleted custom-formula dependency makes the rule unusable. Removing
		// the rule is safer than retaining a validation that rejects every edit.
		return DataValidation{}, false, nil
	}
	if normalized.Range == original.Range && normalized.Formula == original.Formula {
		return original, true, nil
	}
	normalized.Revision++
	normalized.UpdatedBy, normalized.UpdatedAt = actor, now
	return normalized, true, nil
}

func transformFilterForStructure(view FilterView, input StructuralMutation, now time.Time) (FilterView, bool) {
	original, err := cellrange.Parse(view.Range)
	if err != nil {
		return FilterView{}, false
	}
	selected, exists, err := formula.TransformRangeAddress(view.Range, formulaStructuralChange(input, "", ""))
	if err != nil || !exists {
		return FilterView{}, false
	}
	view.Range = selected
	if input.Axis == "column" {
		criteria := make([]FilterCriterion, 0, len(view.Criteria))
		for _, criterion := range view.Criteria {
			column, remains := structuralPosition(criterion.Column, input)
			if remains {
				criterion.Column = column
				criteria = append(criteria, criterion)
			}
		}
		view.Criteria = criteria
	} else if view.HeaderRows > 0 {
		headerEnd := original.Start.Row + view.HeaderRows - 1
		headerRange := cellrange.Address(original.Start.Row, original.Start.Column) + ":" + cellrange.Address(headerEnd, original.End.Column)
		transformedHeader, headerExists, _ := formula.TransformRangeAddress(headerRange, formulaStructuralChange(input, "", ""))
		if !headerExists {
			view.HeaderRows = 0
		} else if parsed, parseErr := cellrange.Parse(transformedHeader); parseErr == nil {
			view.HeaderRows = parsed.End.Row - parsed.Start.Row + 1
		}
	}
	normalized, _, err := NormalizeFilterView(view)
	if err != nil {
		return FilterView{}, false
	}
	normalized.UpdatedAt = now
	return normalized, true
}

func transformCommentForStructure(thread CommentThread, targetSheetID string, input StructuralMutation, now time.Time) (CommentThread, error) {
	if thread.SheetID != targetSheetID || thread.Range == "#REF!" {
		return thread, nil
	}
	transformed, exists, err := formula.TransformRangeAddress(thread.Range, formulaStructuralChange(input, "", ""))
	if err != nil {
		return CommentThread{}, fmt.Errorf("%w: comment range exceeds spreadsheet bounds", ErrInvalid)
	}
	if !exists {
		transformed = "#REF!"
	}
	if transformed == thread.Range {
		return thread, nil
	}
	thread.Range, thread.Revision, thread.UpdatedAt = transformed, thread.Revision+1, now
	return thread, nil
}

func applyRecalculatedInputs(cells map[string]map[cellKey]Cell, inputs []CellInput, now time.Time) {
	for _, input := range inputs {
		key := cellKey{input.Row, input.Column}
		cell := Cell{SheetID: input.SheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), SpillSource: input.SpillSource, UpdatedAt: now}
		if isEmptyCell(cell) {
			delete(cells[input.SheetID], key)
		} else {
			cells[input.SheetID][key] = cell
		}
	}
}

func changedCellCount(before, after map[string]map[cellKey]Cell) int {
	changed := 0
	for sheetID, cells := range before {
		seen := make(map[cellKey]struct{}, len(cells)+len(after[sheetID]))
		for key := range cells {
			seen[key] = struct{}{}
		}
		for key := range after[sheetID] {
			seen[key] = struct{}{}
		}
		for key := range seen {
			if !cellsEqual(cells[key], after[sheetID][key]) {
				changed++
			}
		}
	}
	return changed
}

func cloneFilterMap(source map[string]FilterView) map[string]FilterView {
	result := make(map[string]FilterView, len(source))
	for id, view := range source {
		result[id] = cloneFilterView(view)
	}
	return result
}

func cloneValidationMap(source map[string]DataValidation) map[string]DataValidation {
	result := make(map[string]DataValidation, len(source))
	for id, rule := range source {
		result[id] = cloneDataValidation(rule)
	}
	return result
}

func cloneConditionalMap(source map[string]ConditionalFormat) map[string]ConditionalFormat {
	return cloneConditionalFormatMap(source)
}

func cloneNamedRangeMap(source map[string]NamedRange) map[string]NamedRange {
	result := make(map[string]NamedRange, len(source))
	for id, item := range source {
		result[id] = cloneNamedRange(item)
	}
	return result
}

func cloneChartMap(source map[string]Chart) map[string]Chart {
	result := make(map[string]Chart, len(source))
	for id, item := range source {
		result[id] = cloneChart(item)
	}
	return result
}

func (r *MemoryRepository) ApplyStructure(_ context.Context, raw StructuralMutation) (MutationResult, error) {
	input, err := normalizeStructuralMutation(raw)
	if err != nil {
		return MutationResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, target, err := r.sheetState(input.SheetID)
	if err != nil {
		return MutationResult{}, err
	}
	idempotencyKey := input.ActorID + ":" + input.IdempotencyKey
	if existing, found := state.idempotent[idempotencyKey]; found {
		existing.Duplicate = true
		return existing, nil
	}
	if input.BaseVersion != state.workbook.Version {
		return MutationResult{}, ErrVersionConflict
	}
	now := r.now()
	nextNames := cloneNamedRangeMap(r.namedRanges)
	for id, item := range nextNames {
		if item.WorkbookID != state.workbook.ID {
			continue
		}
		item, err = transformNamedRangeForStructure(item, target.ID, input, input.ActorID, now)
		if err != nil {
			return MutationResult{}, err
		}
		nextNames[id] = item
	}
	nextValidations := cloneValidationMap(r.validations)
	for id, rule := range nextValidations {
		if rule.SheetID != target.ID {
			continue
		}
		updated, remains, transformErr := transformValidationForStructure(rule, target, input, input.ActorID, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		if remains {
			nextValidations[id] = updated
		} else {
			delete(nextValidations, id)
		}
	}
	nextConditionalFormats := cloneConditionalMap(r.conditionalFormats)
	for id, rule := range nextConditionalFormats {
		if rule.SheetID != target.ID {
			continue
		}
		updated, remains, transformErr := transformConditionalFormatForStructure(rule, input, input.ActorID, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		if remains {
			nextConditionalFormats[id] = updated
		} else {
			delete(nextConditionalFormats, id)
		}
	}
	nextFilters := cloneFilterMap(r.filters)
	for id, view := range nextFilters {
		if view.SheetID != target.ID {
			continue
		}
		updated, remains := transformFilterForStructure(view, input, now)
		if remains {
			nextFilters[id] = updated
		} else {
			delete(nextFilters, id)
		}
	}
	nextComments := make(map[string]CommentThread, len(r.comments))
	for id, thread := range r.comments {
		nextComments[id] = cloneCommentThread(thread)
		if thread.WorkbookID != state.workbook.ID || thread.SheetID != target.ID {
			continue
		}
		updated, transformErr := transformCommentForStructure(thread, target.ID, input, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		nextComments[id] = updated
	}
	nextCharts := cloneChartMap(r.charts)
	for id, chart := range nextCharts {
		if chart.WorkbookID != state.workbook.ID {
			continue
		}
		updated, transformErr := transformChartForStructure(chart, target.ID, input, input.ActorID, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		nextCharts[id] = updated
	}
	nextPivots := clonePivotMap(r.pivots)
	for id, pivot := range nextPivots {
		if pivot.WorkbookID != state.workbook.ID {
			continue
		}
		updated, transformErr := transformPivotForStructure(pivot, target.ID, input, input.ActorID, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		nextPivots[id] = updated
	}
	nextCells, err := transformStructureCells(state.sheets, state.cells, target.ID, input)
	if err != nil {
		return MutationResult{}, err
	}
	expanded, recalculated, formulaErrors, err := recalculateCellInputs(state.sheets, nextCells, target.ID, nil, true, formulaNamedRanges(namedRangesFromMap(nextNames, state.workbook.ID)))
	if err != nil {
		return MutationResult{}, err
	}
	applyRecalculatedInputs(nextCells, expanded, now)
	backup := Version{ID: identity.New(), WorkbookID: state.workbook.ID, WorkbookVersion: state.workbook.Version, Name: structureBackupName(input), ActorID: input.ActorID, CreatedAt: now}
	state.versions = append(state.versions, snapshot{version: backup, workbook: state.workbook, sheets: cloneSheets(state.sheets), cells: cloneAllCells(state.cells), filters: cloneFiltersForSheets(r.filters, state.sheets), validations: cloneValidationsForSheets(r.validations, state.sheets), conditionalFormats: cloneConditionalFormatsForSheets(r.conditionalFormats, state.sheets), namedRanges: cloneNamedRangesForWorkbook(r.namedRanges, state.workbook.ID), charts: cloneChartsForWorkbook(r.charts, state.workbook.ID), pivots: clonePivotsForWorkbook(r.pivots, state.workbook.ID)})
	appliedCells := changedCellCount(state.cells, nextCells)
	target.Layout = transformLayoutForStructure(target.Layout, input)
	state.sheets[target.ID] = target
	nextNotifications := make(map[string]MentionNotification, len(r.notifications))
	for id, notification := range r.notifications {
		copy := cloneMentionNotification(notification)
		if thread, found := nextComments[notification.ThreadID]; found {
			copy.Range = thread.Range
		}
		nextNotifications[id] = copy
	}
	state.cells, r.namedRanges, r.validations, r.conditionalFormats, r.filters, r.comments, r.notifications, r.charts, r.pivots = nextCells, nextNames, nextValidations, nextConditionalFormats, nextFilters, nextComments, nextNotifications, nextCharts, nextPivots
	for pivotID, pivot := range nextPivots {
		if pivot.WorkbookID == state.workbook.ID {
			delete(r.pivotCache, pivotID)
		}
	}
	r.bump(state)
	for id, item := range r.namedRanges {
		if item.WorkbookID == state.workbook.ID {
			item.WorkbookVersion = state.workbook.Version
			r.namedRanges[id] = item
		}
	}
	for id, rule := range r.validations {
		if rule.WorkbookID == state.workbook.ID {
			rule.WorkbookVersion = state.workbook.Version
			r.validations[id] = rule
		}
	}
	for id, rule := range r.conditionalFormats {
		if rule.WorkbookID == state.workbook.ID {
			rule.WorkbookVersion = state.workbook.Version
			r.conditionalFormats[id] = rule
		}
	}
	result := MutationResult{OperationID: identity.New(), WorkbookID: state.workbook.ID, SheetID: target.ID, BaseVersion: input.BaseVersion, ServerVersion: state.workbook.Version, AppliedCells: appliedCells, RecalculatedCells: recalculated, FormulaErrors: formulaErrors, BackupVersionID: backup.ID, StructuralAxis: input.Axis, StructuralAction: input.Action, StructuralIndex: input.Index, StructuralCount: input.Count, CreatedAt: now}
	state.operations = append(state.operations, operation{result: result, actorID: input.ActorID, clientID: input.ClientID, operationType: "structure." + input.Axis + "." + input.Action, structural: true})
	state.idempotent[idempotencyKey] = result
	return result, nil
}

func namedRangesFromMap(source map[string]NamedRange, workbookID string) []NamedRange {
	items := make([]NamedRange, 0)
	for _, item := range source {
		if item.WorkbookID == workbookID {
			items = append(items, item)
		}
	}
	return items
}

func structureBackupName(input StructuralMutation) string {
	axis := "행"
	if input.Axis == "column" {
		axis = "열"
	}
	action := "삽입"
	if input.Action == "delete" {
		action = "삭제"
	}
	return fmt.Sprintf("%s %s 전 자동 백업", axis, action)
}

func structuralOperationDocument(input StructuralMutation, result MutationResult) operationDocument {
	return operationDocument{AppliedCells: result.AppliedCells, RecalculatedCells: result.RecalculatedCells, FormulaErrors: result.FormulaErrors, BackupVersionID: result.BackupVersionID, StructuralAxis: input.Axis, StructuralAction: input.Action, StructuralIndex: input.Index, StructuralCount: input.Count}
}

func marshalStructuralOperation(input StructuralMutation, result MutationResult) json.RawMessage {
	encoded, _ := json.Marshal(structuralOperationDocument(input, result))
	return encoded
}
