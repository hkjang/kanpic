package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/identity"
)

func cloneSheetTable(item SheetTable) SheetTable { return item }

func (r *MemoryRepository) sheetTablesForWorkbookLocked(workbookID string) []SheetTable {
	items := make([]SheetTable, 0)
	for _, item := range r.sheetTables {
		if item.WorkbookID == workbookID {
			items = append(items, cloneSheetTable(item))
		}
	}
	sort.Slice(items, func(left, right int) bool { return items[left].Name < items[right].Name })
	return items
}

func (r *MemoryRepository) CreateSheetTable(_ context.Context, workbookID, actor string, input CreateSheetTableInput) (SheetTable, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return SheetTable{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return SheetTable{}, ErrNotFound
	}
	for _, item := range r.sheetTables {
		if item.WorkbookID == workbookID && item.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			item.WorkbookVersion = state.workbook.Version
			return cloneSheetTable(item), nil
		}
	}
	if _, known := state.sheets[input.SheetID]; !known {
		return SheetTable{}, fmt.Errorf("%w: unknown sheet", ErrInvalid)
	}
	existing := r.sheetTablesForWorkbookLocked(workbookID)
	if len(existing) >= MaxTables {
		return SheetTable{}, fmt.Errorf("%w: a workbook may contain at most %d tables", ErrInvalid, MaxTables)
	}
	headerRow := true
	if input.HeaderRow != nil {
		headerRow = *input.HeaderRow
	}
	item, err := normalizeSheetTable(SheetTable{
		WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: strings.TrimSpace(input.IdempotencyKey),
		Name: input.Name, Range: input.Range, HeaderRow: headerRow, Theme: input.Theme,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		return SheetTable{}, err
	}
	if err := checkTableConflicts(existing, item, ""); err != nil {
		return SheetTable{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.sheetTables[item.ID] = item
	if err := r.recalculateAllLocked(state); err != nil {
		delete(r.sheetTables, item.ID)
		return SheetTable{}, err
	}
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.sheetTables[item.ID] = item
	return cloneSheetTable(item), nil
}

func (r *MemoryRepository) ListSheetTables(_ context.Context, workbookID string) ([]SheetTable, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return nil, ErrNotFound
	}
	items := r.sheetTablesForWorkbookLocked(workbookID)
	for index := range items {
		items[index].WorkbookVersion = state.workbook.Version
	}
	return items, nil
}

func (r *MemoryRepository) GetSheetTable(_ context.Context, id string) (SheetTable, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.sheetTables[id]
	if !found {
		return SheetTable{}, ErrNotFound
	}
	if state, known := r.workbooks[item.WorkbookID]; known {
		item.WorkbookVersion = state.workbook.Version
	}
	return cloneSheetTable(item), nil
}

func (r *MemoryRepository) UpdateSheetTable(_ context.Context, id, actor string, input UpdateSheetTableInput) (SheetTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.sheetTables[id]
	if !found {
		return SheetTable{}, ErrNotFound
	}
	state, known := r.workbooks[current.WorkbookID]
	if !known {
		return SheetTable{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return SheetTable{}, ErrRevision
	}
	updated := current
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.HeaderRow != nil {
		updated.HeaderRow = *input.HeaderRow
	}
	if input.Theme != nil {
		updated.Theme = *input.Theme
	}
	normalized, err := normalizeSheetTable(updated)
	if err != nil {
		return SheetTable{}, err
	}
	if err := checkTableConflicts(r.sheetTablesForWorkbookLocked(current.WorkbookID), normalized, id); err != nil {
		return SheetTable{}, err
	}
	normalized.Revision, normalized.UpdatedBy, normalized.UpdatedAt = current.Revision+1, actor, r.now()
	r.sheetTables[id] = normalized
	if err := r.recalculateAllLocked(state); err != nil {
		r.sheetTables[id] = current
		return SheetTable{}, err
	}
	r.bump(state)
	normalized.WorkbookVersion = state.workbook.Version
	r.sheetTables[id] = normalized
	return cloneSheetTable(normalized), nil
}

func (r *MemoryRepository) DeleteSheetTable(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.sheetTables[id]
	if !found {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	state, known := r.workbooks[current.WorkbookID]
	if !known {
		return ErrNotFound
	}
	delete(r.sheetTables, id)
	if err := r.recalculateAllLocked(state); err != nil {
		r.sheetTables[id] = current
		return err
	}
	r.bump(state)
	return nil
}
