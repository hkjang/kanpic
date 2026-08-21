package workbook

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/cellrange"
)

// CellHistory reports the edits that touched one cell, newest first. The
// operation log already stores the before and after of every cell it wrote, so
// this is a read of what is there rather than a second record to keep in sync.
func (r *PostgresRepository) CellHistory(ctx context.Context, sheetID string, row, column, limit int) (CellHistory, error) {
	var workbookID string
	if err := r.pool.QueryRow(ctx, `SELECT s.workbook_id::text FROM sheets s JOIN workbooks w ON w.id=s.workbook_id WHERE s.id=$1 AND w.deleted_at IS NULL`, sheetID).Scan(&workbookID); errors.Is(err, pgx.ErrNoRows) {
		return CellHistory{}, ErrNotFound
	} else if err != nil {
		return CellHistory{}, err
	}
	limit = normalizeHistoryLimit(limit)
	key := operationCoordinateKey(sheetID, row, column)
	result := CellHistory{SheetID: sheetID, Address: cellrange.Address(row, column), Items: make([]CellHistoryEntry, 0, limit)}
	// Operations that never mention the cell are filtered in the database: one
	// cell's history must not read the whole workbook's operation log.
	rows, err := r.pool.Query(ctx, `SELECT operation_id::text,operation_type,actor_id,server_version,payload,created_at
		FROM cell_operations
		WHERE workbook_id=$1 AND (payload->'before' ? $2 OR payload->'after' ? $2)
		ORDER BY server_version DESC LIMIT $3`, workbookID, key, limit)
	if err != nil {
		return CellHistory{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry CellHistoryEntry
		var payload []byte
		if err := rows.Scan(&entry.OperationID, &entry.OperationType, &entry.ActorID, &entry.ServerVersion, &payload, &entry.CreatedAt); err != nil {
			return CellHistory{}, err
		}
		var document operationDocument
		if err := json.Unmarshal(payload, &document); err != nil {
			return CellHistory{}, err
		}
		beforeCell, hadBefore := document.Before[key]
		afterCell, hadAfter := document.After[key]
		entry.Before, entry.After = cellHistorySnapshot(beforeCell, hadBefore), cellHistorySnapshot(afterCell, hadAfter)
		if sameCellHistorySnapshot(entry.Before, entry.After) {
			continue
		}
		result.Items = append(result.Items, entry)
	}
	return result, rows.Err()
}
