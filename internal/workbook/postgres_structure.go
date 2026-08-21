package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

func (r *PostgresRepository) ApplyStructure(ctx context.Context, raw StructuralMutation) (MutationResult, error) {
	input, err := normalizeStructuralMutation(raw)
	if err != nil {
		return MutationResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT w.id::text,w.version FROM workbooks w JOIN sheets s ON s.workbook_id=w.id WHERE s.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF w`, input.SheetID).Scan(&workbookID, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, ErrNotFound
	}
	if err != nil {
		return MutationResult{}, err
	}
	if duplicate, found, err := r.findDuplicate(ctx, tx, workbookID, input.ActorID, input.IdempotencyKey); err != nil {
		return MutationResult{}, err
	} else if found {
		duplicate.Duplicate = true
		return duplicate, tx.Commit(ctx)
	}
	if input.BaseVersion != currentVersion {
		return MutationResult{}, ErrVersionConflict
	}
	sheetList, err := r.listSheets(ctx, tx, workbookID)
	if err != nil {
		return MutationResult{}, err
	}
	sheets := make(map[string]Sheet, len(sheetList))
	var target Sheet
	for _, sheet := range sheetList {
		sheets[sheet.ID] = sheet
		if sheet.ID == input.SheetID {
			target = sheet
		}
	}
	if target.ID == "" {
		return MutationResult{}, ErrNotFound
	}
	beforeSnapshot, err := r.buildSnapshot(ctx, tx, workbookID)
	if err != nil {
		return MutationResult{}, err
	}
	backup, err := r.insertSnapshot(ctx, tx, workbookID, currentVersion, structureBackupName(input), input.ActorID, beforeSnapshot)
	if err != nil {
		return MutationResult{}, err
	}
	existing, err := loadWorkbookCellsForStructure(ctx, tx, workbookID, sheets)
	if err != nil {
		return MutationResult{}, err
	}
	namedRanges, err := listNamedRangesTx(ctx, tx, workbookID)
	if err != nil {
		return MutationResult{}, err
	}
	now := r.now()
	for index := range namedRanges {
		namedRanges[index], err = transformNamedRangeForStructure(namedRanges[index], target.ID, input, input.ActorID, now)
		if err != nil {
			return MutationResult{}, err
		}
		namedRanges[index].WorkbookVersion = currentVersion + 1
	}
	validations, err := listDataValidationsTx(ctx, tx, target.ID)
	if err != nil {
		return MutationResult{}, err
	}
	transformedValidations := make([]DataValidation, 0, len(validations))
	for _, rule := range validations {
		updated, remains, transformErr := transformValidationForStructure(rule, target, input, input.ActorID, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		if remains {
			updated.WorkbookVersion = currentVersion + 1
			transformedValidations = append(transformedValidations, updated)
		}
	}
	protections, err := listProtectedRangesTx(ctx, tx, target.ID)
	if err != nil {
		return MutationResult{}, err
	}
	for _, rule := range protections {
		updated, remains, transformErr := transformProtectedForStructure(rule, target.ID, input, input.ActorID, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		if !remains {
			if _, err := tx.Exec(ctx, `DELETE FROM protected_ranges WHERE id=$1`, rule.ID); err != nil {
				return MutationResult{}, err
			}
			continue
		}
		if updated.Range == rule.Range {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE protected_ranges SET cell_range=$2,revision=$3,updated_by=$4,updated_at=$5 WHERE id=$1`,
			rule.ID, updated.Range, updated.Revision, updated.UpdatedBy, updated.UpdatedAt); err != nil {
			return MutationResult{}, err
		}
	}
	conditionalFormats, err := listConditionalFormatsTx(ctx, tx, target.ID)
	if err != nil {
		return MutationResult{}, err
	}
	transformedConditionalFormats := make([]ConditionalFormat, 0, len(conditionalFormats))
	for _, rule := range conditionalFormats {
		updated, remains, transformErr := transformConditionalFormatForStructure(rule, input, input.ActorID, now)
		if transformErr != nil {
			return MutationResult{}, transformErr
		}
		if remains {
			updated.WorkbookVersion = currentVersion + 1
			transformedConditionalFormats = append(transformedConditionalFormats, updated)
		}
	}
	filters, err := listAllFilterViewsForStructure(ctx, tx, target.ID)
	if err != nil {
		return MutationResult{}, err
	}
	transformedFilters := make([]FilterView, 0, len(filters))
	for _, view := range filters {
		if updated, remains := transformFilterForStructure(view, input, now); remains {
			transformedFilters = append(transformedFilters, updated)
		}
	}
	commentRows, err := tx.Query(ctx, `SELECT id::text,workbook_id::text,sheet_id::text,cell_range,resolved,revision,created_by,resolved_by,resolved_at,created_at,updated_at FROM comment_threads WHERE sheet_id=$1 FOR UPDATE`, target.ID)
	if err != nil {
		return MutationResult{}, err
	}
	transformedComments := make([]CommentThread, 0)
	for commentRows.Next() {
		var thread CommentThread
		if err := commentRows.Scan(&thread.ID, &thread.WorkbookID, &thread.SheetID, &thread.Range, &thread.Resolved, &thread.Revision, &thread.CreatedBy, &thread.ResolvedBy, &thread.ResolvedAt, &thread.CreatedAt, &thread.UpdatedAt); err != nil {
			commentRows.Close()
			return MutationResult{}, err
		}
		updated, transformErr := transformCommentForStructure(thread, target.ID, input, now)
		if transformErr != nil {
			commentRows.Close()
			return MutationResult{}, transformErr
		}
		transformedComments = append(transformedComments, updated)
	}
	if err := commentRows.Err(); err != nil {
		commentRows.Close()
		return MutationResult{}, err
	}
	commentRows.Close()
	chartRows, err := tx.Query(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.source_sheet_id=$1 FOR UPDATE OF c`, target.ID)
	if err != nil {
		return MutationResult{}, err
	}
	transformedCharts := make([]Chart, 0)
	for chartRows.Next() {
		chart, scanErr := scanChart(chartRows)
		if scanErr != nil {
			chartRows.Close()
			return MutationResult{}, scanErr
		}
		chart, scanErr = transformChartForStructure(chart, target.ID, input, input.ActorID, now)
		if scanErr != nil {
			chartRows.Close()
			return MutationResult{}, scanErr
		}
		chart.WorkbookVersion = currentVersion + 1
		transformedCharts = append(transformedCharts, chart)
	}
	if err := chartRows.Err(); err != nil {
		chartRows.Close()
		return MutationResult{}, err
	}
	chartRows.Close()
	pivotRows, err := tx.Query(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.source_sheet_id=$1 FOR UPDATE OF p`, target.ID)
	if err != nil {
		return MutationResult{}, err
	}
	transformedPivots := make([]Pivot, 0)
	for pivotRows.Next() {
		pivot, scanErr := scanPivot(pivotRows)
		if scanErr != nil {
			pivotRows.Close()
			return MutationResult{}, scanErr
		}
		pivot, scanErr = transformPivotForStructure(pivot, target.ID, input, input.ActorID, now)
		if scanErr != nil {
			pivotRows.Close()
			return MutationResult{}, scanErr
		}
		pivot.WorkbookVersion = currentVersion + 1
		transformedPivots = append(transformedPivots, pivot)
	}
	if err := pivotRows.Err(); err != nil {
		pivotRows.Close()
		return MutationResult{}, err
	}
	pivotRows.Close()
	nextCells, err := transformStructureCells(sheets, existing, target.ID, input)
	if err != nil {
		return MutationResult{}, err
	}
	expanded, recalculated, formulaErrors, err := recalculateCellInputs(sheets, nextCells, target.ID, nil, true, formulaNamedRanges(namedRanges), r.importsFor(ctx, workbookID, nextCells, nil))
	if err != nil {
		return MutationResult{}, err
	}
	applyRecalculatedInputs(nextCells, expanded, now)
	if err := replaceWorkbookCellsForStructure(ctx, tx, workbookID, nextCells, now); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM named_ranges WHERE workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	for _, item := range namedRanges {
		if err := insertNamedRangeTx(ctx, tx, item); err != nil {
			return MutationResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM data_validations WHERE sheet_id=$1`, target.ID); err != nil {
		return MutationResult{}, err
	}
	for _, rule := range transformedValidations {
		if err := insertDataValidationTx(ctx, tx, rule); err != nil {
			return MutationResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM conditional_formats WHERE sheet_id=$1`, target.ID); err != nil {
		return MutationResult{}, err
	}
	for _, rule := range transformedConditionalFormats {
		if err := insertConditionalFormatTx(ctx, tx, rule); err != nil {
			return MutationResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM filter_views WHERE sheet_id=$1`, target.ID); err != nil {
		return MutationResult{}, err
	}
	for _, view := range transformedFilters {
		if err := insertFilterViewForStructure(ctx, tx, view); err != nil {
			return MutationResult{}, err
		}
	}
	for _, thread := range transformedComments {
		if _, err := tx.Exec(ctx, `UPDATE comment_threads SET cell_range=$2,revision=$3,updated_at=$4 WHERE id=$1`, thread.ID, thread.Range, thread.Revision, thread.UpdatedAt); err != nil {
			return MutationResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE mention_notifications SET cell_range=$2 WHERE thread_id=$1`, thread.ID, thread.Range); err != nil {
			return MutationResult{}, err
		}
	}
	for _, chart := range transformedCharts {
		if _, err := tx.Exec(ctx, `UPDATE charts SET source_range=$2,revision=$3,updated_by=$4,updated_at=$5 WHERE id=$1`, chart.ID, chart.SourceRange, chart.Revision, chart.UpdatedBy, chart.UpdatedAt); err != nil {
			return MutationResult{}, err
		}
	}
	for _, pivot := range transformedPivots {
		if _, err := tx.Exec(ctx, `UPDATE pivots SET source_range=$2,source_version=0,cached_result=NULL,refreshed_at=NULL,revision=$3,updated_by=$4,updated_at=$5 WHERE id=$1`, pivot.ID, pivot.SourceRange, pivot.Revision, pivot.UpdatedBy, pivot.UpdatedAt); err != nil {
			return MutationResult{}, err
		}
	}
	target.Layout = transformLayoutForStructure(target.Layout, input)
	layoutData, _ := json.Marshal(target.Layout)
	if _, err := tx.Exec(ctx, `UPDATE sheets SET properties=jsonb_set(properties,'{layout}',$2::jsonb,true) WHERE id=$1`, target.ID, layoutData); err != nil {
		return MutationResult{}, err
	}
	serverVersion := currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{OperationID: identity.New(), WorkbookID: workbookID, SheetID: target.ID, BaseVersion: currentVersion, ServerVersion: serverVersion, AppliedCells: changedCellCount(existing, nextCells), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, BackupVersionID: backup.ID, StructuralAxis: input.Axis, StructuralAction: input.Action, StructuralIndex: input.Index, StructuralCount: input.Count, CreatedAt: now}
	document := marshalStructuralOperation(input, result)
	if _, err := tx.Exec(ctx, `INSERT INTO cell_operations(operation_id,idempotency_key,workbook_id,sheet_id,actor_id,client_id,base_version,server_version,operation_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, result.OperationID, input.IdempotencyKey, workbookID, target.ID, input.ActorID, input.ClientID, currentVersion, serverVersion, "structure."+input.Axis+"."+input.Action, document, now); err != nil {
		return MutationResult{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func loadWorkbookCellsForStructure(ctx context.Context, tx pgx.Tx, workbookID string, sheets map[string]Sheet) (map[string]map[cellKey]Cell, error) {
	cells := make(map[string]map[cellKey]Cell, len(sheets))
	for sheetID := range sheets {
		cells[sheetID] = make(map[cellKey]Cell)
	}
	rows, err := tx.Query(ctx, `SELECT b.sheet_id::text,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1 FOR UPDATE OF b`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sheetID string
		var data []byte
		if err := rows.Scan(&sheetID, &data); err != nil {
			return nil, err
		}
		payload := make(map[string]Cell)
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		for _, cell := range payload {
			cells[sheetID][cellKey{cell.Row, cell.Column}] = cell
		}
	}
	return cells, rows.Err()
}

func replaceWorkbookCellsForStructure(ctx context.Context, tx pgx.Tx, workbookID string, cells map[string]map[cellKey]Cell, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM cell_blocks USING sheets WHERE cell_blocks.sheet_id=sheets.id AND sheets.workbook_id=$1`, workbookID); err != nil {
		return err
	}
	type blockKey struct {
		sheetID     string
		row, column int
	}
	blocks := make(map[blockKey]map[string]Cell)
	for sheetID, sheetCells := range cells {
		for _, cell := range sheetCells {
			if isEmptyCell(cell) {
				continue
			}
			key := blockKey{sheetID: sheetID, row: (cell.Row - 1) / cellBlockSize, column: (cell.Column - 1) / cellBlockSize}
			if blocks[key] == nil {
				blocks[key] = make(map[string]Cell)
			}
			blocks[key][coordinateKey(cell.Row, cell.Column)] = cell
		}
	}
	for key, payload := range blocks {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, key.sheetID, key.row, key.column, data, now); err != nil {
			return err
		}
	}
	return nil
}

func listAllFilterViewsForStructure(ctx context.Context, tx pgx.Tx, sheetID string) ([]FilterView, error) {
	rows, err := tx.Query(ctx, `SELECT id::text,sheet_id::text,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at FROM filter_views WHERE sheet_id=$1 FOR UPDATE`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]FilterView, 0)
	for rows.Next() {
		item, err := scanFilterView(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func insertFilterViewForStructure(ctx context.Context, tx pgx.Tx, view FilterView) error {
	criteria, err := json.Marshal(view.Criteria)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO filter_views(id,sheet_id,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, view.ID, view.SheetID, view.ActorID, view.CreateKey, view.Name, view.Range, view.HeaderRows, criteria, view.Active, view.CreatedAt, view.UpdatedAt)
	return err
}

func structuralOperationType(operationType string) bool {
	return strings.HasPrefix(operationType, "structure.")
}
