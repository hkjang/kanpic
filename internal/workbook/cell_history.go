package workbook

import (
	"encoding/json"
	"time"
)

// MaxCellHistoryEntries bounds one cell's history. The point is to answer "who
// last touched this and what did it say before", not to be an audit export.
const MaxCellHistoryEntries = 50

// CellHistorySnapshot is what a cell held at one end of an edit. A nil snapshot
// means the cell did not exist yet, which is how a first entry reads.
type CellHistorySnapshot struct {
	Value   json.RawMessage `json:"value,omitempty"`
	Formula string          `json:"formula,omitempty"`
	Empty   bool            `json:"empty"`
}

// CellHistoryEntry is one operation as it touched this one cell.
type CellHistoryEntry struct {
	OperationID   string              `json:"operation_id"`
	OperationType string              `json:"operation_type"`
	ActorID       string              `json:"actor_id"`
	ActorName     string              `json:"actor_name,omitempty"`
	ServerVersion int64               `json:"server_version"`
	Before        CellHistorySnapshot `json:"before"`
	After         CellHistorySnapshot `json:"after"`
	CreatedAt     time.Time           `json:"created_at"`
}

type CellHistory struct {
	SheetID string             `json:"sheet_id"`
	Address string             `json:"address"`
	Items   []CellHistoryEntry `json:"items"`
}

func cellHistorySnapshot(cell Cell, present bool) CellHistorySnapshot {
	if !present || isEmptyCell(cell) {
		return CellHistorySnapshot{Empty: true}
	}
	return CellHistorySnapshot{Value: cloneJSON(cell.Value), Formula: cell.Formula}
}

// sameCellHistorySnapshot drops entries where an operation swept over the cell
// without changing it — a paste that covered it with the same text, a
// recalculation that landed on the same number. Showing those as edits would
// bury the change somebody is actually looking for.
func sameCellHistorySnapshot(before, after CellHistorySnapshot) bool {
	if before.Empty != after.Empty {
		return false
	}
	return before.Formula == after.Formula && string(before.Value) == string(after.Value)
}

func normalizeHistoryLimit(limit int) int {
	if limit < 1 || limit > MaxCellHistoryEntries {
		return MaxCellHistoryEntries
	}
	return limit
}
