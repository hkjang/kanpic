package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/identity"
)

func (r *MemoryRepository) SetWorkbookFavorite(_ context.Context, workbookID, userID string, favorite bool) error {
	actor := strings.TrimSpace(userID)
	if actor == "" {
		return fmt.Errorf("%w: an actor is required to change a favorite", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.workbooks[workbookID]; !ok {
		return ErrNotFound
	}
	key := strings.ToLower(actor)
	if r.favorites[key] == nil {
		r.favorites[key] = make(map[string]bool)
	}
	if favorite {
		r.favorites[key][workbookID] = true
		return nil
	}
	delete(r.favorites[key], workbookID)
	return nil
}

func (r *MemoryRepository) WorkbookFavorites(_ context.Context, userID string) (map[string]bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	favorites := make(map[string]bool)
	for id, marked := range r.favorites[strings.ToLower(strings.TrimSpace(userID))] {
		if marked {
			favorites[id] = true
		}
	}
	return favorites, nil
}

func (r *MemoryRepository) ListDeletedWorkbooks(_ context.Context, workspaceID string, principal AccessPrincipal) ([]Workbook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	identities := make(map[string]struct{}, 2)
	for _, identity := range principal.identities() {
		identities[identity] = struct{}{}
	}
	items := make([]Workbook, 0)
	for _, state := range r.trash {
		if workspaceID != "" && state.workbook.WorkspaceID != workspaceID {
			continue
		}
		if _, owned := identities[strings.ToLower(state.workbook.OwnerID)]; !owned && !principal.Admin {
			continue
		}
		item := state.workbook
		item.Sheets = nil
		item.DeletedAt, item.DeletedBy = state.deletedAt, state.deletedBy
		item.AccessRole, item.AccessSource = RoleOwner, AccessSourceOwner
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt.After(*items[j].DeletedAt) })
	return items, nil
}

func (r *MemoryRepository) RestoreWorkbook(_ context.Context, workbookID, _ string) (Workbook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.trash[workbookID]
	if !ok {
		return Workbook{}, ErrNotFound
	}
	state.deletedAt, state.deletedBy = nil, ""
	state.workbook.UpdatedAt = r.now()
	delete(r.trash, workbookID)
	r.workbooks[workbookID] = state
	for sheetID := range state.sheets {
		r.sheetToWB[sheetID] = workbookID
	}
	return r.workbookWithSheets(state), nil
}

func (r *MemoryRepository) PurgeWorkbook(_ context.Context, workbookID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.trash[workbookID]
	if !ok {
		return ErrNotFound
	}
	for sheetID := range state.sheets {
		delete(r.sheetToWB, sheetID)
	}
	delete(r.trash, workbookID)
	delete(r.shares, workbookID)
	for id, request := range r.accessRequests {
		if request.WorkbookID == workbookID {
			delete(r.accessRequests, id)
		}
	}
	for user := range r.favorites {
		delete(r.favorites[user], workbookID)
	}
	return nil
}

func (r *MemoryRepository) SheetStats(_ context.Context, workbookID string) ([]SheetStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return nil, ErrNotFound
	}
	items := make([]SheetStats, 0, len(state.sheets))
	for sheetID, sheet := range state.sheets {
		stats := SheetStats{SheetID: sheetID, Name: sheet.Name, Position: sheet.Position, Hidden: sheet.Hidden, Color: sheet.Color}
		for _, cell := range state.cells[sheetID] {
			stats.NonEmptyCells++
			if strings.TrimSpace(cell.Formula) != "" {
				stats.FormulaCells++
			}
			if cell.Row > stats.MaxRow {
				stats.MaxRow = cell.Row
			}
			if cell.Column > stats.MaxColumn {
				stats.MaxColumn = cell.Column
			}
			if stats.UpdatedAt == nil || cell.UpdatedAt.After(*stats.UpdatedAt) {
				updated := cell.UpdatedAt
				stats.UpdatedAt = &updated
			}
		}
		items = append(items, stats)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Position < items[j].Position })
	return items, nil
}

func (r *MemoryRepository) CopySheetToWorkbook(_ context.Context, sheetID string, input CopySheetInput) (Sheet, error) {
	normalized, err := validateCopySheetInput(input)
	if err != nil {
		return Sheet{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sourceWorkbookID, ok := r.sheetToWB[sheetID]
	if !ok {
		return Sheet{}, ErrNotFound
	}
	source := r.workbooks[sourceWorkbookID]
	target, ok := r.workbooks[normalized.TargetWorkbookID]
	if !ok {
		return Sheet{}, ErrNotFound
	}
	sheet, ok := source.sheets[sheetID]
	if !ok {
		return Sheet{}, ErrNotFound
	}
	taken := make(map[string]struct{}, len(target.sheets))
	for _, existing := range target.sheets {
		taken[strings.ToLower(existing.Name)] = struct{}{}
	}
	fallback := sheet.Name
	if normalized.TargetWorkbookID == sourceWorkbookID {
		fallback = sheet.Name + " 복사본"
	}
	now := r.now()
	created := Sheet{
		ID: identity.New(), WorkbookID: normalized.TargetWorkbookID, Name: availableSheetName(normalized.Name, fallback, taken),
		Position: len(target.sheets), Color: sheet.Color, Hidden: false, Layout: sheet.Layout, CreatedAt: now,
	}
	target.sheets[created.ID] = created
	target.cells[created.ID] = make(map[cellKey]Cell, len(source.cells[sheetID]))
	for coordinate, cell := range source.cells[sheetID] {
		copied := cloneCell(cell)
		copied.SheetID = created.ID
		copied.UpdatedAt = now
		target.cells[created.ID][coordinate] = copied
	}
	r.sheetToWB[created.ID] = normalized.TargetWorkbookID
	r.bump(target)
	return created, nil
}
