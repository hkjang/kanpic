package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/identity"
)

func cloneProtectedRange(rule ProtectedRange) ProtectedRange {
	rule.Editors = append([]string(nil), rule.Editors...)
	return rule
}

func (r *MemoryRepository) CreateProtectedRange(_ context.Context, sheetID, actor string, input CreateProtectedRangeInput) (ProtectedRange, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return ProtectedRange{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return ProtectedRange{}, err
	}
	for _, existing := range r.protections {
		if existing.SheetID == sheetID && existing.CreateKey == strings.TrimSpace(input.IdempotencyKey) {
			return cloneProtectedRange(existing), nil
		}
	}
	rule, err := protectedFromInput(sheetID, strings.TrimSpace(input.IdempotencyKey), actor, input)
	if err != nil {
		return ProtectedRange{}, err
	}
	if r.countProtectionsLocked(sheetID) >= MaxProtectedRanges {
		return ProtectedRange{}, fmt.Errorf("%w: a sheet can hold %d protected ranges", ErrInvalid, MaxProtectedRanges)
	}
	now := r.now()
	rule.ID = identity.New()
	rule.Revision = 1
	rule.CreatedAt, rule.UpdatedAt = now, now
	r.bump(state)
	r.protections[rule.ID] = cloneProtectedRange(rule)
	return cloneProtectedRange(rule), nil
}

func (r *MemoryRepository) ListProtectedRanges(_ context.Context, sheetID string) ([]ProtectedRange, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, _, err := r.sheetState(sheetID); err != nil {
		return nil, err
	}
	return r.protectionsForSheetLocked(sheetID), nil
}

func (r *MemoryRepository) UpdateProtectedRange(_ context.Context, id, actor string, input UpdateProtectedRangeInput) (ProtectedRange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.protections[id]
	if !found {
		return ProtectedRange{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return ProtectedRange{}, ErrRevision
	}
	state, _, err := r.sheetState(current.SheetID)
	if err != nil {
		return ProtectedRange{}, err
	}
	updated := cloneProtectedRange(current)
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.Scope != nil {
		updated.Scope = *input.Scope
	}
	if input.Exceptions != nil {
		updated.Exceptions = *input.Exceptions
	}
	if input.Description != nil {
		updated.Description = *input.Description
	}
	if input.Editors != nil {
		updated.Editors = *input.Editors
	}
	if input.WarningOnly != nil {
		updated.WarningOnly = *input.WarningOnly
	}
	normalized, _, err := NormalizeProtectedRange(updated)
	if err != nil {
		return ProtectedRange{}, err
	}
	normalized.Revision = current.Revision + 1
	normalized.UpdatedBy, normalized.UpdatedAt = actor, r.now()
	r.bump(state)
	r.protections[id] = cloneProtectedRange(normalized)
	return cloneProtectedRange(normalized), nil
}

func (r *MemoryRepository) DeleteProtectedRange(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.protections[id]
	if !found {
		return ErrNotFound
	}
	state, _, err := r.sheetState(current.SheetID)
	if err != nil {
		return err
	}
	delete(r.protections, id)
	r.bump(state)
	return nil
}

func (r *MemoryRepository) protectionsForSheetLocked(sheetID string) []ProtectedRange {
	result := make([]ProtectedRange, 0)
	for _, rule := range r.protections {
		if rule.SheetID == sheetID {
			result = append(result, cloneProtectedRange(rule))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Range < result[j].Range })
	return result
}

func (r *MemoryRepository) countProtectionsLocked(sheetID string) int {
	count := 0
	for _, rule := range r.protections {
		if rule.SheetID == sheetID {
			count++
		}
	}
	return count
}

// cloneProtectionsForSheets 는 버전을 찍을 때 그 시트들의 보호 범위를 담아
// 둔다. 담아 두지 않으면 되돌렸을 때 보호가 사라진다 — 막으라고 걸어 둔
// 규칙이 조용히 없어지는 것이라, 사람은 지켜지고 있다고 믿는 칸을 아무나
// 고치게 된다.
func cloneProtectionsForSheets(source map[string]ProtectedRange, sheets map[string]Sheet) map[string]ProtectedRange {
	result := make(map[string]ProtectedRange)
	for id, rule := range source {
		if _, found := sheets[rule.SheetID]; found {
			result[id] = cloneProtectedRange(rule)
		}
	}
	return result
}
