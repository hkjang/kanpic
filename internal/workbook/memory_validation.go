package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

func (r *MemoryRepository) CreateDataValidation(_ context.Context, sheetID, actor string, input CreateDataValidationInput) (DataValidation, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return DataValidation{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return DataValidation{}, err
	}
	for _, existing := range r.validations {
		if existing.SheetID == sheetID && existing.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			return cloneDataValidation(existing), nil
		}
	}
	rule, selected, err := NewDataValidation(sheetID, actor, input)
	if err != nil {
		return DataValidation{}, err
	}
	if err := r.ensureValidationRangeAvailableLocked(sheetID, "", selected); err != nil {
		return DataValidation{}, err
	}
	now := r.now()
	rule.ID = identity.New()
	rule.WorkbookID = state.workbook.ID
	rule.Revision = 1
	rule.CreatedAt = now
	rule.UpdatedAt = now
	r.bump(state)
	rule.WorkbookVersion = state.workbook.Version
	r.validations[rule.ID] = cloneDataValidation(rule)
	return cloneDataValidation(rule), nil
}

func (r *MemoryRepository) ListDataValidations(_ context.Context, sheetID string) ([]DataValidation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return nil, err
	}
	result := make([]DataValidation, 0)
	for _, rule := range r.validations {
		if rule.SheetID == sheetID {
			item := cloneDataValidation(rule)
			item.WorkbookVersion = state.workbook.Version
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Range == result[j].Range {
			return result[i].CreatedAt.Before(result[j].CreatedAt)
		}
		return result[i].Range < result[j].Range
	})
	return result, nil
}

func (r *MemoryRepository) GetDataValidation(_ context.Context, id string) (DataValidation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.validations[id]
	if !ok {
		return DataValidation{}, ErrNotFound
	}
	state, _, err := r.sheetState(rule.SheetID)
	if err != nil {
		return DataValidation{}, err
	}
	rule.WorkbookVersion = state.workbook.Version
	return cloneDataValidation(rule), nil
}

func (r *MemoryRepository) UpdateDataValidation(_ context.Context, id, actor string, input UpdateDataValidationInput) (DataValidation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.validations[id]
	if !ok {
		return DataValidation{}, ErrNotFound
	}
	state, _, err := r.sheetState(current.SheetID)
	if err != nil {
		return DataValidation{}, err
	}
	current.WorkbookVersion = state.workbook.Version
	updated, selected, err := ApplyDataValidationUpdate(current, actor, input)
	if err != nil {
		return DataValidation{}, err
	}
	if err := r.ensureValidationRangeAvailableLocked(current.SheetID, id, selected); err != nil {
		return DataValidation{}, err
	}
	updated.Revision = current.Revision + 1
	updated.UpdatedAt = r.now()
	r.bump(state)
	updated.WorkbookVersion = state.workbook.Version
	r.validations[id] = cloneDataValidation(updated)
	return cloneDataValidation(updated), nil
}

func (r *MemoryRepository) DeleteDataValidation(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule, ok := r.validations[id]
	if !ok {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != rule.Revision {
		return ErrRevision
	}
	state, _, err := r.sheetState(rule.SheetID)
	if err != nil {
		return err
	}
	delete(r.validations, id)
	r.bump(state)
	return nil
}

func (r *MemoryRepository) ensureValidationRangeAvailableLocked(sheetID, excludingID string, selected cellrange.Range) error {
	for id, existing := range r.validations {
		if id == excludingID || existing.SheetID != sheetID {
			continue
		}
		_, other, err := NormalizeDataValidation(existing)
		if err != nil {
			return err
		}
		if ValidationRangesOverlap(selected, other) {
			return fmt.Errorf("%w: data validation ranges cannot overlap", ErrInvalid)
		}
	}
	return nil
}

func (r *MemoryRepository) dataValidationsForSheetLocked(sheetID string) []DataValidation {
	result := make([]DataValidation, 0)
	for _, rule := range r.validations {
		if rule.SheetID == sheetID {
			result = append(result, cloneDataValidation(rule))
		}
	}
	return result
}
