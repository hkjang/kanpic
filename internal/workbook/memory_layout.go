package workbook

import (
	"context"
	"fmt"

	"kanpic/pkg/identity"
)

func (r *MemoryRepository) ApplySheetLayout(_ context.Context, raw SheetLayoutMutation) (SheetLayoutResult, error) {
	input, err := normalizeSheetLayoutMutation(raw)
	if err != nil {
		return SheetLayoutResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, sheet, err := r.sheetState(input.SheetID)
	if err != nil {
		return SheetLayoutResult{}, err
	}
	if state.layoutIdempotent == nil {
		state.layoutIdempotent = make(map[string]SheetLayoutResult)
	}
	key := input.ActorID + ":" + input.IdempotencyKey
	if existing, found := state.layoutIdempotent[key]; found {
		existing.Duplicate = true
		existing.Layout = cloneSheetLayout(existing.Layout)
		return existing, nil
	}
	sheet.Layout = normalizeSheetLayout(sheet.Layout)
	if input.ExpectedRevision != sheet.Layout.Revision {
		return SheetLayoutResult{}, fmt.Errorf("%w: expected layout revision %d, current revision is %d", ErrRevision, input.ExpectedRevision, sheet.Layout.Revision)
	}
	baseVersion, now := state.workbook.Version, r.now()
	sheet.Layout, err = applySheetLayoutMutation(sheet.Layout, input)
	if err != nil {
		return SheetLayoutResult{}, err
	}
	state.sheets[sheet.ID] = sheet
	r.bump(state)
	result := SheetLayoutResult{
		OperationID: identity.New(), WorkbookID: state.workbook.ID, SheetID: sheet.ID,
		BaseVersion: baseVersion, ServerVersion: state.workbook.Version, Layout: cloneSheetLayout(sheet.Layout), CreatedAt: now,
	}
	state.layoutIdempotent[key] = result
	return result, nil
}
