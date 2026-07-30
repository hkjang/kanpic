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
	Before            map[string]Cell    `json:"before,omitempty"`
	After             map[string]Cell    `json:"after,omitempty"`
	SubmittedCells    []CellCoordinate   `json:"submitted_cells,omitempty"`
	Conflicts         []CellConflict     `json:"conflicts,omitempty"`
	AppliedCells      int                `json:"applied_cells"`
	RecalculatedCells []CellCoordinate   `json:"recalculated_cells,omitempty"`
	FormulaErrors     []CellFormulaError `json:"formula_errors,omitempty"`
	UndoOfOperationID string             `json:"undo_of_operation_id,omitempty"`
}

type snapshotBlock struct {
	SheetID     string          `json:"sheet_id"`
	BlockRow    int             `json:"block_row"`
	BlockColumn int             `json:"block_column"`
	Payload     json.RawMessage `json:"payload"`
}

type snapshotWorkbook struct {
	Title    string `json:"title"`
	Favorite bool   `json:"favorite"`
}

type snapshotSheet struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Position   int             `json:"position"`
	Properties json.RawMessage `json:"properties"`
	CreatedAt  time.Time       `json:"created_at"`
}

type snapshotDocument struct {
	SchemaVersion int              `json:"schema_version,omitempty"`
	Workbook      snapshotWorkbook `json:"workbook,omitempty"`
	Sheets        []snapshotSheet  `json:"sheets,omitempty"`
	Blocks        []snapshotBlock  `json:"blocks"`
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

func (r *PostgresRepository) ImportWorkbook(ctx context.Context, input ImportWorkbookInput) (Workbook, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" || len(input.Sheets) == 0 {
		return Workbook{}, fmt.Errorf("%w: title and at least one sheet are required", ErrInvalid)
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return Workbook{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	var existingID string
	err := r.pool.QueryRow(ctx, `SELECT workbook_id::text FROM import_jobs WHERE actor_id=$1 AND idempotency_key=$2`, input.ActorID, input.IdempotencyKey).Scan(&existingID)
	if err == nil {
		return r.GetWorkbook(ctx, existingID)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Workbook{}, err
	}
	names := make(map[string]struct{}, len(input.Sheets))
	cellCount := 0
	for _, imported := range input.Sheets {
		name := strings.ToLower(strings.TrimSpace(imported.Name))
		if name == "" {
			return Workbook{}, fmt.Errorf("%w: sheet name is required", ErrInvalid)
		}
		if _, exists := names[name]; exists {
			return Workbook{}, ErrDuplicateName
		}
		names[name] = struct{}{}
		cellCount += len(imported.Cells)
		if cellCount > 1_000_000 {
			return Workbook{}, fmt.Errorf("%w: import exceeds one million cells", ErrInvalid)
		}
		for _, cell := range imported.Cells {
			if cell.Row < 1 || cell.Column < 1 {
				return Workbook{}, fmt.Errorf("%w: row and column must be positive", ErrInvalid)
			}
		}
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Workbook{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := r.now()
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.Exec(ctx, `INSERT INTO workbooks(id,workspace_id,title,owner_id,version,created_at,updated_at) VALUES($1,$2,$3,$4,1,$5,$5)`, wb.ID, wb.WorkspaceID, wb.Title, wb.OwnerID, now); err != nil {
		return Workbook{}, err
	}
	wb.Sheets = make([]Sheet, 0, len(input.Sheets))
	type importBlockKey struct{ row, column int }
	for position, imported := range input.Sheets {
		sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: strings.TrimSpace(imported.Name), Position: position, Color: imported.Color, CreatedAt: now}
		properties, _ := json.Marshal(sheetProperties{Color: imported.Color})
		if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6)`, sheet.ID, wb.ID, sheet.Name, position, properties, now); err != nil {
			return Workbook{}, mapPostgresError(err)
		}
		blocks := make(map[importBlockKey]map[string]Cell)
		for _, inputCell := range imported.Cells {
			cell := Cell{SheetID: sheet.ID, Row: inputCell.Row, Column: inputCell.Column, Value: cloneJSON(inputCell.Value), Formula: inputCell.Formula, Style: cloneJSON(inputCell.Style), UpdatedAt: now}
			if isEmptyCell(cell) {
				continue
			}
			block := importBlockKey{(cell.Row - 1) / cellBlockSize, (cell.Column - 1) / cellBlockSize}
			if blocks[block] == nil {
				blocks[block] = make(map[string]Cell)
			}
			blocks[block][coordinateKey(cell.Row, cell.Column)] = cell
		}
		for block, payload := range blocks {
			data, _ := json.Marshal(payload)
			if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, sheet.ID, block.row, block.column, data, now); err != nil {
				return Workbook{}, err
			}
		}
		wb.Sheets = append(wb.Sheets, sheet)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO import_jobs(id,actor_id,idempotency_key,file_name,format,workbook_id,cell_count,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, identity.New(), input.ActorID, input.IdempotencyKey, input.FileName, input.Format, wb.ID, cellCount, now); err != nil {
		return Workbook{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workbook{}, err
	}
	return wb, nil
}

func (r *PostgresRepository) ReadAllCells(ctx context.Context, sheetID string) ([]Cell, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE id=$1)`, sheetID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT payload FROM cell_blocks WHERE sheet_id=$1`, sheetID)
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
			result = append(result, cell)
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
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Workbook{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current Workbook
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id,title,owner_id,favorite,version,created_at,updated_at FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&current.ID, &current.WorkspaceID, &current.Title, &current.OwnerID, &current.Favorite, &current.Version, &current.CreatedAt, &current.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workbook{}, ErrNotFound
	}
	if err != nil {
		return Workbook{}, err
	}
	changed := false
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return Workbook{}, fmt.Errorf("%w: title cannot be empty", ErrInvalid)
		}
		if title != current.Title {
			current.Title = title
			changed = true
		}
	}
	if input.Favorite != nil && *input.Favorite != current.Favorite {
		current.Favorite = *input.Favorite
		changed = true
	}
	if !changed {
		current.Sheets, err = r.listSheets(ctx, tx, id)
		if err != nil {
			return Workbook{}, err
		}
		return current, tx.Commit(ctx)
	}
	err = tx.QueryRow(ctx, `UPDATE workbooks SET title=$2,favorite=$3,version=version+1,updated_at=$4 WHERE id=$1 AND deleted_at IS NULL RETURNING version,updated_at`, id, current.Title, current.Favorite, r.now()).Scan(&current.Version, &current.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workbook{}, ErrNotFound
	}
	if err != nil {
		return Workbook{}, err
	}
	current.Sheets, err = r.listSheets(ctx, tx, id)
	if err != nil {
		return Workbook{}, err
	}
	return current, tx.Commit(ctx)
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
	if len(mutation.Cells) == 0 || len(mutation.Cells) > MaxPasteCells {
		return MutationResult{}, fmt.Errorf("%w: cells must contain 1 to %d entries", ErrInvalid, MaxPasteCells)
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

	conflicts := make([]CellConflict, 0)
	if mutation.Expected == nil {
		conflicts, err = r.findConflicts(ctx, tx, workbookID, mutation.SheetID, mutation.BaseVersion, mutation.ActorID, mutation.ClientID, mutation.Cells)
		if err != nil {
			return MutationResult{}, err
		}
	}
	type blockKey struct{ row, column int }
	payloads := make(map[blockKey]map[string]Cell)
	existing := make(map[cellKey]Cell)
	rows, err := tx.Query(ctx, `SELECT block_row,block_column,payload FROM cell_blocks WHERE sheet_id=$1 FOR UPDATE`, mutation.SheetID)
	if err != nil {
		return MutationResult{}, err
	}
	for rows.Next() {
		var block blockKey
		var data []byte
		if err := rows.Scan(&block.row, &block.column, &data); err != nil {
			rows.Close()
			return MutationResult{}, err
		}
		payload := make(map[string]Cell)
		if err := json.Unmarshal(data, &payload); err != nil {
			rows.Close()
			return MutationResult{}, err
		}
		payloads[block] = payload
		for _, cell := range payload {
			existing[cellKey{cell.Row, cell.Column}] = cell
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MutationResult{}, err
	}
	rows.Close()

	effective := make([]CellInput, 0, len(mutation.Cells))
	for _, input := range mutation.Cells {
		if mutation.Expected != nil {
			expected, exists := mutation.Expected[coordinateKey(input.Row, input.Column)]
			if !exists {
				return MutationResult{}, fmt.Errorf("%w: expected cell state is missing", ErrInvalid)
			}
			current := existing[cellKey{input.Row, input.Column}]
			if !cellsEqual(current, expected) {
				changedVersion := currentVersion
				conflicts = append(conflicts, CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: changedVersion, PreviousValue: cloneJSON(current.Value), SubmittedValue: cloneJSON(input.Value)})
				continue
			}
		}
		effective = append(effective, input)
	}
	expanded, recalculated, formulaErrors, err := recalculateCellInputs(existing, effective)
	if err != nil {
		return MutationResult{}, err
	}
	before := make(map[string]Cell, len(expanded))
	after := make(map[string]Cell, len(expanded))
	groups := make(map[blockKey][]CellInput)
	for _, input := range expanded {
		key := blockKey{(input.Row - 1) / cellBlockSize, (input.Column - 1) / cellBlockSize}
		groups[key] = append(groups[key], input)
	}
	now := r.now()
	for block, inputs := range groups {
		payload := payloads[block]
		if payload == nil {
			payload = make(map[string]Cell)
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
			data, _ := json.Marshal(payload)
			if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sheet_id,block_row,block_column) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, mutation.SheetID, block.row, block.column, data, now); err != nil {
				return MutationResult{}, err
			}
		}
	}
	serverVersion := currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{OperationID: identity.New(), WorkbookID: workbookID, SheetID: mutation.SheetID, BaseVersion: mutation.BaseVersion, ServerVersion: serverVersion, AppliedCells: len(effective), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, Conflicts: conflicts, CreatedAt: now}
	operationType := mutation.OperationType
	if operationType == "" {
		operationType = "cells.batch"
	}
	document, _ := json.Marshal(operationDocument{Before: before, After: after, SubmittedCells: submittedCoordinates(effective), Conflicts: conflicts, AppliedCells: len(effective), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, UndoOfOperationID: mutation.UndoOfOperationID})
	_, err = tx.Exec(ctx, `INSERT INTO cell_operations(operation_id,idempotency_key,workbook_id,sheet_id,actor_id,client_id,base_version,server_version,operation_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, result.OperationID, mutation.IdempotencyKey, workbookID, mutation.SheetID, mutation.ActorID, mutation.ClientID, mutation.BaseVersion, serverVersion, operationType, document, now)
	if err != nil {
		return MutationResult{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (r *PostgresRepository) UndoOperation(ctx context.Context, input UndoOperationInput) (MutationResult, error) {
	if strings.TrimSpace(input.OperationID) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return MutationResult{}, fmt.Errorf("%w: operation_id and idempotency_key are required", ErrInvalid)
	}
	var sheetID string
	var targetVersion int64
	var documentData []byte
	err := r.pool.QueryRow(ctx, `SELECT coalesce(sheet_id::text,''),server_version,payload FROM cell_operations WHERE operation_id=$1 AND actor_id=$2`, input.OperationID, input.ActorID).Scan(&sheetID, &targetVersion, &documentData)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, ErrNotFound
	}
	if err != nil {
		return MutationResult{}, err
	}
	if sheetID == "" {
		return MutationResult{}, fmt.Errorf("%w: operation cannot be undone at cell level", ErrInvalid)
	}
	var document operationDocument
	if err := json.Unmarshal(documentData, &document); err != nil {
		return MutationResult{}, err
	}
	coordinates := append([]CellCoordinate(nil), document.SubmittedCells...)
	if len(coordinates) == 0 {
		for key := range document.After {
			coordinate, err := parseCoordinateKey(key)
			if err != nil {
				return MutationResult{}, err
			}
			coordinates = append(coordinates, coordinate)
		}
		sort.Slice(coordinates, func(i, j int) bool {
			if coordinates[i].Row == coordinates[j].Row {
				return coordinates[i].Column < coordinates[j].Column
			}
			return coordinates[i].Row < coordinates[j].Row
		})
	}
	if len(coordinates) == 0 {
		return MutationResult{}, fmt.Errorf("%w: operation has no cells to undo", ErrInvalid)
	}
	cells := make([]CellInput, 0, len(coordinates))
	expected := make(map[string]Cell, len(coordinates))
	for _, coordinate := range coordinates {
		key := coordinateKey(coordinate.Row, coordinate.Column)
		cells = append(cells, inputFromCell(coordinate.Row, coordinate.Column, document.Before[key]))
		expected[key] = cloneCell(document.After[key])
	}
	return r.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: input.ActorID, ClientID: input.ClientID, BaseVersion: targetVersion, IdempotencyKey: input.IdempotencyKey, Cells: cells, Expected: expected, OperationType: "operation.undo", UndoOfOperationID: input.OperationID})
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
	document, err := r.buildSnapshot(ctx, tx, workbookID)
	if err != nil {
		return Version{}, err
	}
	version, err := r.insertSnapshot(ctx, tx, workbookID, current, name, actorID, document)
	if err != nil {
		return Version{}, err
	}
	return version, tx.Commit(ctx)
}

func (r *PostgresRepository) buildSnapshot(ctx context.Context, tx pgx.Tx, workbookID string) (snapshotDocument, error) {
	document := snapshotDocument{SchemaVersion: 2, Sheets: make([]snapshotSheet, 0), Blocks: make([]snapshotBlock, 0)}
	if err := tx.QueryRow(ctx, `SELECT title,favorite FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&document.Workbook.Title, &document.Workbook.Favorite); errors.Is(err, pgx.ErrNoRows) {
		return snapshotDocument{}, ErrNotFound
	} else if err != nil {
		return snapshotDocument{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,name,position,properties,created_at FROM sheets WHERE workbook_id=$1 ORDER BY position,id`, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	for rows.Next() {
		var sheet snapshotSheet
		if err := rows.Scan(&sheet.ID, &sheet.Name, &sheet.Position, &sheet.Properties, &sheet.CreatedAt); err != nil {
			rows.Close()
			return snapshotDocument{}, err
		}
		document.Sheets = append(document.Sheets, sheet)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshotDocument{}, err
	}
	if len(document.Sheets) == 0 {
		return snapshotDocument{}, fmt.Errorf("%w: a snapshot must contain at least one sheet", ErrInvalid)
	}
	rows, err = tx.Query(ctx, `SELECT b.sheet_id::text,b.block_row,b.block_column,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1 ORDER BY b.sheet_id,b.block_row,b.block_column`, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	for rows.Next() {
		var block snapshotBlock
		if err := rows.Scan(&block.SheetID, &block.BlockRow, &block.BlockColumn, &block.Payload); err != nil {
			rows.Close()
			return snapshotDocument{}, err
		}
		document.Blocks = append(document.Blocks, block)
	}
	rows.Close()
	return document, rows.Err()
}

func (r *PostgresRepository) insertSnapshot(ctx context.Context, tx pgx.Tx, workbookID string, workbookVersion int64, name, actorID string, document snapshotDocument) (Version, error) {
	data, err := json.Marshal(document)
	if err != nil {
		return Version{}, err
	}
	version := Version{ID: identity.New(), WorkbookID: workbookID, WorkbookVersion: workbookVersion, Name: strings.TrimSpace(name), ActorID: actorID, CreatedAt: r.now()}
	if _, err := tx.Exec(ctx, `INSERT INTO workbook_versions(id,workbook_id,workbook_version,name,actor_id,snapshot,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, version.ID, workbookID, workbookVersion, version.Name, actorID, data, version.CreatedAt); err != nil {
		return Version{}, err
	}
	return version, nil
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
	currentSnapshot, err := r.buildSnapshot(ctx, tx, workbookID)
	if err != nil {
		return MutationResult{}, err
	}
	backup, err := r.insertSnapshot(ctx, tx, workbookID, base, "복원 전 자동 백업", actorID, currentSnapshot)
	if err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM cell_blocks USING sheets WHERE cell_blocks.sheet_id=sheets.id AND sheets.workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	now := r.now()
	desiredSheetIDs := make(map[string]struct{})
	if snapshot.SchemaVersion >= 2 {
		if strings.TrimSpace(snapshot.Workbook.Title) == "" || len(snapshot.Sheets) == 0 {
			return MutationResult{}, fmt.Errorf("%w: version snapshot is missing workbook structure", ErrInvalid)
		}
		if _, err := tx.Exec(ctx, `UPDATE sheets SET name='__kanpic_restore__' || gen_random_uuid()::text WHERE workbook_id=$1`, workbookID); err != nil {
			return MutationResult{}, err
		}
		rows, err := tx.Query(ctx, `SELECT id::text FROM sheets WHERE workbook_id=$1`, workbookID)
		if err != nil {
			return MutationResult{}, err
		}
		currentSheetIDs := make([]string, 0)
		for rows.Next() {
			var sheetID string
			if err := rows.Scan(&sheetID); err != nil {
				rows.Close()
				return MutationResult{}, err
			}
			currentSheetIDs = append(currentSheetIDs, sheetID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return MutationResult{}, err
		}
		desiredSheetIDs = make(map[string]struct{}, len(snapshot.Sheets))
		for _, sheet := range snapshot.Sheets {
			if strings.TrimSpace(sheet.ID) == "" || strings.TrimSpace(sheet.Name) == "" {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains an invalid sheet", ErrInvalid)
			}
			if _, duplicate := desiredSheetIDs[sheet.ID]; duplicate {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains duplicate sheets", ErrInvalid)
			}
			desiredSheetIDs[sheet.ID] = struct{}{}
		}
		for _, sheetID := range currentSheetIDs {
			if _, keep := desiredSheetIDs[sheetID]; !keep {
				if _, err := tx.Exec(ctx, `DELETE FROM sheets WHERE id=$1 AND workbook_id=$2`, sheetID, workbookID); err != nil {
					return MutationResult{}, err
				}
			}
		}
		for _, sheet := range snapshot.Sheets {
			properties := sheet.Properties
			if len(properties) == 0 {
				properties = json.RawMessage(`{}`)
			}
			createdAt := sheet.CreatedAt
			if createdAt.IsZero() {
				createdAt = now
			}
			command, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET name=excluded.name,position=excluded.position,properties=excluded.properties,created_at=excluded.created_at WHERE sheets.workbook_id=excluded.workbook_id`, sheet.ID, workbookID, sheet.Name, sheet.Position, properties, createdAt)
			if err != nil {
				return MutationResult{}, mapPostgresError(err)
			}
			if command.RowsAffected() != 1 {
				return MutationResult{}, fmt.Errorf("%w: version snapshot references a sheet owned by another workbook", ErrInvalid)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE workbooks SET title=$2,favorite=$3 WHERE id=$1`, workbookID, snapshot.Workbook.Title, snapshot.Workbook.Favorite); err != nil {
			return MutationResult{}, err
		}
	}
	for _, block := range snapshot.Blocks {
		if snapshot.SchemaVersion >= 2 {
			if _, found := desiredSheetIDs[block.SheetID]; !found {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains a block for an unknown sheet", ErrInvalid)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, block.SheetID, block.BlockRow, block.BlockColumn, block.Payload, now); err != nil {
			return MutationResult{}, err
		}
	}
	serverVersion := base + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{OperationID: identity.New(), WorkbookID: workbookID, BaseVersion: base, ServerVersion: serverVersion, CreatedAt: now}
	payload, _ := json.Marshal(map[string]string{"restored_version_id": versionID, "backup_version_id": backup.ID})
	if _, err := tx.Exec(ctx, `INSERT INTO cell_operations(operation_id,idempotency_key,workbook_id,actor_id,base_version,server_version,operation_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,'version.restore',$7,$8)`, result.OperationID, "restore:"+versionID+":"+strconv.FormatInt(base, 10), workbookID, actorID, base, serverVersion, payload, now); err != nil {
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
	result.AppliedCells = document.AppliedCells
	result.RecalculatedCells = document.RecalculatedCells
	result.FormulaErrors = document.FormulaErrors
	result.Conflicts = document.Conflicts
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
