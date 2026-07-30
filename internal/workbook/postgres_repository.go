package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const cellBlockSize = 64

type PostgresRepository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

type sheetProperties struct {
	Color  string `json:"color,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

type operationDocument struct {
	Before       map[string]Cell `json:"before,omitempty"`
	After        map[string]Cell `json:"after,omitempty"`
	Conflicts    []CellConflict  `json:"conflicts,omitempty"`
	AppliedCells int             `json:"applied_cells"`
}

type snapshotBlock struct {
	SheetID     string          `json:"sheet_id"`
	BlockRow    int             `json:"block_row"`
	BlockColumn int             `json:"block_column"`
	Payload     json.RawMessage `json:"payload"`
}

type snapshotDocument struct {
	Blocks []snapshotBlock `json:"blocks"`
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

func (r *PostgresRepository) CreateWorkbook(ctx context.Context, input CreateWorkbookInput) (Workbook, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return Workbook{}, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Workbook{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := r.now()
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.Exec(ctx, `INSERT INTO workbooks(id,workspace_id,title,owner_id,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)`, wb.ID, wb.WorkspaceID, wb.Title, wb.OwnerID, wb.Version, now); err != nil {
		return Workbook{}, err
	}
	sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: "Sheet1", Position: 0, CreatedAt: now}
	if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,created_at) VALUES($1,$2,$3,$4,$5)`, sheet.ID, wb.ID, sheet.Name, sheet.Position, now); err != nil {
		return Workbook{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workbook{}, err
	}
	wb.Sheets = []Sheet{sheet}
	return wb, nil
}

func (r *PostgresRepository) ListWorkbooks(ctx context.Context, workspaceID string) ([]Workbook, error) {
	query := `SELECT id::text,workspace_id,title,owner_id,favorite,version,created_at,updated_at FROM workbooks WHERE deleted_at IS NULL`
	args := []any{}
	if workspaceID != "" {
		query += ` AND workspace_id=$1`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Workbook, 0)
	for rows.Next() {
		var wb Workbook
		if err := rows.Scan(&wb.ID, &wb.WorkspaceID, &wb.Title, &wb.OwnerID, &wb.Favorite, &wb.Version, &wb.CreatedAt, &wb.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, wb)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Sheets, err = r.listSheets(ctx, r.pool, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (r *PostgresRepository) GetWorkbook(ctx context.Context, id string) (Workbook, error) {
	var wb Workbook
	err := r.pool.QueryRow(ctx, `SELECT id::text,workspace_id,title,owner_id,favorite,version,created_at,updated_at FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&wb.ID, &wb.WorkspaceID, &wb.Title, &wb.OwnerID, &wb.Favorite, &wb.Version, &wb.CreatedAt, &wb.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workbook{}, ErrNotFound
	}
	if err != nil {
		return Workbook{}, err
	}
	wb.Sheets, err = r.listSheets(ctx, r.pool, id)
	return wb, err
}

func (r *PostgresRepository) UpdateWorkbook(ctx context.Context, id string, input UpdateWorkbookInput) (Workbook, error) {
	current, err := r.GetWorkbook(ctx, id)
	if err != nil {
		return Workbook{}, err
	}
	if input.Title != nil {
		current.Title = strings.TrimSpace(*input.Title)
		if current.Title == "" {
			return Workbook{}, fmt.Errorf("%w: title cannot be empty", ErrInvalid)
		}
	}
	if input.Favorite != nil {
		current.Favorite = *input.Favorite
	}
	err = r.pool.QueryRow(ctx, `UPDATE workbooks SET title=$2,favorite=$3,updated_at=$4 WHERE id=$1 AND deleted_at IS NULL RETURNING updated_at`, id, current.Title, current.Favorite, r.now()).Scan(&current.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workbook{}, ErrNotFound
	}
	return current, err
}

func (r *PostgresRepository) DeleteWorkbook(ctx context.Context, id string) error {
	command, err := r.pool.Exec(ctx, `UPDATE workbooks SET deleted_at=$2,updated_at=$2 WHERE id=$1 AND deleted_at IS NULL`, id, r.now())
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (r *PostgresRepository) CreateSheet(ctx context.Context, workbookID string, input CreateSheetInput) (Sheet, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Sheet{}, fmt.Errorf("%w: sheet name is required", ErrInvalid)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Sheet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var position int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sheets WHERE workbook_id=$1`, workbookID).Scan(&position); err != nil {
		return Sheet{}, err
	}
	now := r.now()
	sheet := Sheet{ID: identity.New(), WorkbookID: workbookID, Name: name, Position: position, Color: input.Color, CreatedAt: now}
	properties, _ := json.Marshal(sheetProperties{Color: input.Color})
	if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) SELECT $1,id,$3,$4,$5,$6 FROM workbooks WHERE id=$2 AND deleted_at IS NULL`, sheet.ID, workbookID, name, position, properties, now); err != nil {
		return Sheet{}, mapPostgresError(err)
	}
	command, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1 AND deleted_at IS NULL`, workbookID, now)
	if err != nil {
		return Sheet{}, err
	}
	if command.RowsAffected() == 0 {
		return Sheet{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return Sheet{}, err
	}
	return sheet, nil
}

func (r *PostgresRepository) UpdateSheet(ctx context.Context, sheetID string, input UpdateSheetInput) (Sheet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Sheet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sheet Sheet
	var propertiesData []byte
	err = tx.QueryRow(ctx, `SELECT id::text,workbook_id::text,name,position,properties,created_at FROM sheets WHERE id=$1 FOR UPDATE`, sheetID).Scan(&sheet.ID, &sheet.WorkbookID, &sheet.Name, &sheet.Position, &propertiesData, &sheet.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	}
	if err != nil {
		return Sheet{}, err
	}
	var properties sheetProperties
	_ = json.Unmarshal(propertiesData, &properties)
	if input.Name != nil {
		sheet.Name = strings.TrimSpace(*input.Name)
		if sheet.Name == "" {
			return Sheet{}, fmt.Errorf("%w: sheet name cannot be empty", ErrInvalid)
		}
	}
	if input.Position != nil {
		if *input.Position < 0 {
			return Sheet{}, fmt.Errorf("%w: position cannot be negative", ErrInvalid)
		}
		sheet.Position = *input.Position
	}
	if input.Color != nil {
		properties.Color = *input.Color
	}
	if input.Hidden != nil {
		properties.Hidden = *input.Hidden
	}
	sheet.Color, sheet.Hidden = properties.Color, properties.Hidden
	propertiesData, _ = json.Marshal(properties)
	if _, err := tx.Exec(ctx, `UPDATE sheets SET name=$2,position=$3,properties=$4 WHERE id=$1`, sheetID, sheet.Name, sheet.Position, propertiesData); err != nil {
		return Sheet{}, mapPostgresError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, sheet.WorkbookID, r.now()); err != nil {
		return Sheet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Sheet{}, err
	}
	return sheet, nil
}

func (r *PostgresRepository) DeleteSheet(ctx context.Context, sheetID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	var count int
	err = tx.QueryRow(ctx, `SELECT workbook_id::text,(SELECT count(*) FROM sheets s2 WHERE s2.workbook_id=s.workbook_id) FROM sheets s WHERE id=$1 FOR UPDATE`, sheetID).Scan(&workbookID, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if count <= 1 {
		return fmt.Errorf("%w: a workbook must contain at least one sheet", ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sheets WHERE id=$1`, sheetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, workbookID, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) ApplyCells(ctx context.Context, mutation CellMutation) (MutationResult, error) {
	if strings.TrimSpace(mutation.IdempotencyKey) == "" {
		return MutationResult{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	if len(mutation.Cells) == 0 || len(mutation.Cells) > 1000 {
		return MutationResult{}, fmt.Errorf("%w: cells must contain 1 to 1000 entries", ErrInvalid)
	}
	for _, cell := range mutation.Cells {
		if cell.Row < 1 || cell.Column < 1 {
			return MutationResult{}, fmt.Errorf("%w: row and column must be positive", ErrInvalid)
		}
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT w.id::text,w.version FROM workbooks w JOIN sheets s ON s.workbook_id=w.id WHERE s.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF w`, mutation.SheetID).Scan(&workbookID, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, ErrNotFound
	}
	if err != nil {
		return MutationResult{}, err
	}
	if mutation.BaseVersion > currentVersion {
		return MutationResult{}, ErrVersionAhead
	}
	if duplicate, ok, err := r.findDuplicate(ctx, tx, workbookID, mutation.ActorID, mutation.IdempotencyKey); err != nil {
		return MutationResult{}, err
	} else if ok {
		duplicate.Duplicate = true
		return duplicate, tx.Commit(ctx)
	}

	conflicts, err := r.findConflicts(ctx, tx, workbookID, mutation.SheetID, mutation.BaseVersion, mutation.ActorID, mutation.ClientID, mutation.Cells)
	if err != nil {
		return MutationResult{}, err
	}
	before := make(map[string]Cell, len(mutation.Cells))
	after := make(map[string]Cell, len(mutation.Cells))
	type blockKey struct{ row, column int }
	groups := make(map[blockKey][]CellInput)
	for _, input := range mutation.Cells {
		key := blockKey{(input.Row - 1) / cellBlockSize, (input.Column - 1) / cellBlockSize}
		groups[key] = append(groups[key], input)
	}
	now := r.now()
	for block, inputs := range groups {
		payload := make(map[string]Cell)
		var data []byte
		err := tx.QueryRow(ctx, `SELECT payload FROM cell_blocks WHERE sheet_id=$1 AND block_row=$2 AND block_column=$3 FOR UPDATE`, mutation.SheetID, block.row, block.column).Scan(&data)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return MutationResult{}, err
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &payload); err != nil {
				return MutationResult{}, err
			}
		}
		for _, input := range inputs {
			coordinate := coordinateKey(input.Row, input.Column)
			before[coordinate] = payload[coordinate]
			cell := Cell{SheetID: mutation.SheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), UpdatedAt: now}
			if isEmptyCell(cell) {
				delete(payload, coordinate)
			} else {
				payload[coordinate] = cell
			}
			after[coordinate] = cell
		}
		if len(payload) == 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM cell_blocks WHERE sheet_id=$1 AND block_row=$2 AND block_column=$3`, mutation.SheetID, block.row, block.column); err != nil {
				return MutationResult{}, err
			}
		} else {
			data, _ = json.Marshal(payload)
			if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sheet_id,block_row,block_column) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, mutation.SheetID, block.row, block.column, data, now); err != nil {
				return MutationResult{}, err
			}
		}
	}
	serverVersion := currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{OperationID: identity.New(), WorkbookID: workbookID, SheetID: mutation.SheetID, BaseVersion: mutation.BaseVersion, ServerVersion: serverVersion, AppliedCells: len(mutation.Cells), Conflicts: conflicts, CreatedAt: now}
	document, _ := json.Marshal(operationDocument{Before: before, After: after, Conflicts: conflicts, AppliedCells: len(mutation.Cells)})
	_, err = tx.Exec(ctx, `INSERT INTO cell_operations(operation_id,idempotency_key,workbook_id,sheet_id,actor_id,client_id,base_version,server_version,operation_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'cells.batch',$9,$10)`, result.OperationID, mutation.IdempotencyKey, workbookID, mutation.SheetID, mutation.ActorID, mutation.ClientID, mutation.BaseVersion, serverVersion, document, now)
	if err != nil {
		return MutationResult{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (r *PostgresRepository) ReadRange(ctx context.Context, sheetID string, selected cellrange.Range) ([]Cell, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE id=$1)`, sheetID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT payload FROM cell_blocks WHERE sheet_id=$1 AND block_row BETWEEN $2 AND $3 AND block_column BETWEEN $4 AND $5`, sheetID, (selected.Start.Row-1)/cellBlockSize, (selected.End.Row-1)/cellBlockSize, (selected.Start.Column-1)/cellBlockSize, (selected.End.Column-1)/cellBlockSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Cell, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var payload map[string]Cell
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		for _, cell := range payload {
			if selected.Contains(cell.Row, cell.Column) {
				result = append(result, cell)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Row == result[j].Row {
			return result[i].Column < result[j].Column
		}
		return result[i].Row < result[j].Row
	})
	return result, nil
}

func (r *PostgresRepository) CreateVersion(ctx context.Context, workbookID, name, actorID string) (Version, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	} else if err != nil {
		return Version{}, err
	}
	rows, err := tx.Query(ctx, `SELECT b.sheet_id::text,b.block_row,b.block_column,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1`, workbookID)
	if err != nil {
		return Version{}, err
	}
	blocks := make([]snapshotBlock, 0)
	for rows.Next() {
		var block snapshotBlock
		if err := rows.Scan(&block.SheetID, &block.BlockRow, &block.BlockColumn, &block.Payload); err != nil {
			rows.Close()
			return Version{}, err
		}
		blocks = append(blocks, block)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Version{}, err
	}
	document, _ := json.Marshal(snapshotDocument{Blocks: blocks})
	version := Version{ID: identity.New(), WorkbookID: workbookID, WorkbookVersion: current, Name: strings.TrimSpace(name), ActorID: actorID, CreatedAt: r.now()}
	if _, err := tx.Exec(ctx, `INSERT INTO workbook_versions(id,workbook_id,workbook_version,name,actor_id,snapshot,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, version.ID, workbookID, current, version.Name, actorID, document, version.CreatedAt); err != nil {
		return Version{}, err
	}
	return version, tx.Commit(ctx)
}

func (r *PostgresRepository) ListVersions(ctx context.Context, workbookID string) ([]Version, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,workbook_id::text,workbook_version,name,actor_id,created_at FROM workbook_versions WHERE workbook_id=$1 ORDER BY created_at DESC`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Version, 0)
	for rows.Next() {
		var item Version
		if err := rows.Scan(&item.ID, &item.WorkbookID, &item.WorkbookVersion, &item.Name, &item.ActorID, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return result, nil
}

func (r *PostgresRepository) RestoreVersion(ctx context.Context, versionID, actorID string) (MutationResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	var data []byte
	if err := tx.QueryRow(ctx, `SELECT workbook_id::text,snapshot FROM workbook_versions WHERE id=$1`, versionID).Scan(&workbookID, &data); errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, ErrNotFound
	} else if err != nil {
		return MutationResult{}, err
	}
	var base int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&base); errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, ErrNotFound
	} else if err != nil {
		return MutationResult{}, err
	}
	var snapshot snapshotDocument
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM cell_blocks USING sheets WHERE cell_blocks.sheet_id=sheets.id AND sheets.workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	now := r.now()
	for _, block := range snapshot.Blocks {
		if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, block.SheetID, block.BlockRow, block.BlockColumn, block.Payload, now); err != nil {
			return MutationResult{}, err
		}
	}
	serverVersion := base + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{OperationID: identity.New(), WorkbookID: workbookID, BaseVersion: base, ServerVersion: serverVersion, CreatedAt: now}
	if _, err := tx.Exec(ctx, `INSERT INTO cell_operations(operation_id,idempotency_key,workbook_id,actor_id,base_version,server_version,operation_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,'version.restore','{}',$7)`, result.OperationID, "restore:"+versionID+":"+strconv.FormatInt(base, 10), workbookID, actorID, base, serverVersion, now); err != nil {
		return MutationResult{}, err
	}
	return result, tx.Commit(ctx)
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (r *PostgresRepository) listSheets(ctx context.Context, db queryer, workbookID string) ([]Sheet, error) {
	rows, err := db.Query(ctx, `SELECT id::text,workbook_id::text,name,position,properties,created_at FROM sheets WHERE workbook_id=$1 ORDER BY position,id`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Sheet, 0)
	for rows.Next() {
		var item Sheet
		var data []byte
		if err := rows.Scan(&item.ID, &item.WorkbookID, &item.Name, &item.Position, &data, &item.CreatedAt); err != nil {
			return nil, err
		}
		var properties sheetProperties
		_ = json.Unmarshal(data, &properties)
		item.Color, item.Hidden = properties.Color, properties.Hidden
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) findDuplicate(ctx context.Context, tx pgx.Tx, workbookID, actorID, key string) (MutationResult, bool, error) {
	var result MutationResult
	var documentData []byte
	err := tx.QueryRow(ctx, `SELECT operation_id::text,workbook_id::text,coalesce(sheet_id::text,''),base_version,server_version,payload,created_at FROM cell_operations WHERE workbook_id=$1 AND actor_id=$2 AND idempotency_key=$3`, workbookID, actorID, key).Scan(&result.OperationID, &result.WorkbookID, &result.SheetID, &result.BaseVersion, &result.ServerVersion, &documentData, &result.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, false, nil
	}
	if err != nil {
		return MutationResult{}, false, err
	}
	var document operationDocument
	if err := json.Unmarshal(documentData, &document); err != nil {
		return MutationResult{}, false, err
	}
	result.AppliedCells, result.Conflicts = document.AppliedCells, document.Conflicts
	return result, true, nil
}

func (r *PostgresRepository) findConflicts(ctx context.Context, tx pgx.Tx, workbookID, sheetID string, baseVersion int64, actorID, clientID string, inputs []CellInput) ([]CellConflict, error) {
	if baseVersion < 1 {
		baseVersion = 0
	}
	rows, err := tx.Query(ctx, `SELECT server_version,payload FROM cell_operations WHERE workbook_id=$1 AND sheet_id=$2 AND server_version>$3 AND ($5='' OR actor_id<>$4 OR client_id<>$5) ORDER BY server_version`, workbookID, sheetID, baseVersion, actorID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make(map[string]CellInput, len(inputs))
	for _, input := range inputs {
		targets[coordinateKey(input.Row, input.Column)] = input
	}
	byCoordinate := make(map[string]CellConflict)
	for rows.Next() {
		var version int64
		var data []byte
		if err := rows.Scan(&version, &data); err != nil {
			return nil, err
		}
		var document operationDocument
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, err
		}
		for coordinate, changed := range document.After {
			if input, ok := targets[coordinate]; ok {
				byCoordinate[coordinate] = CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: version, PreviousValue: changed.Value, SubmittedValue: cloneJSON(input.Value)}
			}
		}
	}
	result := make([]CellConflict, 0, len(byCoordinate))
	for _, conflict := range byCoordinate {
		result = append(result, conflict)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Row == result[j].Row {
			return result[i].Column < result[j].Column
		}
		return result[i].Row < result[j].Row
	})
	return result, rows.Err()
}

func coordinateKey(row, column int) string { return strconv.Itoa(row) + ":" + strconv.Itoa(column) }

func mapPostgresError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return ErrDuplicateName
	}
	return err
}
