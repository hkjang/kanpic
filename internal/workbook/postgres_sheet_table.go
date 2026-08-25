package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

const sheetTableColumns = `id::text,workbook_id::text,sheet_id::text,idempotency_key,name,cell_range,header_row,theme,revision,created_by,updated_by,created_at,updated_at`

type sheetTableScanner interface{ Scan(...any) error }

func scanSheetTable(row sheetTableScanner) (SheetTable, error) {
	var item SheetTable
	if err := row.Scan(&item.ID, &item.WorkbookID, &item.SheetID, &item.CreateKey, &item.Name,
		&item.Range, &item.HeaderRow, &item.Theme, &item.Revision,
		&item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return SheetTable{}, err
	}
	return item, nil
}

func listSheetTablesFrom(ctx context.Context, source interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, workbookID string) ([]SheetTable, error) {
	rows, err := source.Query(ctx, `SELECT `+sheetTableColumns+` FROM sheet_tables WHERE workbook_id=$1 ORDER BY name`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SheetTable, 0)
	for rows.Next() {
		item, scanErr := scanSheetTable(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) CreateSheetTable(ctx context.Context, workbookID, actor string, input CreateSheetTableInput) (SheetTable, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return SheetTable{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SheetTable{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	createKey := strings.TrimSpace(input.IdempotencyKey)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":sheet-table:"+createKey); err != nil {
		return SheetTable{}, err
	}
	if existing, lookupErr := scanSheetTable(tx.QueryRow(ctx, `SELECT `+sheetTableColumns+` FROM sheet_tables WHERE workbook_id=$1 AND idempotency_key=$2`, workbookID, createKey)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return SheetTable{}, lookupErr
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return SheetTable{}, ErrNotFound
	} else if err != nil {
		return SheetTable{}, err
	}
	// 표는 그 워크북의 시트에만 걸 수 있다. 남의 시트를 가리키는 표는
	// 이름으로 남의 자료를 읽는 길이 된다.
	var sheetWorkbook string
	if err := tx.QueryRow(ctx, `SELECT workbook_id::text FROM sheets WHERE id=$1`, input.SheetID).Scan(&sheetWorkbook); errors.Is(err, pgx.ErrNoRows) || sheetWorkbook != workbookID {
		return SheetTable{}, fmt.Errorf("%w: unknown sheet", ErrInvalid)
	} else if err != nil {
		return SheetTable{}, err
	}
	existing, err := listSheetTablesFrom(ctx, tx, workbookID)
	if err != nil {
		return SheetTable{}, err
	}
	if len(existing) >= MaxTables {
		return SheetTable{}, fmt.Errorf("%w: a workbook may contain at most %d tables", ErrInvalid, MaxTables)
	}
	headerRow := true
	if input.HeaderRow != nil {
		headerRow = *input.HeaderRow
	}
	item, err := normalizeSheetTable(SheetTable{
		WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: createKey,
		Name: input.Name, Range: input.Range, HeaderRow: headerRow, Theme: input.Theme,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		return SheetTable{}, err
	}
	if err := checkTableConflicts(existing, item, ""); err != nil {
		return SheetTable{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if _, err := tx.Exec(ctx, `INSERT INTO sheet_tables(id,workbook_id,sheet_id,idempotency_key,name,cell_range,header_row,theme,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		item.ID, item.WorkbookID, item.SheetID, item.CreateKey, item.Name, item.Range, item.HeaderRow, item.Theme,
		item.Revision, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt); err != nil {
		return SheetTable{}, mapPostgresError(err)
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, workbookID); err != nil {
		return SheetTable{}, err
	}
	item.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, item.WorkbookVersion, now); err != nil {
		return SheetTable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SheetTable{}, err
	}
	return item, nil
}

func (r *PostgresRepository) ListSheetTables(ctx context.Context, workbookID string) ([]SheetTable, error) {
	items, err := listSheetTablesFrom(ctx, r.pool, workbookID)
	if err != nil {
		return nil, err
	}
	var version int64
	if err := r.pool.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&version); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].WorkbookVersion = version
	}
	return items, nil
}

func (r *PostgresRepository) GetSheetTable(ctx context.Context, id string) (SheetTable, error) {
	item, err := scanSheetTable(r.pool.QueryRow(ctx, `SELECT `+sheetTableColumns+` FROM sheet_tables WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return SheetTable{}, ErrNotFound
	} else if err != nil {
		return SheetTable{}, err
	}
	_ = r.pool.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1`, item.WorkbookID).Scan(&item.WorkbookVersion)
	return item, nil
}

func (r *PostgresRepository) UpdateSheetTable(ctx context.Context, id, actor string, input UpdateSheetTableInput) (SheetTable, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SheetTable{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanSheetTable(tx.QueryRow(ctx, `SELECT `+sheetTableColumns+` FROM sheet_tables WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return SheetTable{}, ErrNotFound
	} else if err != nil {
		return SheetTable{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return SheetTable{}, ErrRevision
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, current.WorkbookID).Scan(&currentVersion); err != nil {
		return SheetTable{}, err
	}
	updated := current
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.HeaderRow != nil {
		updated.HeaderRow = *input.HeaderRow
	}
	if input.Theme != nil {
		updated.Theme = *input.Theme
	}
	normalized, err := normalizeSheetTable(updated)
	if err != nil {
		return SheetTable{}, err
	}
	existing, err := listSheetTablesFrom(ctx, tx, current.WorkbookID)
	if err != nil {
		return SheetTable{}, err
	}
	if err := checkTableConflicts(existing, normalized, id); err != nil {
		return SheetTable{}, err
	}
	now := r.now()
	normalized.Revision, normalized.UpdatedBy, normalized.UpdatedAt = current.Revision+1, actor, now
	if _, err := tx.Exec(ctx, `UPDATE sheet_tables SET name=$2,cell_range=$3,header_row=$4,theme=$5,revision=$6,updated_by=$7,updated_at=$8 WHERE id=$1`,
		id, normalized.Name, normalized.Range, normalized.HeaderRow, normalized.Theme,
		normalized.Revision, normalized.UpdatedBy, normalized.UpdatedAt); err != nil {
		return SheetTable{}, mapPostgresError(err)
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, current.WorkbookID); err != nil {
		return SheetTable{}, err
	}
	normalized.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, normalized.WorkbookVersion, now); err != nil {
		return SheetTable{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SheetTable{}, err
	}
	return normalized, nil
}

func (r *PostgresRepository) DeleteSheetTable(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanSheetTable(tx.QueryRow(ctx, `SELECT `+sheetTableColumns+` FROM sheet_tables WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, current.WorkbookID).Scan(&currentVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sheet_tables WHERE id=$1`, id); err != nil {
		return err
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, current.WorkbookID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, currentVersion+1, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
