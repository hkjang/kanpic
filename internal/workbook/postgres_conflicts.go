package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/cellrange"
)

const postgresConflictColumns = `id::text,workbook_id::text,sheet_id::text,operation_id::text,row_number,column_number,base_version,changed_at_version,server_version,actor_id,client_id,conflicting_actor_id,base_cell,conflicting_cell,submitted_cell,applied_cell,status,resolution,revision,resolved_by,coalesce(resolution_operation_id::text,''),resolution_server_version,resolved_at,created_at,updated_at`

type conflictScanner interface {
	Scan(...any) error
}

func scanPostgresConflict(scanner conflictScanner) (CellConflict, error) {
	var conflict CellConflict
	var baseCell, conflictingCell, submittedCell, appliedCell []byte
	err := scanner.Scan(
		&conflict.ID, &conflict.WorkbookID, &conflict.SheetID, &conflict.OperationID,
		&conflict.Row, &conflict.Column, &conflict.BaseVersion, &conflict.ChangedAtVersion,
		&conflict.ServerVersion, &conflict.ActorID, &conflict.ClientID, &conflict.ConflictingActorID,
		&baseCell, &conflictingCell, &submittedCell, &appliedCell,
		&conflict.Status, &conflict.Resolution, &conflict.Revision, &conflict.ResolvedBy,
		&conflict.ResolutionOperationID, &conflict.ResolutionServerVersion, &conflict.ResolvedAt,
		&conflict.CreatedAt, &conflict.UpdatedAt,
	)
	if err != nil {
		return CellConflict{}, err
	}
	if err := json.Unmarshal(baseCell, &conflict.BaseCell); err != nil {
		return CellConflict{}, err
	}
	if err := json.Unmarshal(conflictingCell, &conflict.ConflictingCell); err != nil {
		return CellConflict{}, err
	}
	if err := json.Unmarshal(submittedCell, &conflict.SubmittedCell); err != nil {
		return CellConflict{}, err
	}
	if err := json.Unmarshal(appliedCell, &conflict.AppliedCell); err != nil {
		return CellConflict{}, err
	}
	conflict.PreviousValue = cloneJSON(conflict.ConflictingCell.Value)
	conflict.SubmittedValue = cloneJSON(conflict.SubmittedCell.Value)
	conflict.CurrentCell = cloneConflictSnapshot(conflict.AppliedCell)
	return conflict, nil
}

func (r *PostgresRepository) hydrateConflictCurrent(ctx context.Context, conflict *CellConflict) error {
	selected := cellrange.Range{
		Start: cellrange.Position{Row: conflict.Row, Column: conflict.Column},
		End:   cellrange.Position{Row: conflict.Row, Column: conflict.Column},
	}
	cells, err := r.ReadRange(ctx, conflict.SheetID, selected)
	if err != nil {
		return err
	}
	current := Cell{SheetID: conflict.SheetID, Row: conflict.Row, Column: conflict.Column}
	if len(cells) > 0 {
		current = cells[0]
	}
	conflict.CurrentCell = conflictSnapshotFromCell(current)
	return nil
}

func (r *PostgresRepository) ListCellConflicts(ctx context.Context, workbookID string, includeResolved bool) ([]CellConflict, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	query := `SELECT ` + postgresConflictColumns + ` FROM cell_conflicts WHERE workbook_id=$1`
	arguments := []any{workbookID}
	if !includeResolved {
		query += ` AND status=$2`
		arguments = append(arguments, ConflictStatusOpen)
	}
	query += ` ORDER BY CASE WHEN status='open' THEN 0 ELSE 1 END,created_at DESC,id`
	rows, err := r.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CellConflict, 0)
	for rows.Next() {
		conflict, err := scanPostgresConflict(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, conflict)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Close before ReadRange obtains another connection. This also keeps the
	// method usable with small PostgreSQL pools in offline deployments.
	rows.Close()
	for index := range result {
		if err := r.hydrateConflictCurrent(ctx, &result[index]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *PostgresRepository) GetCellConflict(ctx context.Context, conflictID string) (CellConflict, error) {
	conflict, err := scanPostgresConflict(r.pool.QueryRow(ctx, `SELECT `+postgresConflictColumns+` FROM cell_conflicts WHERE id=$1`, conflictID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CellConflict{}, ErrNotFound
	}
	if err != nil {
		return CellConflict{}, err
	}
	if err := r.hydrateConflictCurrent(ctx, &conflict); err != nil {
		return CellConflict{}, err
	}
	return conflict, nil
}

func (r *PostgresRepository) ResolveCellConflict(ctx context.Context, conflictID string, input ResolveCellConflictInput) (CellConflictResolutionResult, error) {
	if err := validateConflictResolution(input); err != nil {
		return CellConflictResolutionResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return CellConflictResolutionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	conflict, err := scanPostgresConflict(tx.QueryRow(ctx, `SELECT `+postgresConflictColumns+` FROM cell_conflicts WHERE id=$1 FOR UPDATE`, conflictID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CellConflictResolutionResult{}, ErrNotFound
	}
	if err != nil {
		return CellConflictResolutionResult{}, err
	}
	if conflict.Status == ConflictStatusResolved {
		if conflict.Resolution != input.Resolution {
			return CellConflictResolutionResult{}, ErrRevision
		}
		if err := r.hydrateConflictCurrent(ctx, &conflict); err != nil {
			return CellConflictResolutionResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CellConflictResolutionResult{}, err
		}
		return CellConflictResolutionResult{Conflict: conflict, Operation: MutationResult{OperationID: conflict.ResolutionOperationID, WorkbookID: conflict.WorkbookID, SheetID: conflict.SheetID, ServerVersion: conflict.ResolutionServerVersion, Duplicate: true}}, nil
	}
	if conflict.Revision != input.ExpectedRevision {
		return CellConflictResolutionResult{}, ErrRevision
	}
	operationType := "conflict.resolve." + input.Resolution
	var existingType string
	existingErr := tx.QueryRow(ctx, `SELECT operation_type FROM cell_operations WHERE workbook_id=$1 AND actor_id=$2 AND idempotency_key=$3`, conflict.WorkbookID, input.ActorID, input.IdempotencyKey).Scan(&existingType)
	if existingErr == nil {
		if existingType != operationType {
			return CellConflictResolutionResult{}, ErrRevision
		}
		duplicate, found, err := r.findDuplicate(ctx, tx, conflict.WorkbookID, input.ActorID, input.IdempotencyKey)
		if err != nil {
			return CellConflictResolutionResult{}, err
		}
		if found {
			duplicate.Duplicate = true
			now := r.now()
			if err := finishPostgresConflictResolution(ctx, tx, &conflict, input, duplicate, now); err != nil {
				return CellConflictResolutionResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return CellConflictResolutionResult{}, err
			}
			if err := r.hydrateConflictCurrent(ctx, &conflict); err != nil {
				return CellConflictResolutionResult{}, err
			}
			return CellConflictResolutionResult{Conflict: conflict, Operation: duplicate}, nil
		}
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return CellConflictResolutionResult{}, existingErr
	}
	if err := r.hydrateConflictCurrent(ctx, &conflict); err != nil {
		return CellConflictResolutionResult{}, err
	}
	current := cellFromConflictSnapshot(conflict.SheetID, conflict.Row, conflict.Column, conflict.CurrentCell)
	target := conflictSnapshotFromCell(current)
	if input.Resolution == ConflictRestorePrevious {
		if !cellsEqual(current, cellFromConflictSnapshot(conflict.SheetID, conflict.Row, conflict.Column, conflict.AppliedCell)) {
			return CellConflictResolutionResult{}, ErrVersionConflict
		}
		target = conflict.ConflictingCell
	}
	book, err := r.GetWorkbook(ctx, conflict.WorkbookID)
	if err != nil {
		return CellConflictResolutionResult{}, err
	}
	result, err := r.ApplyCells(ctx, CellMutation{
		SheetID: conflict.SheetID, ActorID: input.ActorID, ClientID: input.ClientID,
		BaseVersion: book.Version, IdempotencyKey: input.IdempotencyKey,
		Cells:         []CellInput{inputFromConflictSnapshot(conflict.Row, conflict.Column, target)},
		Expected:      map[string]Cell{coordinateKey(conflict.Row, conflict.Column): current},
		OperationType: operationType,
	})
	if err != nil {
		return CellConflictResolutionResult{}, err
	}
	if result.AppliedCells != 1 {
		return CellConflictResolutionResult{}, ErrVersionConflict
	}
	now := r.now()
	if err := finishPostgresConflictResolution(ctx, tx, &conflict, input, result, now); err != nil {
		return CellConflictResolutionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CellConflictResolutionResult{}, err
	}
	if err := r.hydrateConflictCurrent(ctx, &conflict); err != nil {
		return CellConflictResolutionResult{}, err
	}
	return CellConflictResolutionResult{Conflict: conflict, Operation: result}, nil
}

func finishPostgresConflictResolution(ctx context.Context, tx pgx.Tx, conflict *CellConflict, input ResolveCellConflictInput, result MutationResult, now time.Time) error {
	command, err := tx.Exec(ctx, `UPDATE cell_conflicts SET status=$2,resolution=$3,revision=revision+1,resolved_by=$4,resolution_operation_id=$5,resolution_server_version=$6,resolved_at=$7,updated_at=$7 WHERE id=$1 AND status=$8 AND revision=$9`, conflict.ID, ConflictStatusResolved, input.Resolution, input.ActorID, result.OperationID, result.ServerVersion, now, ConflictStatusOpen, input.ExpectedRevision)
	if err != nil {
		return mapPostgresError(err)
	}
	if command.RowsAffected() != 1 {
		return ErrRevision
	}
	conflict.Status = ConflictStatusResolved
	conflict.Resolution = input.Resolution
	conflict.Revision++
	conflict.ResolvedBy = input.ActorID
	conflict.ResolutionOperationID = result.OperationID
	conflict.ResolutionServerVersion = result.ServerVersion
	conflict.ResolvedAt = &now
	conflict.UpdatedAt = now
	return nil
}
