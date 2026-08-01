package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const pivotColumns = `p.id::text,p.workbook_id::text,w.version,p.sheet_id::text,coalesce(p.source_sheet_id::text,''),p.idempotency_key,p.name,p.source_range,p.first_row_headers,p.row_dimensions,p.column_dimensions,p.value_fields,p.filters,p.calculated_fields,p.refresh_mode,p.source_version,p.refreshed_at,p.revision,p.created_by,p.updated_by,p.created_at,p.updated_at`

type pivotScanner interface{ Scan(...any) error }

func (r *PostgresRepository) CreatePivot(ctx context.Context, workbookID, actor string, input CreatePivotInput) (Pivot, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return Pivot{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Pivot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":pivot:"+actor+":"+key); err != nil {
		return Pivot{}, err
	}
	if existing, lookupErr := scanPivot(tx.QueryRow(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.workbook_id=$1 AND p.created_by=$2 AND p.idempotency_key=$3 AND w.deleted_at IS NULL`, workbookID, actor, key)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return Pivot{}, lookupErr
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Pivot{}, ErrNotFound
	} else if err != nil {
		return Pivot{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pivots WHERE workbook_id=$1`, workbookID).Scan(&count); err != nil {
		return Pivot{}, err
	}
	if count >= MaxPivotsPerWorkbook {
		return Pivot{}, fmt.Errorf("%w: a workbook may contain at most %d pivots", ErrInvalid, MaxPivotsPerWorkbook)
	}
	headers := true
	if input.FirstRowHeaders != nil {
		headers = *input.FirstRowHeaders
	}
	item, err := normalizePivot(Pivot{WorkbookID: workbookID, SheetID: input.SheetID, SourceSheetID: input.SourceSheetID, CreateKey: key, Name: input.Name, SourceRange: input.SourceRange, FirstRowHeaders: headers, Rows: input.Rows, Columns: input.Columns, Values: input.Values, Filters: input.Filters, CalculatedFields: input.CalculatedFields, RefreshMode: input.RefreshMode, CreatedBy: actor, UpdatedBy: actor}, false)
	if err != nil {
		return Pivot{}, err
	}
	if err := validatePostgresPivotSheets(ctx, tx, item); err != nil {
		return Pivot{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if err := insertPivotTx(ctx, tx, item); err != nil {
		return Pivot{}, err
	}
	item.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, item.WorkbookVersion, now); err != nil {
		return Pivot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Pivot{}, err
	}
	return item, nil
}

func (r *PostgresRepository) ListPivots(ctx context.Context, workbookID, sheetID string) ([]Pivot, error) {
	query := `SELECT ` + pivotColumns + ` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.workbook_id=$1 AND w.deleted_at IS NULL`
	args := []any{workbookID}
	if strings.TrimSpace(sheetID) != "" {
		query += ` AND p.sheet_id=$2`
		args = append(args, strings.TrimSpace(sheetID))
	}
	query += ` ORDER BY p.created_at,p.id`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Pivot, 0)
	for rows.Next() {
		item, scanErr := scanPivot(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return items, nil
}

func (r *PostgresRepository) GetPivot(ctx context.Context, id string) (Pivot, error) {
	item, err := scanPivot(r.pool.QueryRow(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.id=$1 AND w.deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pivot{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) GetPivotData(ctx context.Context, id string) (PivotData, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return PivotData{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, cached, err := scanPivotCache(tx.QueryRow(ctx, `SELECT `+pivotColumns+`,p.cached_result FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF p`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return PivotData{}, ErrNotFound
	}
	if err != nil {
		return PivotData{}, err
	}
	if item.RefreshMode == "manual" && len(cached) > 0 && string(cached) != "null" {
		var data PivotData
		if err := json.Unmarshal(cached, &data); err != nil {
			return PivotData{}, err
		}
		data.Pivot, data.WorkbookVersion, data.Cached = item, item.WorkbookVersion, true
		return data, tx.Commit(ctx)
	}
	cells, err := pivotRangeCells(ctx, tx, item)
	if err != nil {
		return PivotData{}, err
	}
	now := r.now()
	data, err := buildPivotData(item, item.WorkbookVersion, cells, now)
	if err != nil {
		return PivotData{}, err
	}
	if item.RefreshMode == "manual" {
		item.SourceVersion, item.LastRefreshedAt = item.WorkbookVersion, &now
		data.Pivot, data.Cached = item, true
		encoded, _ := json.Marshal(data)
		if _, err := tx.Exec(ctx, `UPDATE pivots SET source_version=$2,refreshed_at=$3,cached_result=$4 WHERE id=$1`, item.ID, item.SourceVersion, now, encoded); err != nil {
			return PivotData{}, err
		}
	}
	return data, tx.Commit(ctx)
}

func (r *PostgresRepository) RefreshPivot(ctx context.Context, id, actor string) (PivotData, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return PivotData{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanPivot(tx.QueryRow(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF p,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return PivotData{}, ErrNotFound
	}
	if err != nil {
		return PivotData{}, err
	}
	cells, err := pivotRangeCells(ctx, tx, item)
	if err != nil {
		return PivotData{}, err
	}
	now := r.now()
	data, err := buildPivotData(item, item.WorkbookVersion, cells, now)
	if err != nil {
		return PivotData{}, err
	}
	item.SourceVersion, item.LastRefreshedAt, item.UpdatedBy, item.UpdatedAt = item.WorkbookVersion, &now, actor, now
	data.Pivot = item
	var cache any
	if item.RefreshMode == "manual" {
		data.Cached = true
		encoded, _ := json.Marshal(data)
		cache = encoded
	}
	if _, err := tx.Exec(ctx, `UPDATE pivots SET source_version=$2,refreshed_at=$3,cached_result=$4,updated_by=$5,updated_at=$3 WHERE id=$1`, item.ID, item.SourceVersion, now, cache, actor); err != nil {
		return PivotData{}, err
	}
	return data, tx.Commit(ctx)
}

func (r *PostgresRepository) PivotDrilldown(ctx context.Context, id string, input PivotDrilldownInput) (PivotDrilldownResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanPivot(tx.QueryRow(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.id=$1 AND w.deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return PivotDrilldownResult{}, ErrNotFound
	}
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	if item.SourceRange == "#REF!" {
		return PivotDrilldownResult{}, fmt.Errorf("%w: pivot source is unavailable", ErrInvalid)
	}
	cells, err := pivotRangeCells(ctx, tx, item)
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	result, err := buildPivotDrilldown(item, cells, input)
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	return result, tx.Commit(ctx)
}

func (r *PostgresRepository) UpdatePivot(ctx context.Context, id, actor string, input UpdatePivotInput) (Pivot, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Pivot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanPivot(tx.QueryRow(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF p,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pivot{}, ErrNotFound
	}
	if err != nil {
		return Pivot{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return Pivot{}, ErrRevision
	}
	updated := current
	if input.SheetID != nil {
		updated.SheetID = *input.SheetID
	}
	if input.SourceSheetID != nil {
		updated.SourceSheetID = *input.SourceSheetID
	}
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.SourceRange != nil {
		updated.SourceRange = *input.SourceRange
	}
	if input.FirstRowHeaders != nil {
		updated.FirstRowHeaders = *input.FirstRowHeaders
	}
	if input.Rows != nil {
		updated.Rows = *input.Rows
	}
	if input.Columns != nil {
		updated.Columns = *input.Columns
	}
	if input.Values != nil {
		updated.Values = *input.Values
	}
	if input.Filters != nil {
		updated.Filters = *input.Filters
	}
	if input.CalculatedFields != nil {
		updated.CalculatedFields = *input.CalculatedFields
	}
	if input.RefreshMode != nil {
		updated.RefreshMode = *input.RefreshMode
	}
	updated, err = normalizePivot(updated, false)
	if err != nil {
		return Pivot{}, err
	}
	if err := validatePostgresPivotSheets(ctx, tx, updated); err != nil {
		return Pivot{}, err
	}
	now := r.now()
	updated.Revision, updated.UpdatedBy, updated.UpdatedAt, updated.SourceVersion, updated.LastRefreshedAt = current.Revision+1, actor, now, 0, nil
	rowsJSON, _ := json.Marshal(updated.Rows)
	columnsJSON, _ := json.Marshal(updated.Columns)
	valuesJSON, _ := json.Marshal(updated.Values)
	filtersJSON, _ := json.Marshal(updated.Filters)
	calculatedJSON, _ := json.Marshal(updated.CalculatedFields)
	if _, err := tx.Exec(ctx, `UPDATE pivots SET sheet_id=$2,source_sheet_id=$3,name=$4,source_range=$5,first_row_headers=$6,row_dimensions=$7,column_dimensions=$8,value_fields=$9,filters=$10,calculated_fields=$11,refresh_mode=$12,source_version=0,cached_result=NULL,refreshed_at=NULL,revision=$13,updated_by=$14,updated_at=$15 WHERE id=$1`, id, updated.SheetID, updated.SourceSheetID, updated.Name, updated.SourceRange, updated.FirstRowHeaders, rowsJSON, columnsJSON, valuesJSON, filtersJSON, calculatedJSON, updated.RefreshMode, updated.Revision, actor, now); err != nil {
		return Pivot{}, mapPostgresError(err)
	}
	updated.WorkbookVersion = current.WorkbookVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, updated.WorkbookVersion, now); err != nil {
		return Pivot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Pivot{}, err
	}
	return updated, nil
}

func (r *PostgresRepository) DeletePivot(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanPivot(tx.QueryRow(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF p,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	if _, err := tx.Exec(ctx, `DELETE FROM pivots WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, current.WorkbookID, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validatePostgresPivotSheets(ctx context.Context, tx pgx.Tx, item Pivot) error {
	ids := []string{item.SheetID}
	if item.SourceRange != "#REF!" {
		ids = append(ids, item.SourceSheetID)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sheets WHERE workbook_id=$1 AND id=ANY($2::uuid[])`, item.WorkbookID, ids).Scan(&count); err != nil {
		return err
	}
	expected := len(ids)
	if len(ids) == 2 && ids[0] == ids[1] {
		expected = 1
	}
	if count != expected {
		return fmt.Errorf("%w: pivot and source sheets must belong to workbook", ErrInvalid)
	}
	return nil
}

func scanPivot(row pivotScanner) (Pivot, error) {
	var item Pivot
	var rowsJSON, columnsJSON, valuesJSON, filtersJSON, calculatedJSON []byte
	err := row.Scan(&item.ID, &item.WorkbookID, &item.WorkbookVersion, &item.SheetID, &item.SourceSheetID, &item.CreateKey, &item.Name, &item.SourceRange, &item.FirstRowHeaders, &rowsJSON, &columnsJSON, &valuesJSON, &filtersJSON, &calculatedJSON, &item.RefreshMode, &item.SourceVersion, &item.LastRefreshedAt, &item.Revision, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Pivot{}, err
	}
	if err := json.Unmarshal(rowsJSON, &item.Rows); err != nil {
		return Pivot{}, err
	}
	if err := json.Unmarshal(columnsJSON, &item.Columns); err != nil {
		return Pivot{}, err
	}
	if err := json.Unmarshal(valuesJSON, &item.Values); err != nil {
		return Pivot{}, err
	}
	if err := json.Unmarshal(filtersJSON, &item.Filters); err != nil {
		return Pivot{}, err
	}
	if err := json.Unmarshal(calculatedJSON, &item.CalculatedFields); err != nil {
		return Pivot{}, err
	}
	return item, nil
}

func scanPivotCache(row pivotScanner) (Pivot, []byte, error) {
	var item Pivot
	var rowsJSON, columnsJSON, valuesJSON, filtersJSON, calculatedJSON, cached []byte
	err := row.Scan(&item.ID, &item.WorkbookID, &item.WorkbookVersion, &item.SheetID, &item.SourceSheetID, &item.CreateKey, &item.Name, &item.SourceRange, &item.FirstRowHeaders, &rowsJSON, &columnsJSON, &valuesJSON, &filtersJSON, &calculatedJSON, &item.RefreshMode, &item.SourceVersion, &item.LastRefreshedAt, &item.Revision, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &cached)
	if err != nil {
		return Pivot{}, nil, err
	}
	if err := json.Unmarshal(rowsJSON, &item.Rows); err != nil {
		return Pivot{}, nil, err
	}
	if err := json.Unmarshal(columnsJSON, &item.Columns); err != nil {
		return Pivot{}, nil, err
	}
	if err := json.Unmarshal(valuesJSON, &item.Values); err != nil {
		return Pivot{}, nil, err
	}
	if err := json.Unmarshal(filtersJSON, &item.Filters); err != nil {
		return Pivot{}, nil, err
	}
	if err := json.Unmarshal(calculatedJSON, &item.CalculatedFields); err != nil {
		return Pivot{}, nil, err
	}
	return item, cached, nil
}

func insertPivotTx(ctx context.Context, tx pgx.Tx, item Pivot) error {
	rowsJSON, _ := json.Marshal(item.Rows)
	columnsJSON, _ := json.Marshal(item.Columns)
	valuesJSON, _ := json.Marshal(item.Values)
	filtersJSON, _ := json.Marshal(item.Filters)
	calculatedJSON, _ := json.Marshal(item.CalculatedFields)
	var source any = item.SourceSheetID
	if item.SourceSheetID == "" {
		source = nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO pivots(id,workbook_id,sheet_id,source_sheet_id,idempotency_key,name,source_range,first_row_headers,row_dimensions,column_dimensions,value_fields,filters,calculated_fields,refresh_mode,source_version,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, item.ID, item.WorkbookID, item.SheetID, source, item.CreateKey, item.Name, item.SourceRange, item.FirstRowHeaders, rowsJSON, columnsJSON, valuesJSON, filtersJSON, calculatedJSON, item.RefreshMode, item.SourceVersion, item.Revision, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt)
	return mapPostgresError(err)
}

func listPivotsTx(ctx context.Context, tx pgx.Tx, workbookID string) ([]Pivot, error) {
	rows, err := tx.Query(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.workbook_id=$1 ORDER BY p.created_at,p.id`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Pivot, 0)
	for rows.Next() {
		item, scanErr := scanPivot(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func pivotRangeCells(ctx context.Context, db queryer, item Pivot) ([]Cell, error) {
	if item.SourceRange == "#REF!" || item.SourceSheetID == "" {
		return nil, nil
	}
	selected, err := cellrange.Parse(item.SourceRange)
	if err != nil {
		return nil, err
	}
	return readRangeQuery(ctx, db, item.SourceSheetID, selected)
}
