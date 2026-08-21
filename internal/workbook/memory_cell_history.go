package workbook

import (
	"context"

	"kanpic/pkg/cellrange"
)

// CellHistory reports the edits that touched one cell, newest first.
func (r *MemoryRepository) CellHistory(_ context.Context, sheetID string, row, column, limit int) (CellHistory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return CellHistory{}, err
	}
	limit = normalizeHistoryLimit(limit)
	key := scopedCellKey{sheetID: sheetID, cellKey: cellKey{row, column}}
	result := CellHistory{SheetID: sheetID, Address: cellrange.Address(row, column), Items: make([]CellHistoryEntry, 0, limit)}
	for index := len(state.operations) - 1; index >= 0 && len(result.Items) < limit; index-- {
		item := state.operations[index]
		beforeCell, hadBefore := item.before[key]
		afterCell, hadAfter := item.after[key]
		if !hadBefore && !hadAfter {
			continue
		}
		before, after := cellHistorySnapshot(beforeCell, hadBefore), cellHistorySnapshot(afterCell, hadAfter)
		if sameCellHistorySnapshot(before, after) {
			continue
		}
		result.Items = append(result.Items, CellHistoryEntry{
			OperationID: item.result.OperationID, OperationType: item.operationType, ActorID: item.actorID,
			ServerVersion: item.result.ServerVersion, Before: before, After: after, CreatedAt: item.result.CreatedAt,
		})
	}
	return result, nil
}
