package workbook

import (
	"fmt"
	"strings"
	"time"

	"kanpic/pkg/identity"
)

const (
	ConflictStatusOpen      = "open"
	ConflictStatusResolved  = "resolved"
	ConflictKeepCurrent     = "keep_current"
	ConflictRestorePrevious = "restore_previous"
)

func conflictSnapshotFromCell(cell Cell) CellConflictSnapshot {
	return CellConflictSnapshot{
		Value:       cloneJSON(cell.Value),
		Formula:     cell.Formula,
		Style:       cloneJSON(cell.Style),
		SpillSource: cell.SpillSource,
	}
}

func conflictSnapshotFromInput(input CellInput) CellConflictSnapshot {
	return CellConflictSnapshot{
		Value:       cloneJSON(input.Value),
		Formula:     input.Formula,
		Style:       cloneJSON(input.Style),
		SpillSource: input.SpillSource,
	}
}

func cellFromConflictSnapshot(sheetID string, row, column int, snapshot CellConflictSnapshot) Cell {
	return Cell{
		SheetID:     sheetID,
		Row:         row,
		Column:      column,
		Value:       cloneJSON(snapshot.Value),
		Formula:     snapshot.Formula,
		Style:       cloneJSON(snapshot.Style),
		SpillSource: snapshot.SpillSource,
	}
}

func inputFromConflictSnapshot(row, column int, snapshot CellConflictSnapshot) CellInput {
	return CellInput{
		Row:         row,
		Column:      column,
		Value:       cloneJSON(snapshot.Value),
		Formula:     snapshot.Formula,
		Style:       cloneJSON(snapshot.Style),
		SpillSource: snapshot.SpillSource,
	}
}

func emptyConflictSnapshot(snapshot CellConflictSnapshot) bool {
	return len(snapshot.Value) == 0 && snapshot.Formula == "" && len(snapshot.Style) == 0 && snapshot.SpillSource == ""
}

func cloneConflictSnapshot(snapshot CellConflictSnapshot) CellConflictSnapshot {
	snapshot.Value = cloneJSON(snapshot.Value)
	snapshot.Style = cloneJSON(snapshot.Style)
	return snapshot
}

func cloneCellConflict(conflict CellConflict) CellConflict {
	conflict.BaseCell = cloneConflictSnapshot(conflict.BaseCell)
	conflict.ConflictingCell = cloneConflictSnapshot(conflict.ConflictingCell)
	conflict.SubmittedCell = cloneConflictSnapshot(conflict.SubmittedCell)
	conflict.AppliedCell = cloneConflictSnapshot(conflict.AppliedCell)
	conflict.CurrentCell = cloneConflictSnapshot(conflict.CurrentCell)
	conflict.PreviousValue = cloneJSON(conflict.PreviousValue)
	conflict.SubmittedValue = cloneJSON(conflict.SubmittedValue)
	if conflict.ResolvedAt != nil {
		resolved := *conflict.ResolvedAt
		conflict.ResolvedAt = &resolved
	}
	return conflict
}

func validateConflictResolution(input ResolveCellConflictInput) error {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	if input.ExpectedRevision < 1 {
		return fmt.Errorf("%w: expected_revision must be positive", ErrInvalid)
	}
	if input.Resolution != ConflictKeepCurrent && input.Resolution != ConflictRestorePrevious {
		return fmt.Errorf("%w: resolution must be keep_current or restore_previous", ErrInvalid)
	}
	return nil
}

func finalizeCellConflicts(conflicts []CellConflict, mutation CellMutation, result MutationResult, current func(int, int) (Cell, bool), now time.Time) []CellConflict {
	if len(conflicts) == 0 {
		return nil
	}
	inputs := make(map[string]CellInput, len(mutation.Cells))
	for _, input := range mutation.Cells {
		inputs[coordinateKey(input.Row, input.Column)] = input
	}
	finalized := make([]CellConflict, len(conflicts))
	for index, conflict := range conflicts {
		input := inputs[coordinateKey(conflict.Row, conflict.Column)]
		conflict.ID = identity.New()
		conflict.WorkbookID = result.WorkbookID
		conflict.SheetID = mutation.SheetID
		conflict.OperationID = result.OperationID
		conflict.BaseVersion = mutation.BaseVersion
		conflict.ServerVersion = result.ServerVersion
		conflict.ActorID = mutation.ActorID
		conflict.ClientID = mutation.ClientID
		if emptyConflictSnapshot(conflict.SubmittedCell) {
			conflict.SubmittedCell = conflictSnapshotFromInput(input)
		}
		if cell, ok := current(conflict.Row, conflict.Column); ok {
			conflict.CurrentCell = conflictSnapshotFromCell(cell)
		} else {
			conflict.CurrentCell = cloneConflictSnapshot(conflict.SubmittedCell)
		}
		conflict.AppliedCell = cloneConflictSnapshot(conflict.CurrentCell)
		if len(conflict.PreviousValue) == 0 {
			conflict.PreviousValue = cloneJSON(conflict.ConflictingCell.Value)
		}
		if len(conflict.SubmittedValue) == 0 {
			conflict.SubmittedValue = cloneJSON(conflict.SubmittedCell.Value)
		}
		conflict.Status = ConflictStatusOpen
		conflict.Revision = 1
		conflict.CreatedAt = now
		conflict.UpdatedAt = now
		finalized[index] = conflict
	}
	return finalized
}
