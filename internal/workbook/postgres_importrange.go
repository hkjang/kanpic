package workbook

import (
	"context"

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
