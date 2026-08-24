package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/internal/formula"
	"kanpic/pkg/identity"
)

func cloneNamedFunction(item NamedFunction) NamedFunction {
	item.Parameters = append([]string(nil), item.Parameters...)
	return item
}

func (r *MemoryRepository) namedFunctionsForWorkbookLocked(workbookID string) []NamedFunction {
	items := make([]NamedFunction, 0)
	for _, item := range r.namedFunctions {
		if item.WorkbookID == workbookID {
			items = append(items, cloneNamedFunction(item))
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

// namedFunctionDefinitionsLocked 는 수식을 다시 셈할 때 엔진에 넘길 꼴이다.
func (r *MemoryRepository) namedFunctionDefinitionsLocked(workbookID string) map[string]formula.NamedFunction {
	return NamedFunctionDefinitions(r.namedFunctionsForWorkbookLocked(workbookID))
}

func (r *MemoryRepository) CreateNamedFunction(_ context.Context, workbookID, actor string, input CreateNamedFunctionInput) (NamedFunction, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return NamedFunction{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return NamedFunction{}, ErrNotFound
	}
	for _, item := range r.namedFunctions {
		if item.WorkbookID == workbookID && item.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			item.WorkbookVersion = state.workbook.Version
			return cloneNamedFunction(item), nil
		}
	}
	existing := r.namedFunctionsForWorkbookLocked(workbookID)
	if len(existing) >= MaxNamedFunctions {
		return NamedFunction{}, fmt.Errorf("%w: a workbook may contain at most %d named functions", ErrInvalid, MaxNamedFunctions)
	}
	item, err := normalizeNamedFunction(NamedFunction{
		WorkbookID: workbookID, CreateKey: strings.TrimSpace(input.IdempotencyKey),
		Name: input.Name, Parameters: input.Parameters, Body: input.Body, Description: input.Description,
		CreatedBy: actor, UpdatedBy: actor,
	}, existing)
	if err != nil {
		return NamedFunction{}, err
	}
	for _, other := range existing {
		if strings.EqualFold(other.Name, item.Name) {
			return NamedFunction{}, fmt.Errorf("%w: a named function called %s already exists", ErrDuplicateName, item.Name)
		}
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.namedFunctions[item.ID] = item
	if err := r.recalculateAllLocked(state); err != nil {
		delete(r.namedFunctions, item.ID)
		return NamedFunction{}, err
	}
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.namedFunctions[item.ID] = item
	return cloneNamedFunction(item), nil
}

func (r *MemoryRepository) ListNamedFunctions(_ context.Context, workbookID string) ([]NamedFunction, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return nil, ErrNotFound
	}
	items := r.namedFunctionsForWorkbookLocked(workbookID)
	for index := range items {
		items[index].WorkbookVersion = state.workbook.Version
	}
	return items, nil
}

func (r *MemoryRepository) GetNamedFunction(_ context.Context, id string) (NamedFunction, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.namedFunctions[id]
	if !found {
		return NamedFunction{}, ErrNotFound
	}
	if state, ok := r.workbooks[item.WorkbookID]; ok {
		item.WorkbookVersion = state.workbook.Version
	}
	return cloneNamedFunction(item), nil
}

func (r *MemoryRepository) UpdateNamedFunction(_ context.Context, id, actor string, input UpdateNamedFunctionInput) (NamedFunction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.namedFunctions[id]
	if !found {
		return NamedFunction{}, ErrNotFound
	}
	state, ok := r.workbooks[current.WorkbookID]
	if !ok {
		return NamedFunction{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return NamedFunction{}, ErrVersionConflict
	}
	updated := cloneNamedFunction(current)
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Parameters != nil {
		updated.Parameters = append([]string(nil), (*input.Parameters)...)
	}
	if input.Body != nil {
		updated.Body = *input.Body
	}
	if input.Description != nil {
		updated.Description = *input.Description
	}
	updated.UpdatedBy = actor
	others := make([]NamedFunction, 0)
	for _, item := range r.namedFunctionsForWorkbookLocked(current.WorkbookID) {
		if item.ID != id {
			others = append(others, item)
		}
	}
	normalized, err := normalizeNamedFunction(updated, others)
	if err != nil {
		return NamedFunction{}, err
	}
	for _, other := range others {
		if strings.EqualFold(other.Name, normalized.Name) {
			return NamedFunction{}, fmt.Errorf("%w: a named function called %s already exists", ErrDuplicateName, normalized.Name)
		}
	}
	normalized.Revision, normalized.UpdatedAt = current.Revision+1, r.now()
	r.namedFunctions[id] = normalized
	if err := r.recalculateAllLocked(state); err != nil {
		r.namedFunctions[id] = current
		return NamedFunction{}, err
	}
	r.bump(state)
	normalized.WorkbookVersion = state.workbook.Version
	r.namedFunctions[id] = normalized
	return cloneNamedFunction(normalized), nil
}

func (r *MemoryRepository) DeleteNamedFunction(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.namedFunctions[id]
	if !found {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrVersionConflict
	}
	state, ok := r.workbooks[current.WorkbookID]
	if !ok {
		return ErrNotFound
	}
	delete(r.namedFunctions, id)
	// 지우면 그것을 쓰던 칸은 #NAME? 이 된다. 조용히 예전 값을 남기면
	// 사람은 아직 살아 있는 줄 안다.
	if err := r.recalculateAllLocked(state); err != nil {
		r.namedFunctions[id] = current
		return err
	}
	r.bump(state)
	return nil
}
