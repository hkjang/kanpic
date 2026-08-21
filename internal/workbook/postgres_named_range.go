package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/internal/formula"
	"kanpic/pkg/identity"
)

const namedRangeColumns = `n.id::text,n.workbook_id::text,w.version,n.sheet_id::text,n.idempotency_key,n.name,n.cell_range,n.revision,n.created_by,n.updated_by,n.created_at,n.updated_at`

type namedRangeScanner interface{ Scan(...any) error }

func (r *PostgresRepository) CreateNamedRange(ctx context.Context, workbookID, actor string, input CreateNamedRangeInput) (NamedRange, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return NamedRange{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return NamedRange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	createKey := strings.TrimSpace(input.IdempotencyKey)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":named-range:"+createKey); err != nil {
		return NamedRange{}, err
	}
	if existing, lookupErr := scanNamedRange(tx.QueryRow(ctx, `SELECT `+namedRangeColumns+` FROM named_ranges n JOIN workbooks w ON w.id=n.workbook_id WHERE n.workbook_id=$1 AND n.idempotency_key=$2`, workbookID, createKey)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return NamedRange{}, lookupErr
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return NamedRange{}, ErrNotFound
	} else if err != nil {
		return NamedRange{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM named_ranges WHERE workbook_id=$1`, workbookID).Scan(&count); err != nil {
		return NamedRange{}, err
	}
	if count >= MaxNamedRanges {
		return NamedRange{}, fmt.Errorf("%w: a workbook may contain at most %d named ranges", ErrInvalid, MaxNamedRanges)
	}
	item, err := normalizeNamedRange(NamedRange{WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: createKey, Name: input.Name, Range: input.Range, CreatedBy: actor, UpdatedBy: actor})
	if err != nil {
		return NamedRange{}, err
	}
	if err := validatePostgresNamedRangeTarget(ctx, tx, item, ""); err != nil {
		return NamedRange{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if _, err := tx.Exec(ctx, `INSERT INTO named_ranges(id,workbook_id,sheet_id,idempotency_key,name,cell_range,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,1,$7,$7,$8,$8)`, item.ID, item.WorkbookID, item.SheetID, item.CreateKey, item.Name, item.Range, actor, now); err != nil {
		return NamedRange{}, mapPostgresError(err)
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, workbookID); err != nil {
		return NamedRange{}, err
	}
	item.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, item.WorkbookVersion, now); err != nil {
		return NamedRange{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return NamedRange{}, err
	}
	return item, nil
}

func (r *PostgresRepository) ListNamedRanges(ctx context.Context, workbookID string) ([]NamedRange, error) {
	items, err := listNamedRanges(ctx, r.pool, workbookID)
	if err != nil {
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

func (r *PostgresRepository) GetNamedRange(ctx context.Context, id string) (NamedRange, error) {
	item, err := scanNamedRange(r.pool.QueryRow(ctx, `SELECT `+namedRangeColumns+` FROM named_ranges n JOIN workbooks w ON w.id=n.workbook_id WHERE n.id=$1 AND w.deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return NamedRange{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) UpdateNamedRange(ctx context.Context, id, actor string, input UpdateNamedRangeInput) (NamedRange, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return NamedRange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanNamedRange(tx.QueryRow(ctx, `SELECT `+namedRangeColumns+` FROM named_ranges n JOIN workbooks w ON w.id=n.workbook_id WHERE n.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF n,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return NamedRange{}, ErrNotFound
	}
	if err != nil {
		return NamedRange{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return NamedRange{}, ErrRevision
	}
	updated := current
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.SheetID != nil {
		updated.SheetID = *input.SheetID
	}
	if input.Range != nil {
		updated.Range = *input.Range
	}
	updated, err = normalizeNamedRange(updated)
	if err != nil {
		return NamedRange{}, err
	}
	if err := validatePostgresNamedRangeTarget(ctx, tx, updated, id); err != nil {
		return NamedRange{}, err
	}
	now := r.now()
	updated.Revision, updated.UpdatedBy, updated.UpdatedAt = current.Revision+1, actor, now
	if _, err := tx.Exec(ctx, `UPDATE named_ranges SET sheet_id=$2,name=$3,cell_range=$4,revision=$5,updated_by=$6,updated_at=$7 WHERE id=$1`, id, updated.SheetID, updated.Name, updated.Range, updated.Revision, actor, now); err != nil {
		return NamedRange{}, mapPostgresError(err)
	}
	if !strings.EqualFold(current.Name, updated.Name) {
		if err := r.renameNamedRangeReferencesTx(ctx, tx, current.WorkbookID, current.Name, updated.Name); err != nil {
			return NamedRange{}, err
		}
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, current.WorkbookID); err != nil {
		return NamedRange{}, err
	}
	updated.WorkbookVersion = current.WorkbookVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, updated.WorkbookVersion, now); err != nil {
		return NamedRange{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return NamedRange{}, err
	}
	return updated, nil
}

func (r *PostgresRepository) DeleteNamedRange(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanNamedRange(tx.QueryRow(ctx, `SELECT `+namedRangeColumns+` FROM named_ranges n JOIN workbooks w ON w.id=n.workbook_id WHERE n.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF n,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	if _, err := tx.Exec(ctx, `DELETE FROM named_ranges WHERE id=$1`, id); err != nil {
		return err
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, current.WorkbookID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, current.WorkbookID, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validatePostgresNamedRangeTarget(ctx context.Context, tx pgx.Tx, item NamedRange, excludingID string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE id=$1 AND workbook_id=$2)`, item.SheetID, item.WorkbookID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: named range sheet does not belong to the workbook", ErrInvalid)
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM named_ranges WHERE workbook_id=$1 AND id::text<>$2 AND lower(name)=lower($3))`, item.WorkbookID, excludingID, item.Name).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return ErrDuplicateName
	}
	return nil
}

func listNamedRangesTx(ctx context.Context, tx pgx.Tx, workbookID string) ([]NamedRange, error) {
	return listNamedRanges(ctx, tx, workbookID)
}

func listNamedRanges(ctx context.Context, db queryer, workbookID string) ([]NamedRange, error) {
	rows, err := db.Query(ctx, `SELECT `+namedRangeColumns+` FROM named_ranges n JOIN workbooks w ON w.id=n.workbook_id WHERE n.workbook_id=$1 ORDER BY lower(n.name),n.id`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]NamedRange, 0)
	for rows.Next() {
		item, err := scanNamedRange(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanNamedRange(row namedRangeScanner) (NamedRange, error) {
	var item NamedRange
	err := row.Scan(&item.ID, &item.WorkbookID, &item.WorkbookVersion, &item.SheetID, &item.CreateKey, &item.Name, &item.Range, &item.Revision, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func insertNamedRangeTx(ctx context.Context, tx pgx.Tx, item NamedRange) error {
	_, err := tx.Exec(ctx, `INSERT INTO named_ranges(id,workbook_id,sheet_id,idempotency_key,name,cell_range,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, item.ID, item.WorkbookID, item.SheetID, item.CreateKey, item.Name, item.Range, item.Revision, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt)
	return mapPostgresError(err)
}

func (r *PostgresRepository) recalculateWorkbookFormulasTx(ctx context.Context, tx pgx.Tx, workbookID string) error {
	sheetList, err := r.listSheets(ctx, tx, workbookID)
	if err != nil {
		return err
	}
	sheets := make(map[string]Sheet, len(sheetList))
	existing := make(map[string]map[cellKey]Cell, len(sheetList))
	currentSheetID := ""
	for _, sheet := range sheetList {
		sheets[sheet.ID] = sheet
		existing[sheet.ID] = make(map[cellKey]Cell)
		if currentSheetID == "" {
			currentSheetID = sheet.ID
		}
	}
	type blockKey struct {
		sheetID     string
		row, column int
	}
	payloads := make(map[blockKey]map[string]Cell)
	rows, err := tx.Query(ctx, `SELECT b.sheet_id::text,b.block_row,b.block_column,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1 FOR UPDATE OF b`, workbookID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key blockKey
		var data []byte
		if err := rows.Scan(&key.sheetID, &key.row, &key.column, &data); err != nil {
			rows.Close()
			return err
		}
		payload := make(map[string]Cell)
		if err := json.Unmarshal(data, &payload); err != nil {
			rows.Close()
			return err
		}
		payloads[key] = payload
		for _, cell := range payload {
			existing[key.sheetID][cellKey{cell.Row, cell.Column}] = cell
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	namedRanges, err := listNamedRangesTx(ctx, tx, workbookID)
	if err != nil {
		return err
	}
	expanded, _, _, err := recalculateCellInputs(sheets, existing, currentSheetID, nil, true, formulaNamedRanges(namedRanges), r.importsFor(ctx, workbookID, existing, nil))
	if err != nil {
		return err
	}
	groups := make(map[blockKey][]CellInput)
	for _, input := range expanded {
		key := blockKey{sheetID: input.SheetID, row: (input.Row - 1) / cellBlockSize, column: (input.Column - 1) / cellBlockSize}
		groups[key] = append(groups[key], input)
	}
	now := r.now()
	for key, inputs := range groups {
		payload := payloads[key]
		if payload == nil {
			payload = make(map[string]Cell)
		}
		for _, input := range inputs {
			coordinate := coordinateKey(input.Row, input.Column)
			cell := Cell{SheetID: key.sheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), Note: input.Note, SpillSource: input.SpillSource, UpdatedAt: now}
			if isEmptyCell(cell) {
				delete(payload, coordinate)
			} else {
				payload[coordinate] = cell
			}
		}
		if len(payload) == 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM cell_blocks WHERE sheet_id=$1 AND block_row=$2 AND block_column=$3`, key.sheetID, key.row, key.column); err != nil {
				return err
			}
		} else {
			data, _ := json.Marshal(payload)
			if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sheet_id,block_row,block_column) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, key.sheetID, key.row, key.column, data, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *PostgresRepository) renameNamedRangeReferencesTx(ctx context.Context, tx pgx.Tx, workbookID, oldName, newName string) error {
	type block struct {
		sheetID     string
		row, column int
		payload     map[string]Cell
	}
	rows, err := tx.Query(ctx, `SELECT b.sheet_id::text,b.block_row,b.block_column,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1 FOR UPDATE OF b`, workbookID)
	if err != nil {
		return err
	}
	changed := make([]block, 0)
	for rows.Next() {
		var item block
		var data []byte
		if err := rows.Scan(&item.sheetID, &item.row, &item.column, &data); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(data, &item.payload); err != nil {
			rows.Close()
			return err
		}
		blockChanged := false
		for key, cell := range item.payload {
			renamed := formula.RenameNamedRangeReferences(cell.Formula, oldName, newName)
			if renamed != cell.Formula {
				cell.Formula = renamed
				item.payload[key] = cell
				blockChanged = true
			}
		}
		if blockChanged {
			changed = append(changed, item)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range changed {
		data, _ := json.Marshal(item.payload)
		if _, err := tx.Exec(ctx, `UPDATE cell_blocks SET payload=$4,updated_at=$5 WHERE sheet_id=$1 AND block_row=$2 AND block_column=$3`, item.sheetID, item.row, item.column, data, r.now()); err != nil {
			return err
		}
	}
	return nil
}
