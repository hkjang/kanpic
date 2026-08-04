package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"kanpic/pkg/identity"
)

// SetWorkbookFavorite stores a star for one user. Favourites are personal, so a
// shared workbook can be starred by every collaborator independently.
func (r *PostgresRepository) SetWorkbookFavorite(ctx context.Context, workbookID, userID string, favorite bool) error {
	actor := strings.TrimSpace(userID)
	if actor == "" {
		return fmt.Errorf("%w: an actor is required to change a favorite", ErrInvalid)
	}
	var exists string
	if err := r.pool.QueryRow(ctx, `SELECT id::text FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&exists); err != nil {
		var pgError *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgError) && pgError.Code == "22P02") {
			return ErrNotFound
		}
		return err
	}
	if favorite {
		_, err := r.pool.Exec(ctx, `INSERT INTO workbook_favorites(workbook_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, workbookID, actor)
		return err
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM workbook_favorites WHERE workbook_id=$1 AND lower(user_id)=lower($2)`, workbookID, actor)
	return err
}

func (r *PostgresRepository) WorkbookFavorites(ctx context.Context, userID string) (map[string]bool, error) {
	favorites := make(map[string]bool)
	actor := strings.TrimSpace(userID)
	if actor == "" {
		return favorites, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT workbook_id::text FROM workbook_favorites WHERE lower(user_id)=lower($1)`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		favorites[id] = true
	}
	return favorites, rows.Err()
}

// ListDeletedWorkbooks returns the trash. Only owners and administrators see a
// deleted workbook, because a share cannot be evaluated once it is gone.
func (r *PostgresRepository) ListDeletedWorkbooks(ctx context.Context, workspaceID string, principal AccessPrincipal) ([]Workbook, error) {
	query := `SELECT id::text,workspace_id,title,owner_id,version,created_at,updated_at,deleted_at,deleted_by
		FROM workbooks WHERE deleted_at IS NOT NULL`
	// The owner filter is the only user of $1, so it is bound with the clause.
	args := []any{}
	if !principal.Admin {
		args = append(args, principal.identities())
		query += ` AND lower(owner_id) = ANY($1)`
	}
	if workspaceID != "" {
		args = append(args, workspaceID)
		query += fmt.Sprintf(` AND workspace_id=$%d`, len(args))
	}
	query += ` ORDER BY deleted_at DESC`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Workbook, 0)
	for rows.Next() {
		var wb Workbook
		if err := rows.Scan(&wb.ID, &wb.WorkspaceID, &wb.Title, &wb.OwnerID, &wb.Version, &wb.CreatedAt, &wb.UpdatedAt, &wb.DeletedAt, &wb.DeletedBy); err != nil {
			return nil, err
		}
		wb.AccessRole, wb.AccessSource = RoleOwner, AccessSourceOwner
		items = append(items, wb)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) RestoreWorkbook(ctx context.Context, workbookID, actorID string) (Workbook, error) {
	command, err := r.pool.Exec(ctx, `UPDATE workbooks SET deleted_at=NULL,deleted_by='',updated_at=$2 WHERE id=$1 AND deleted_at IS NOT NULL`, workbookID, r.now())
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "22P02" {
			return Workbook{}, ErrNotFound
		}
		return Workbook{}, err
	}
	if command.RowsAffected() == 0 {
		return Workbook{}, ErrNotFound
	}
	return r.GetWorkbook(ctx, workbookID)
}

// PurgeWorkbook deletes a trashed workbook and everything referencing it for
// good. Active workbooks must be trashed first so a purge is never a surprise.
func (r *PostgresRepository) PurgeWorkbook(ctx context.Context, workbookID string) error {
	command, err := r.pool.Exec(ctx, `DELETE FROM workbooks WHERE id=$1 AND deleted_at IS NOT NULL`, workbookID)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "22P02" {
			return ErrNotFound
		}
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) SheetStats(ctx context.Context, workbookID string) ([]SheetStats, error) {
	if _, err := r.GetWorkbook(ctx, workbookID); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT s.id::text, s.name, s.position, s.properties,
		       count(item.cell) FILTER (WHERE item.cell IS NOT NULL)::int AS cells,
		       count(item.cell) FILTER (WHERE coalesce(item.cell->>'formula','') <> '')::int AS formulas,
		       coalesce(max((item.cell->>'row')::integer), 0) AS max_row,
		       coalesce(max((item.cell->>'column')::integer), 0) AS max_column,
		       max((item.cell->>'updated_at')::timestamptz) AS updated_at
		FROM sheets s
		LEFT JOIN cell_blocks b ON b.sheet_id = s.id
		LEFT JOIN LATERAL jsonb_each(b.payload) AS item(coordinate, cell) ON true
		WHERE s.workbook_id=$1
		GROUP BY s.id, s.name, s.position, s.properties
		ORDER BY s.position`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SheetStats, 0)
	for rows.Next() {
		var stats SheetStats
		var propertiesData []byte
		if err := rows.Scan(&stats.SheetID, &stats.Name, &stats.Position, &propertiesData, &stats.NonEmptyCells, &stats.FormulaCells, &stats.MaxRow, &stats.MaxColumn, &stats.UpdatedAt); err != nil {
			return nil, err
		}
		var properties sheetProperties
		_ = json.Unmarshal(propertiesData, &properties)
		stats.Hidden, stats.Color = properties.Hidden, properties.Color
		items = append(items, stats)
	}
	return items, rows.Err()
}

// CopySheetToWorkbook duplicates a sheet, including cells, formatting and
// layout, into another workbook and bumps that workbook's version.
func (r *PostgresRepository) CopySheetToWorkbook(ctx context.Context, sheetID string, input CopySheetInput) (Sheet, error) {
	normalized, err := validateCopySheetInput(input)
	if err != nil {
		return Sheet{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Sheet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var source Sheet
	var propertiesData []byte
	err = tx.QueryRow(ctx, `SELECT id::text,workbook_id::text,name,position,properties,created_at FROM sheets WHERE id=$1`, sheetID).
		Scan(&source.ID, &source.WorkbookID, &source.Name, &source.Position, &propertiesData, &source.CreatedAt)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgError) && pgError.Code == "22P02") {
			return Sheet{}, ErrNotFound
		}
		return Sheet{}, err
	}
	var targetID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, normalized.TargetWorkbookID).Scan(&targetID); err != nil {
		var pgError *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgError) && pgError.Code == "22P02") {
			return Sheet{}, ErrNotFound
		}
		return Sheet{}, err
	}
	taken := make(map[string]struct{})
	names, err := tx.Query(ctx, `SELECT name FROM sheets WHERE workbook_id=$1`, targetID)
	if err != nil {
		return Sheet{}, err
	}
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			names.Close()
			return Sheet{}, err
		}
		taken[strings.ToLower(name)] = struct{}{}
	}
	names.Close()
	// Copying inside the same workbook always needs a new name; copying across
	// workbooks keeps the original name when it is free.
	fallback := source.Name
	if targetID == source.WorkbookID {
		fallback = source.Name + " 복사본"
	}
	var position int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sheets WHERE workbook_id=$1`, targetID).Scan(&position); err != nil {
		return Sheet{}, err
	}
	now := r.now()
	created := Sheet{ID: identity.New(), WorkbookID: targetID, Name: availableSheetName(normalized.Name, fallback, taken), Position: position, CreatedAt: now}
	var properties sheetProperties
	_ = json.Unmarshal(propertiesData, &properties)
	properties.Layout = normalizeSheetLayout(properties.Layout)
	properties.Hidden = false
	created.Color, created.Hidden, created.Layout = properties.Color, false, properties.Layout
	copiedProperties, _ := json.Marshal(properties)
	if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6)`, created.ID, targetID, created.Name, created.Position, copiedProperties, now); err != nil {
		return Sheet{}, mapPostgresError(err)
	}
	// Cell payloads embed their sheet id, so every block is rewritten for the
	// new sheet instead of copied verbatim.
	type copiedBlock struct {
		row, column int
		payload     []byte
	}
	blockRows, err := tx.Query(ctx, `SELECT block_row,block_column,payload FROM cell_blocks WHERE sheet_id=$1 ORDER BY block_row,block_column`, sheetID)
	if err != nil {
		return Sheet{}, err
	}
	blocks := make([]copiedBlock, 0)
	for blockRows.Next() {
		var block copiedBlock
		if err := blockRows.Scan(&block.row, &block.column, &block.payload); err != nil {
			blockRows.Close()
			return Sheet{}, err
		}
		blocks = append(blocks, block)
	}
	if err := blockRows.Err(); err != nil {
		blockRows.Close()
		return Sheet{}, err
	}
	blockRows.Close()
	for _, block := range blocks {
		var payload map[string]Cell
		if err := json.Unmarshal(block.payload, &payload); err != nil {
			return Sheet{}, err
		}
		for coordinate, cell := range payload {
			cell.SheetID = created.ID
			cell.UpdatedAt = now
			payload[coordinate] = cell
		}
		data, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return Sheet{}, marshalErr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, created.ID, block.row, block.column, data, now); err != nil {
			return Sheet{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, targetID, now); err != nil {
		return Sheet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Sheet{}, err
	}
	return created, nil
}
