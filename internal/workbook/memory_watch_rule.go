package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/identity"
)

func cloneWatchRule(item WatchRule) WatchRule { return item }

func (r *MemoryRepository) watchRulesForSheetLocked(sheetID string) []WatchRule {
	items := make([]WatchRule, 0)
	for _, item := range r.watchRules {
		if item.SheetID == sheetID {
			items = append(items, cloneWatchRule(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (r *MemoryRepository) CreateWatchRule(_ context.Context, workbookID, actor string, input CreateWatchRuleInput) (WatchRule, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return WatchRule{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.workbooks[workbookID]; !found {
		return WatchRule{}, ErrNotFound
	}
	if owner, ok := r.sheetToWB[input.SheetID]; !ok || owner != workbookID {
		return WatchRule{}, fmt.Errorf("%w: the sheet does not belong to this workbook", ErrInvalid)
	}
	for _, item := range r.watchRules {
		if item.WorkbookID == workbookID && item.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			return cloneWatchRule(item), nil
		}
	}
	count := 0
	for _, item := range r.watchRules {
		if item.WorkbookID == workbookID {
			count++
		}
	}
	if count >= MaxWatchRules {
		return WatchRule{}, fmt.Errorf("%w: a workbook may contain at most %d watch rules", ErrInvalid, MaxWatchRules)
	}
	watcher := strings.TrimSpace(input.Watcher)
	if watcher == "" {
		// 대개 자기가 지켜본다. 남을 대신 등록하는 것은 시키지 않은 메일을
		// 보내는 일이므로 기본은 자기 자신이다.
		watcher = actor
	}
	item, err := normalizeWatchRule(WatchRule{
		WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: strings.TrimSpace(input.IdempotencyKey),
		Watcher: watcher, Range: input.Range, Label: input.Label, Enabled: true,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		return WatchRule{}, err
	}
	for _, other := range r.watchRulesForSheetLocked(item.SheetID) {
		if strings.EqualFold(other.Watcher, item.Watcher) && other.Range == item.Range {
			return WatchRule{}, fmt.Errorf("%w: this range is already watched", ErrDuplicateName)
		}
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.watchRules[item.ID] = item
	return cloneWatchRule(item), nil
}

// ListWatchRules 는 한 사람이 이 워크북에서 지켜보는 것을 돌려준다. 남이
// 무엇을 지켜보는지는 보여 주지 않는다 — 누가 무엇을 보고 있는지는
// 그 사람의 일이다.
func (r *MemoryRepository) ListWatchRules(_ context.Context, workbookID, watcher string) ([]WatchRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, found := r.workbooks[workbookID]; !found {
		return nil, ErrNotFound
	}
	items := make([]WatchRule, 0)
	for _, item := range r.watchRules {
		if item.WorkbookID == workbookID && strings.EqualFold(item.Watcher, strings.TrimSpace(watcher)) {
			items = append(items, cloneWatchRule(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// SheetWatchRules 는 셀이 바뀔 때 알릴 사람을 찾는 자리에서 쓴다.
func (r *MemoryRepository) SheetWatchRules(_ context.Context, sheetID string) ([]WatchRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.watchRulesForSheetLocked(sheetID), nil
}

func (r *MemoryRepository) UpdateWatchRule(_ context.Context, id, actor string, input UpdateWatchRuleInput) (WatchRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.watchRules[id]
	if !found {
		return WatchRule{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return WatchRule{}, ErrVersionConflict
	}
	updated := current
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.Label != nil {
		updated.Label = *input.Label
	}
	if input.Enabled != nil {
		updated.Enabled = *input.Enabled
	}
	updated.UpdatedBy = actor
	normalized, err := normalizeWatchRule(updated)
	if err != nil {
		return WatchRule{}, err
	}
	normalized.Revision, normalized.UpdatedAt = current.Revision+1, r.now()
	r.watchRules[id] = normalized
	return cloneWatchRule(normalized), nil
}

func (r *MemoryRepository) DeleteWatchRule(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.watchRules[id]
	if !found {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrVersionConflict
	}
	delete(r.watchRules, id)
	return nil
}

func (r *MemoryRepository) GetWatchRule(_ context.Context, id string) (WatchRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.watchRules[id]
	if !found {
		return WatchRule{}, ErrNotFound
	}
	return cloneWatchRule(item), nil
}
