package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

func (r *MemoryRepository) CreateConditionalFormat(_ context.Context, sheetID, actor string, input CreateConditionalFormatInput) (ConditionalFormat, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return ConditionalFormat{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return ConditionalFormat{}, err
	}
	for _, existing := range r.conditionalFormats {
		if existing.SheetID == sheetID && existing.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			return cloneConditionalFormat(existing), nil
		}
	}
	count := 0
	for _, existing := range r.conditionalFormats {
		if existing.SheetID == sheetID {
			count++
		}
	}
	if count >= MaxConditionalFormats {
		return ConditionalFormat{}, fmt.Errorf("%w: a sheet may contain at most %d conditional formats", ErrInvalid, MaxConditionalFormats)
	}
	rule, _, err := NewConditionalFormat(sheetID, actor, input)
	if err != nil {
		return ConditionalFormat{}, err
	}
	now := r.now()
	rule.ID, rule.WorkbookID, rule.Revision = identity.New(), state.workbook.ID, 1
	rule.CreatedAt, rule.UpdatedAt = now, now
	r.bump(state)
	rule.WorkbookVersion = state.workbook.Version
	r.conditionalFormats[rule.ID] = cloneConditionalFormat(rule)
	return cloneConditionalFormat(rule), nil
}

func (r *MemoryRepository) ListConditionalFormats(_ context.Context, sheetID string) ([]ConditionalFormat, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return nil, err
	}
	items := make([]ConditionalFormat, 0)
	for _, rule := range r.conditionalFormats {
		if rule.SheetID == sheetID {
			item := cloneConditionalFormat(rule)
			item.WorkbookVersion = state.workbook.Version
			items = append(items, item)
		}
	}
	sortConditionalFormats(items)
	return items, nil
}

func (r *MemoryRepository) GetConditionalFormat(_ context.Context, id string) (ConditionalFormat, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, found := r.conditionalFormats[id]
	if !found {
		return ConditionalFormat{}, ErrNotFound
	}
	state, _, err := r.sheetState(rule.SheetID)
	if err != nil {
		return ConditionalFormat{}, err
	}
	rule.WorkbookVersion = state.workbook.Version
	return cloneConditionalFormat(rule), nil
}

func (r *MemoryRepository) EvaluateConditionalFormats(_ context.Context, sheetID string, requested cellrange.Range) (ConditionalFormatEvaluation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return ConditionalFormatEvaluation{}, err
	}
	sources := make([]conditionalFormatSource, 0)
	for _, rule := range r.conditionalFormats {
		if rule.SheetID != sheetID {
			continue
		}
		selected, _ := cellrange.Parse(rule.Range)
		cells := make([]Cell, 0)
		for _, cell := range state.cells[sheetID] {
			if selected.Contains(cell.Row, cell.Column) {
				cells = append(cells, cloneCell(cell))
			}
		}
		sources = append(sources, conditionalFormatSource{Rule: cloneConditionalFormat(rule), Cells: cells})
	}
	return EvaluateConditionalFormats(sheetID, state.workbook.Version, requested, sources)
}

func (r *MemoryRepository) UpdateConditionalFormat(_ context.Context, id, actor string, input UpdateConditionalFormatInput) (ConditionalFormat, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.conditionalFormats[id]
	if !found {
		return ConditionalFormat{}, ErrNotFound
	}
	state, _, err := r.sheetState(current.SheetID)
	if err != nil {
		return ConditionalFormat{}, err
	}
	updated, _, err := ApplyConditionalFormatUpdate(current, actor, input)
	if err != nil {
		return ConditionalFormat{}, err
	}
	updated.Revision, updated.UpdatedAt = current.Revision+1, r.now()
	r.bump(state)
	updated.WorkbookVersion = state.workbook.Version
	r.conditionalFormats[id] = cloneConditionalFormat(updated)
	return cloneConditionalFormat(updated), nil
}

func (r *MemoryRepository) DeleteConditionalFormat(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule, found := r.conditionalFormats[id]
	if !found {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != rule.Revision {
		return ErrRevision
	}
	state, _, err := r.sheetState(rule.SheetID)
	if err != nil {
		return err
	}
	delete(r.conditionalFormats, id)
	r.bump(state)
	return nil
}

func sortConditionalFormats(items []ConditionalFormat) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority == items[j].Priority {
			if items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].Priority < items[j].Priority
	})
}

func cloneConditionalFormatsForSheets(source map[string]ConditionalFormat, sheets map[string]Sheet) map[string]ConditionalFormat {
	result := make(map[string]ConditionalFormat)
	for id, rule := range source {
		if _, found := sheets[rule.SheetID]; found {
			result[id] = cloneConditionalFormat(rule)
		}
	}
	return result
}

func cloneConditionalFormatMap(source map[string]ConditionalFormat) map[string]ConditionalFormat {
	result := make(map[string]ConditionalFormat, len(source))
	for id, rule := range source {
		result[id] = cloneConditionalFormat(rule)
	}
	return result
}
