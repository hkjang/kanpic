package workbook

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const (
	MaxNamedRanges        = 1_000
	maxSpreadsheetRows    = 1_048_576
	maxSpreadsheetColumns = 16_384
)

func normalizeNamedRangeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) == 0 || len(runes) > 255 || (!unicode.IsLetter(runes[0]) && runes[0] != '_') {
		return "", fmt.Errorf("%w: name must start with a letter or underscore and contain at most 255 characters", ErrInvalid)
	}
	for _, character := range runes[1:] {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' && character != '.' {
			return "", fmt.Errorf("%w: name may contain only letters, numbers, underscores, and periods", ErrInvalid)
		}
	}
	upper := strings.ToUpper(value)
	if upper == "TRUE" || upper == "FALSE" || looksLikeCellReference(upper) {
		return "", fmt.Errorf("%w: name conflicts with a cell reference or reserved value", ErrInvalid)
	}
	return value, nil
}

func looksLikeCellReference(value string) bool {
	index := 0
	column := uint64(0)
	for index < len(value) && value[index] >= 'A' && value[index] <= 'Z' {
		column = column*26 + uint64(value[index]-'A'+1)
		index++
	}
	if index == 0 || index == len(value) || column > maxSpreadsheetColumns {
		return false
	}
	digitIndex := index
	for index < len(value) {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
		index++
	}
	row, err := strconv.ParseUint(value[digitIndex:], 10, 64)
	return err == nil && row > 0 && row <= maxSpreadsheetRows
}

func normalizeNamedRangeAddress(value string) (string, error) {
	selected, err := cellrange.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("%w: range must be a valid A1 cell or range", ErrInvalid)
	}
	if selected.End.Row > maxSpreadsheetRows || selected.End.Column > maxSpreadsheetColumns {
		return "", fmt.Errorf("%w: range exceeds XFD1048576", ErrInvalid)
	}
	start := cellrange.Address(selected.Start.Row, selected.Start.Column)
	end := cellrange.Address(selected.End.Row, selected.End.Column)
	if start == end {
		return start, nil
	}
	return start + ":" + end, nil
}

// namedRangeFromInput builds the name a create request describes so both
// repositories agree on every field it carries.
func namedRangeFromInput(workbookID, key, actor string, input CreateNamedRangeInput) (NamedRange, error) {
	return normalizeNamedRange(NamedRange{
		WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: key,
		Name: input.Name, Range: input.Range, CreatedBy: actor, UpdatedBy: actor,
	})
}

func normalizeNamedRange(input NamedRange) (NamedRange, error) {
	name, err := normalizeNamedRangeName(input.Name)
	if err != nil {
		return NamedRange{}, err
	}
	selected, err := normalizeNamedRangeAddress(input.Range)
	if err != nil {
		return NamedRange{}, err
	}
	if strings.TrimSpace(input.SheetID) == "" {
		return NamedRange{}, fmt.Errorf("%w: sheet_id is required", ErrInvalid)
	}
	input.Name, input.Range, input.SheetID = name, selected, strings.TrimSpace(input.SheetID)
	return input, nil
}

func normalizeStoredNamedRange(input NamedRange) (NamedRange, error) {
	if strings.TrimSpace(input.Range) != "#REF!" {
		return normalizeNamedRange(input)
	}
	originalRange := input.Range
	input.Range = "A1"
	normalized, err := normalizeNamedRange(input)
	if err != nil {
		return NamedRange{}, err
	}
	normalized.Range = originalRange
	return normalized, nil
}

func cloneNamedRange(value NamedRange) NamedRange { return value }

func (r *MemoryRepository) CreateNamedRange(_ context.Context, workbookID, actor string, input CreateNamedRangeInput) (NamedRange, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return NamedRange{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return NamedRange{}, ErrNotFound
	}
	for _, item := range r.namedRanges {
		if item.WorkbookID == workbookID && item.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			item.WorkbookVersion = state.workbook.Version
			return cloneNamedRange(item), nil
		}
	}
	if len(r.namedRangesForWorkbookLocked(workbookID)) >= MaxNamedRanges {
		return NamedRange{}, fmt.Errorf("%w: a workbook may contain at most %d named ranges", ErrInvalid, MaxNamedRanges)
	}
	item, err := namedRangeFromInput(workbookID, strings.TrimSpace(input.IdempotencyKey), actor, input)
	if err != nil {
		return NamedRange{}, err
	}
	if err := r.validateNamedRangeTargetLocked(state, item, ""); err != nil {
		return NamedRange{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.namedRanges[item.ID] = item
	if err := r.recalculateAllLocked(state); err != nil {
		delete(r.namedRanges, item.ID)
		return NamedRange{}, err
	}
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.namedRanges[item.ID] = item
	return cloneNamedRange(item), nil
}

// buildImportedNamedRanges turns the names a file carried into stored ones,
// resolving each file-level sheet name to the sheet the import just created.
// A name kanpic cannot hold is dropped rather than failing the whole import:
// the preview already told the reader which ones would not survive.
func buildImportedNamedRanges(workbookID, actor string, imported []ImportNamedRange, sheetIDsByName map[string]string, now time.Time) []NamedRange {
	if len(imported) == 0 {
		return nil
	}
	items := make([]NamedRange, 0, len(imported))
	taken := make(map[string]struct{}, len(imported))
	for index, source := range imported {
		if len(items) >= MaxNamedRanges {
			break
		}
		sheetID, known := sheetIDsByName[source.SheetName]
		if !known {
			continue
		}
		item, err := namedRangeFromInput(workbookID, fmt.Sprintf("import:%d", index), actor, CreateNamedRangeInput{Name: source.Name, SheetID: sheetID, Range: source.Range})
		if err != nil {
			continue
		}
		key := strings.ToUpper(item.Name)
		if _, duplicate := taken[key]; duplicate {
			continue
		}
		taken[key] = struct{}{}
		item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
		items = append(items, item)
	}
	return items
}

func (r *MemoryRepository) ListNamedRanges(_ context.Context, workbookID string) ([]NamedRange, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return nil, ErrNotFound
	}
	items := r.namedRangesForWorkbookLocked(workbookID)
	for index := range items {
		items[index].WorkbookVersion = state.workbook.Version
	}
	return items, nil
}

func (r *MemoryRepository) GetNamedRange(_ context.Context, id string) (NamedRange, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.namedRanges[id]
	if !found {
		return NamedRange{}, ErrNotFound
	}
	state, found := r.workbooks[item.WorkbookID]
	if !found {
		return NamedRange{}, ErrNotFound
	}
	item.WorkbookVersion = state.workbook.Version
	return cloneNamedRange(item), nil
}

func (r *MemoryRepository) UpdateNamedRange(_ context.Context, id, actor string, input UpdateNamedRangeInput) (NamedRange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.namedRanges[id]
	if !found {
		return NamedRange{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return NamedRange{}, ErrRevision
	}
	state, found := r.workbooks[current.WorkbookID]
	if !found {
		return NamedRange{}, ErrNotFound
	}
	updated := current
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.SheetID != nil {
		updated.SheetID = *input.SheetID
	}
	if input.Range != nil {
		updated.Range = *input.Range
	}
	var err error
	updated, err = normalizeNamedRange(updated)
	if err != nil {
		return NamedRange{}, err
	}
	if err := r.validateNamedRangeTargetLocked(state, updated, id); err != nil {
		return NamedRange{}, err
	}
	updated.Revision, updated.UpdatedBy, updated.UpdatedAt = current.Revision+1, actor, r.now()
	previousCells := cloneAllCells(state.cells)
	if !strings.EqualFold(current.Name, updated.Name) {
		for sheetID, cells := range state.cells {
			for key, cell := range cells {
				renamed := formula.RenameNamedRangeReferences(cell.Formula, current.Name, updated.Name)
				if renamed != cell.Formula {
					cell.Formula = renamed
					state.cells[sheetID][key] = cell
				}
			}
		}
	}
	r.namedRanges[id] = updated
	if err := r.recalculateAllLocked(state); err != nil {
		r.namedRanges[id] = current
		state.cells = previousCells
		return NamedRange{}, err
	}
	r.bump(state)
	updated.WorkbookVersion = state.workbook.Version
	r.namedRanges[id] = updated
	return cloneNamedRange(updated), nil
}

func (r *MemoryRepository) DeleteNamedRange(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, found := r.namedRanges[id]
	if !found {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != item.Revision {
		return ErrRevision
	}
	state, found := r.workbooks[item.WorkbookID]
	if !found {
		return ErrNotFound
	}
	delete(r.namedRanges, id)
	if err := r.recalculateAllLocked(state); err != nil {
		r.namedRanges[id] = item
		return err
	}
	r.bump(state)
	return nil
}

func (r *MemoryRepository) validateNamedRangeTargetLocked(state *workbookState, item NamedRange, excludingID string) error {
	if _, found := state.sheets[item.SheetID]; !found {
		return fmt.Errorf("%w: named range sheet does not belong to the workbook", ErrInvalid)
	}
	for id, existing := range r.namedRanges {
		if id != excludingID && existing.WorkbookID == item.WorkbookID && strings.EqualFold(existing.Name, item.Name) {
			return ErrDuplicateName
		}
	}
	return nil
}

func (r *MemoryRepository) namedRangesForWorkbookLocked(workbookID string) []NamedRange {
	items := make([]NamedRange, 0)
	for _, item := range r.namedRanges {
		if item.WorkbookID == workbookID {
			items = append(items, cloneNamedRange(item))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if strings.EqualFold(items[i].Name, items[j].Name) {
			return items[i].ID < items[j].ID
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}

func formulaNamedRanges(items []NamedRange) map[string]formula.NamedRange {
	result := make(map[string]formula.NamedRange, len(items))
	for _, item := range items {
		result[item.Name] = formula.NamedRange{SheetID: item.SheetID, Range: item.Range}
	}
	return result
}

func (r *MemoryRepository) recalculateAllLocked(state *workbookState) error {
	currentSheetID := ""
	for sheetID := range state.sheets {
		currentSheetID = sheetID
		break
	}
	expanded, _, _, err := recalculateCellInputs(state.sheets, state.cells, currentSheetID, nil, true, nameContext{Ranges: formulaNamedRanges(r.namedRangesForWorkbookLocked(state.workbook.ID)), Functions: r.namedFunctionDefinitionsLocked(state.workbook.ID), Tables: formulaTables(r.sheetTablesForWorkbookLocked(state.workbook.ID)), Imports: r.importsForLocked(state.workbook.ID, state.cells, nil), External: r.externalForLocked(state.cells, nil)})
	if err != nil {
		return err
	}
	now := r.now()
	for _, input := range expanded {
		key := cellKey{input.Row, input.Column}
		cell := Cell{SheetID: input.SheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), Note: input.Note, SpillSource: input.SpillSource, UpdatedAt: now}
		if isEmptyCell(cell) {
			delete(state.cells[input.SheetID], key)
		} else {
			state.cells[input.SheetID][key] = cell
		}
	}
	return nil
}

func cloneNamedRangesForWorkbook(source map[string]NamedRange, workbookID string) map[string]NamedRange {
	result := make(map[string]NamedRange)
	for id, item := range source {
		if item.WorkbookID == workbookID {
			result[id] = cloneNamedRange(item)
		}
	}
	return result
}
