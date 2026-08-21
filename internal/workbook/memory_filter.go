package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func (r *MemoryRepository) CreateFilterView(_ context.Context, sheetID, actorID string, input CreateFilterViewInput) (FilterView, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return FilterView{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, _, err := r.sheetState(sheetID); err != nil {
		return FilterView{}, err
	}
	for _, candidate := range r.filters {
		if candidate.SheetID == sheetID && candidate.ActorID == actorID && candidate.CreateKey == input.IdempotencyKey {
			return cloneFilterView(candidate), nil
		}
	}
	now := r.now()
	view, _, err := filterViewFromInput(sheetID, actorID, now, input)
	if err != nil {
		return FilterView{}, err
	}
	for _, candidate := range r.filters {
		if candidate.SheetID == sheetID && candidate.ActorID == actorID && strings.EqualFold(candidate.Name, view.Name) {
			return FilterView{}, ErrDuplicateName
		}
	}
	if view.Active {
		r.deactivateFilters(sheetID, actorID, "")
	}
	r.filters[view.ID] = cloneFilterView(view)
	return cloneFilterView(view), nil
}

func (r *MemoryRepository) ListFilterViews(_ context.Context, sheetID, actorID string) ([]FilterView, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, _, err := r.sheetState(sheetID); err != nil {
		return nil, err
	}
	items := make([]FilterView, 0)
	for _, view := range r.filters {
		if view.SheetID == sheetID && view.ActorID == actorID {
			items = append(items, cloneFilterView(view))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Active != items[j].Active {
			return items[i].Active
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (r *MemoryRepository) GetFilterView(_ context.Context, id, actorID string) (FilterView, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	view, ok := r.filters[id]
	if !ok || view.ActorID != actorID {
		return FilterView{}, ErrNotFound
	}
	return cloneFilterView(view), nil
}

func (r *MemoryRepository) UpdateFilterView(_ context.Context, id, actorID string, input UpdateFilterViewInput) (FilterView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.filters[id]
	if !ok || current.ActorID != actorID {
		return FilterView{}, ErrNotFound
	}
	next := cloneFilterView(current)
	if input.Name != nil {
		next.Name = *input.Name
	}
	if input.Range != nil {
		next.Range = *input.Range
	}
	if input.HeaderRows != nil {
		next.HeaderRows = *input.HeaderRows
	}
	if input.Criteria != nil {
		next.Criteria = cloneFilterCriteria(*input.Criteria)
	}
	if input.Active != nil {
		next.Active = *input.Active
	}
	var err error
	next, _, err = NormalizeFilterView(next)
	if err != nil {
		return FilterView{}, err
	}
	for candidateID, candidate := range r.filters {
		if candidateID != id && candidate.SheetID == next.SheetID && candidate.ActorID == actorID && strings.EqualFold(candidate.Name, next.Name) {
			return FilterView{}, ErrDuplicateName
		}
	}
	if next.Active {
		r.deactivateFilters(next.SheetID, actorID, id)
	}
	next.UpdatedAt = r.now()
	r.filters[id] = cloneFilterView(next)
	return cloneFilterView(next), nil
}

func (r *MemoryRepository) DeleteFilterView(_ context.Context, id, actorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	view, ok := r.filters[id]
	if !ok || view.ActorID != actorID {
		return ErrNotFound
	}
	delete(r.filters, id)
	return nil
}

func (r *MemoryRepository) deactivateFilters(sheetID, actorID, exceptID string) {
	for id, view := range r.filters {
		if id != exceptID && view.SheetID == sheetID && view.ActorID == actorID && view.Active {
			view.Active = false
			view.UpdatedAt = r.now()
			r.filters[id] = view
		}
	}
}

func cloneFilterView(view FilterView) FilterView {
	view.Criteria = cloneFilterCriteria(view.Criteria)
	return view
}

func cloneFilterCriteria(criteria []FilterCriterion) []FilterCriterion {
	if criteria == nil {
		return []FilterCriterion{}
	}
	result := make([]FilterCriterion, len(criteria))
	for index, criterion := range criteria {
		result[index] = criterion
		result[index].Value = cloneJSON(criterion.Value)
		if criterion.Values != nil {
			result[index].Values = make([]json.RawMessage, len(criterion.Values))
			for valueIndex, value := range criterion.Values {
				result[index].Values[valueIndex] = cloneJSON(value)
			}
		}
	}
	return result
}
