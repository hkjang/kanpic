package workbook

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

// postgresImportReader resolves IMPORTRANGE against the database. It reads the
// source workbook through the pool rather than the caller's transaction: the
// source is a different workbook, an import is a copy rather than part of the
// mutation, and taking no locks on it keeps two workbooks importing from each
// other from deadlocking.
type postgresImportReader struct {
	ctx        context.Context
	repository *PostgresRepository
}

func (p postgresImportReader) importOwner(workbookID string) (string, error) {
	var owner string
	if err := p.repository.pool.QueryRow(p.ctx, `SELECT owner_id FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&owner); err != nil {
		return "", err
	}
	return owner, nil
}

func (p postgresImportReader) importTitle(workbookID string) (string, error) {
	var title string
	if err := p.repository.pool.QueryRow(p.ctx, `SELECT title FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&title); err != nil {
		return "", err
	}
	return title, nil
}

func (p postgresImportReader) importReadable(sourceWorkbookID, ownerID string) (bool, error) {
	principal := AccessPrincipal{UserID: ownerID, Authenticated: ownerID != ""}
	access, err := p.repository.ResolveWorkbookAccess(p.ctx, sourceWorkbookID, principal)
	if err != nil {
		return false, err
	}
	return access.CanRead, nil
}

func (p postgresImportReader) importSheet(sourceWorkbookID, sheetName string) (string, error) {
	var sheetID string
	if sheetName == "" {
		err := p.repository.pool.QueryRow(p.ctx, `SELECT id::text FROM sheets WHERE workbook_id=$1 ORDER BY position LIMIT 1`, sourceWorkbookID).Scan(&sheetID)
		return sheetID, err
	}
	err := p.repository.pool.QueryRow(p.ctx, `SELECT id::text FROM sheets WHERE workbook_id=$1 AND upper(btrim(name))=upper(btrim($2)) ORDER BY position LIMIT 1`, sourceWorkbookID, sheetName).Scan(&sheetID)
	return sheetID, err
}

func (p postgresImportReader) importCells(sheetID string, selected cellrange.Range) ([]Cell, error) {
	return p.repository.ReadRange(p.ctx, sheetID, selected)
}

// importsFor gathers and resolves the workbook's IMPORTRANGE calls.
func (r *PostgresRepository) importsFor(ctx context.Context, workbookID string, cells map[string]map[cellKey]Cell, submitted []CellInput) map[string]formula.ImportedRange {
	return resolveImportRequests(postgresImportReader{ctx: ctx, repository: r}, workbookID, collectImportRequests(cells, submitted))
}

// loadWorkbookCells reads a workbook's cells without locking them, which is
// what the read-only connection listing needs.
func (r *PostgresRepository) loadWorkbookCells(ctx context.Context, db queryer, workbookID string, sheets []Sheet) (map[string]map[cellKey]Cell, error) {
	cells := make(map[string]map[cellKey]Cell, len(sheets))
	for _, sheet := range sheets {
		cells[sheet.ID] = make(map[cellKey]Cell)
	}
	rows, err := db.Query(ctx, `SELECT b.sheet_id::text,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1`, workbookID)
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
		if cells[sheetID] == nil {
			cells[sheetID] = make(map[cellKey]Cell)
		}
		for _, cell := range payload {
			cells[sheetID][cellKey{cell.Row, cell.Column}] = cell
		}
	}
	return cells, rows.Err()
}

// ListConnections reports every cross-workbook import and whether it can be
// read right now.
func (r *PostgresRepository) ListConnections(ctx context.Context, workbookID string) (WorkbookConnections, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
		return WorkbookConnections{}, err
	}
	if !exists {
		return WorkbookConnections{}, ErrNotFound
	}
	sheetList, err := r.listSheets(ctx, r.pool, workbookID)
	if err != nil {
		return WorkbookConnections{}, err
	}
	cells, err := r.loadWorkbookCells(ctx, r.pool, workbookID, sheetList)
	if err != nil {
		return WorkbookConnections{}, err
	}
	sheets := make(map[string]Sheet, len(sheetList))
	for _, sheet := range sheetList {
		sheets[sheet.ID] = sheet
	}
	return describeConnections(postgresImportReader{ctx: ctx, repository: r}, workbookID, sheets, cells, r.now()), nil
}

// RefreshConnections recalculates every formula so IMPORTRANGE re-reads its
// sources, then reports what each connection looks like afterwards.
func (r *PostgresRepository) RefreshConnections(ctx context.Context, workbookID, actorID string) (WorkbookConnections, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return WorkbookConnections{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return WorkbookConnections{}, ErrNotFound
	} else if err != nil {
		return WorkbookConnections{}, err
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, workbookID); err != nil {
		return WorkbookConnections{}, err
	}
	now, serverVersion := r.now(), currentVersion+1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return WorkbookConnections{}, err
	}
	sheetList, err := r.listSheets(ctx, tx, workbookID)
	if err != nil {
		return WorkbookConnections{}, err
	}
	cells, err := r.loadWorkbookCells(ctx, tx, workbookID, sheetList)
	if err != nil {
		return WorkbookConnections{}, err
	}
	sheets := make(map[string]Sheet, len(sheetList))
	for _, sheet := range sheetList {
		sheets[sheet.ID] = sheet
	}
	result := describeConnections(postgresImportReader{ctx: ctx, repository: r}, workbookID, sheets, cells, now)
	result.RefreshedAt, result.Version = &now, serverVersion
	if err := tx.Commit(ctx); err != nil {
		return WorkbookConnections{}, err
	}
	return result, nil
}
