package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/identity"
)

func (r *MemoryRepository) scenariosForWorkbookLocked(workbookID string) []Scenario {
	items := make([]Scenario, 0)
	for _, item := range r.scenarios {
		if item.WorkbookID == workbookID {
			items = append(items, cloneScenario(item))
		}
	}
	sort.Slice(items, func(left, right int) bool { return items[left].Name < items[right].Name })
	return items
}

func (r *MemoryRepository) CreateScenario(_ context.Context, workbookID, actor string, input CreateScenarioInput) (Scenario, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return Scenario{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return Scenario{}, ErrNotFound
	}
	for _, item := range r.scenarios {
		if item.WorkbookID == workbookID && item.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			item.WorkbookVersion = state.workbook.Version
			return cloneScenario(item), nil
		}
	}
	if _, known := state.sheets[input.SheetID]; !known {
		return Scenario{}, fmt.Errorf("%w: unknown sheet", ErrInvalid)
	}
	existing := r.scenariosForWorkbookLocked(workbookID)
	if len(existing) >= MaxScenarios {
		return Scenario{}, fmt.Errorf("%w: a workbook may contain at most %d scenarios", ErrInvalid, MaxScenarios)
	}
	item, err := normalizeScenario(Scenario{
		WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: strings.TrimSpace(input.IdempotencyKey),
		Name: input.Name, Inputs: input.Inputs, Note: input.Note,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		return Scenario{}, err
	}
	for _, other := range existing {
		if strings.EqualFold(other.Name, item.Name) {
			return Scenario{}, fmt.Errorf("%w: a scenario called %s already exists", ErrDuplicateName, item.Name)
		}
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.scenarios[item.ID] = item
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.scenarios[item.ID] = item
	return cloneScenario(item), nil
}

func (r *MemoryRepository) ListScenarios(_ context.Context, workbookID string) ([]Scenario, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return nil, ErrNotFound
	}
	items := r.scenariosForWorkbookLocked(workbookID)
	for index := range items {
		items[index].WorkbookVersion = state.workbook.Version
	}
	return items, nil
}

func (r *MemoryRepository) GetScenario(_ context.Context, id string) (Scenario, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.scenarios[id]
	if !found {
		return Scenario{}, ErrNotFound
	}
	if state, known := r.workbooks[item.WorkbookID]; known {
		item.WorkbookVersion = state.workbook.Version
	}
	return cloneScenario(item), nil
}

func (r *MemoryRepository) UpdateScenario(_ context.Context, id, actor string, input UpdateScenarioInput) (Scenario, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.scenarios[id]
	if !found {
		return Scenario{}, ErrNotFound
	}
	state, known := r.workbooks[current.WorkbookID]
	if !known {
		return Scenario{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return Scenario{}, ErrRevision
	}
	updated := current
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Inputs != nil {
		updated.Inputs = *input.Inputs
	}
	if input.Note != nil {
		updated.Note = *input.Note
	}
	normalized, err := normalizeScenario(updated)
	if err != nil {
		return Scenario{}, err
	}
	for _, other := range r.scenariosForWorkbookLocked(current.WorkbookID) {
		if other.ID != id && strings.EqualFold(other.Name, normalized.Name) {
			return Scenario{}, fmt.Errorf("%w: a scenario called %s already exists", ErrDuplicateName, normalized.Name)
		}
	}
	normalized.Revision, normalized.UpdatedBy, normalized.UpdatedAt = current.Revision+1, actor, r.now()
	r.scenarios[id] = normalized
	r.bump(state)
	normalized.WorkbookVersion = state.workbook.Version
	r.scenarios[id] = normalized
	return cloneScenario(normalized), nil
}

func (r *MemoryRepository) DeleteScenario(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.scenarios[id]
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
	delete(r.scenarios, id)
	r.bump(state)
	return nil
}
