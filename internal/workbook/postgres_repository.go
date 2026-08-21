package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const cellBlockSize = 64

type PostgresRepository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

type sheetProperties struct {
	Color  string      `json:"color,omitempty"`
	Hidden bool        `json:"hidden,omitempty"`
	Layout SheetLayout `json:"layout,omitempty"`
}

type operationDocument struct {
	Before             map[string]Cell       `json:"before,omitempty"`
	After              map[string]Cell       `json:"after,omitempty"`
	SubmittedCells     []CellCoordinate      `json:"submitted_cells,omitempty"`
	Conflicts          []CellConflict        `json:"conflicts,omitempty"`
	AppliedCells       int                   `json:"applied_cells"`
	RecalculatedCells  []CellCoordinate      `json:"recalculated_cells,omitempty"`
	FormulaErrors      []CellFormulaError    `json:"formula_errors,omitempty"`
	ValidationWarnings []ValidationViolation `json:"validation_warnings,omitempty"`
	UndoOfOperationID  string                `json:"undo_of_operation_id,omitempty"`
	BackupVersionID    string                `json:"backup_version_id,omitempty"`
	StructuralAxis     string                `json:"structural_axis,omitempty"`
	StructuralAction   string                `json:"structural_action,omitempty"`
	StructuralIndex    int                   `json:"structural_index,omitempty"`
	StructuralCount    int                   `json:"structural_count,omitempty"`
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
	SchemaVersion      int                 `json:"schema_version,omitempty"`
	Workbook           snapshotWorkbook    `json:"workbook,omitempty"`
	Sheets             []snapshotSheet     `json:"sheets,omitempty"`
	Blocks             []snapshotBlock     `json:"blocks"`
	Filters            []FilterView        `json:"filters,omitempty"`
	Validations        []DataValidation    `json:"validations,omitempty"`
	ConditionalFormats []ConditionalFormat `json:"conditional_formats,omitempty"`
	NamedRanges        []NamedRange        `json:"named_ranges,omitempty"`
	Charts             []Chart             `json:"charts,omitempty"`
	Pivots             []Pivot             `json:"pivots,omitempty"`
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
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now, LinkAccess: LinkAccessRestricted, LinkRole: RoleViewer, ViewerCanCopy: true}
	if _, err := tx.Exec(ctx, `INSERT INTO workbooks(id,workspace_id,title,owner_id,version,created_at,updated_at) VALUES($1,$2,$3,$4,1,$5,$5)`, wb.ID, wb.WorkspaceID, wb.Title, wb.OwnerID, now); err != nil {
		return Workbook{}, err
	}
	wb.Sheets = make([]Sheet, 0, len(input.Sheets))
	type importBlockKey struct{ row, column int }
	importedCells := make(map[string]map[cellKey]Cell, len(input.Sheets))
	sheets := make(map[string]Sheet, len(input.Sheets))
	for position, imported := range input.Sheets {
		sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: strings.TrimSpace(imported.Name), Position: position, Color: imported.Color, Layout: importedSheetLayout(imported.Layout), CreatedAt: now}
		properties, _ := json.Marshal(sheetProperties{Color: imported.Color, Layout: sheet.Layout})
		if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6)`, sheet.ID, wb.ID, sheet.Name, position, properties, now); err != nil {
			return Workbook{}, mapPostgresError(err)
		}
		// Input rules the file carried go through the same normalisation a
		// request does, then straight into the sheet they belong to.
		for index, rule := range imported.Validations {
			created, ok := importedValidationInput(rule, index)
			if !ok {
				continue
			}
			normalized, _, ruleErr := NewDataValidation(sheet.ID, input.ActorID, created)
			if ruleErr != nil {
				continue
			}
			normalized.ID, normalized.Revision, normalized.CreatedAt, normalized.UpdatedAt = identity.New(), 1, now, now
			normalized.CreatedBy, normalized.UpdatedBy = input.ActorID, input.ActorID
			if err := insertDataValidationTx(ctx, tx, normalized); err != nil {
				return Workbook{}, err
			}
		}
		importedCells[sheet.ID] = make(map[cellKey]Cell, len(imported.Cells))
		for _, inputCell := range imported.Cells {
			cell := Cell{SheetID: sheet.ID, Row: inputCell.Row, Column: inputCell.Column, Value: cloneJSON(inputCell.Value), Formula: inputCell.Formula, Style: cloneJSON(inputCell.Style), UpdatedAt: now}
			if isEmptyCell(cell) {
				continue
			}
			importedCells[sheet.ID][cellKey{cell.Row, cell.Column}] = cell
		}
		wb.Sheets = append(wb.Sheets, sheet)
		sheets[sheet.ID] = sheet
	}
	expanded, _, _, err := recalculateCellInputs(sheets, importedCells, wb.Sheets[0].ID, nil, true, nil, nil)
	if err != nil {
		return Workbook{}, err
	}
	for _, inputCell := range expanded {
		key := cellKey{inputCell.Row, inputCell.Column}
		cell := Cell{SheetID: inputCell.SheetID, Row: inputCell.Row, Column: inputCell.Column, Value: cloneJSON(inputCell.Value), Formula: inputCell.Formula, Style: cloneJSON(inputCell.Style), SpillSource: inputCell.SpillSource, UpdatedAt: now}
		if isEmptyCell(cell) {
			delete(importedCells[inputCell.SheetID], key)
		} else {
			importedCells[inputCell.SheetID][key] = cell
		}
	}
	for sheetID, cells := range importedCells {
		blocks := make(map[importBlockKey]map[string]Cell)
		for key, cell := range cells {
			block := importBlockKey{(key.row - 1) / cellBlockSize, (key.column - 1) / cellBlockSize}
			if blocks[block] == nil {
				blocks[block] = make(map[string]Cell)
			}
			blocks[block][coordinateKey(key.row, key.column)] = cell
		}
		for block, payload := range blocks {
			data, _ := json.Marshal(payload)
			if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, sheetID, block.row, block.column, data, now); err != nil {
				return Workbook{}, err
			}
		}
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
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now, LinkAccess: LinkAccessRestricted, LinkRole: RoleViewer, ViewerCanCopy: true}
	if _, err := tx.Exec(ctx, `INSERT INTO workbooks(id,workspace_id,title,owner_id,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)`, wb.ID, wb.WorkspaceID, wb.Title, wb.OwnerID, wb.Version, now); err != nil {
		return Workbook{}, err
	}
	sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: "Sheet1", Position: 0, Layout: defaultSheetLayout(), CreatedAt: now}
	properties, _ := json.Marshal(sheetProperties{Layout: sheet.Layout})
	if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6)`, sheet.ID, wb.ID, sheet.Name, sheet.Position, properties, now); err != nil {
		return Workbook{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workbook{}, err
	}
	wb.Sheets = []Sheet{sheet}
	return wb, nil
}

// ListWorkbooks returns only the workbooks the principal may open: the ones
// they own, the ones shared with them directly, through a department or an
// identity provider role, and the ones opened up by link access. Administrators
// see every workbook so they can audit and repair sharing.
func (r *PostgresRepository) ListWorkbooks(ctx context.Context, workspaceID string, principal AccessPrincipal) ([]Workbook, error) {
	identities := principal.identities()
	roles := principal.roleSet()
	closure, err := r.departmentClosure(ctx, principal)
	if err != nil {
		return nil, err
	}
	departmentIDs := make([]string, 0, len(closure))
	for id := range closure {
		departmentIDs = append(departmentIDs, id)
	}
	query := `SELECT id::text,workspace_id,title,owner_id,favorite,version,created_at,updated_at,link_access,link_role,sharing_locked,viewer_can_copy
		FROM workbooks WHERE deleted_at IS NULL`
	// Administrators see every workbook, so the visibility parameters are only
	// bound when the clause that reads them is part of the query.
	args := []any{}
	if !principal.Admin {
		args = append(args, identities, roles, departmentIDs)
		query += ` AND (
			lower(owner_id) = ANY($1)
			OR link_access IN ('organization','anyone')
			OR EXISTS (
				SELECT 1 FROM workbook_shares s WHERE s.workbook_id = workbooks.id AND (
					(s.principal_type='user' AND lower(s.principal_id) = ANY($1))
					OR (s.principal_type='role' AND lower(s.principal_id) = ANY($2))
					OR (s.principal_type='department' AND lower(s.principal_id) = ANY($3))
				)
			)
		)`
	}
	if workspaceID != "" {
		args = append(args, workspaceID)
		query += fmt.Sprintf(` AND workspace_id=$%d`, len(args))
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
		if err := rows.Scan(&wb.ID, &wb.WorkspaceID, &wb.Title, &wb.OwnerID, &wb.Favorite, &wb.Version, &wb.CreatedAt, &wb.UpdatedAt, &wb.LinkAccess, &wb.LinkRole, &wb.SharingLocked, &wb.ViewerCanCopy); err != nil {
			return nil, err
		}
		items = append(items, wb)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	shares, err := r.sharesFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	favorites, err := r.WorkbookFavorites(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Favorite = favorites[items[i].ID]
		items[i].Sheets, err = r.listSheets(ctx, r.pool, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i].SharedCount = len(shares[items[i].ID])
		access := resolveAccess(items[i].ID, principal, sharingFromWorkbook(items[i], shares[items[i].ID]), closure)
		items[i].AccessRole, items[i].AccessSource = access.Role, access.Source
	}
	return items, nil
}

func (r *PostgresRepository) GetWorkbook(ctx context.Context, id string) (Workbook, error) {
	var wb Workbook
	err := r.pool.QueryRow(ctx, `SELECT id::text,workspace_id,title,owner_id,favorite,version,created_at,updated_at,link_access,link_role,sharing_locked,viewer_can_copy FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&wb.ID, &wb.WorkspaceID, &wb.Title, &wb.OwnerID, &wb.Favorite, &wb.Version, &wb.CreatedAt, &wb.UpdatedAt, &wb.LinkAccess, &wb.LinkRole, &wb.SharingLocked, &wb.ViewerCanCopy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workbook{}, ErrNotFound
	}
	if err != nil {
		return Workbook{}, err
	}
	wb.Sheets, err = r.listSheets(ctx, r.pool, id)
	return wb, err
}

func (r *PostgresRepository) DuplicateWorkbook(ctx context.Context, id string, input DuplicateWorkbookInput) (Workbook, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Workbook{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var source Workbook
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id,title,owner_id FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&source.ID, &source.WorkspaceID, &source.Title, &source.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workbook{}, ErrNotFound
	}
	if err != nil {
		return Workbook{}, err
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = source.Title + " 복사본"
	}
	ownerID := strings.TrimSpace(input.OwnerID)
	if ownerID == "" {
		ownerID = source.OwnerID
	}
	now := r.now()
	duplicated := Workbook{ID: identity.New(), WorkspaceID: source.WorkspaceID, Title: title, OwnerID: ownerID, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.Exec(ctx, `INSERT INTO workbooks(id,workspace_id,title,owner_id,version,created_at,updated_at) VALUES($1,$2,$3,$4,1,$5,$5)`, duplicated.ID, duplicated.WorkspaceID, duplicated.Title, duplicated.OwnerID, now); err != nil {
		return Workbook{}, err
	}
	type copiedSheet struct {
		sourceID   string
		dest       Sheet
		properties []byte
	}
	rows, err := tx.Query(ctx, `SELECT id::text,name,position,properties FROM sheets WHERE workbook_id=$1 ORDER BY position,id`, source.ID)
	if err != nil {
		return Workbook{}, err
	}
	copiedSheets := make([]copiedSheet, 0)
	sheetIDs := make(map[string]string)
	for rows.Next() {
		var item copiedSheet
		if err := rows.Scan(&item.sourceID, &item.dest.Name, &item.dest.Position, &item.properties); err != nil {
			rows.Close()
			return Workbook{}, err
		}
		item.dest.ID = identity.New()
		item.dest.WorkbookID = duplicated.ID
		item.dest.CreatedAt = now
		var properties sheetProperties
		_ = json.Unmarshal(item.properties, &properties)
		item.dest.Color, item.dest.Hidden, item.dest.Layout = properties.Color, properties.Hidden, normalizeSheetLayout(properties.Layout)
		copiedSheets = append(copiedSheets, item)
		sheetIDs[item.sourceID] = item.dest.ID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Workbook{}, err
	}
	rows.Close()
	for _, item := range copiedSheets {
		if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6)`, item.dest.ID, duplicated.ID, item.dest.Name, item.dest.Position, item.properties, now); err != nil {
			return Workbook{}, err
		}
		duplicated.Sheets = append(duplicated.Sheets, item.dest)
	}
	rows, err = tx.Query(ctx, `SELECT cb.sheet_id::text,cb.block_row,cb.block_column,cb.payload FROM cell_blocks cb JOIN sheets s ON s.id=cb.sheet_id WHERE s.workbook_id=$1`, source.ID)
	if err != nil {
		return Workbook{}, err
	}
	type copiedBlock struct {
		sheetID     string
		blockRow    int
		blockColumn int
		data        []byte
	}
	copiedBlocks := make([]copiedBlock, 0)
	for rows.Next() {
		var sourceSheetID string
		var item copiedBlock
		if err := rows.Scan(&sourceSheetID, &item.blockRow, &item.blockColumn, &item.data); err != nil {
			rows.Close()
			return Workbook{}, err
		}
		destinationSheetID, ok := sheetIDs[sourceSheetID]
		if !ok {
			rows.Close()
			return Workbook{}, fmt.Errorf("%w: cell block references an unknown sheet", ErrInvalid)
		}
		var payload map[string]Cell
		if err := json.Unmarshal(item.data, &payload); err != nil {
			rows.Close()
			return Workbook{}, err
		}
		for key, cell := range payload {
			cell.SheetID = destinationSheetID
			cell.UpdatedAt = now
			payload[key] = cell
		}
		item.data, err = json.Marshal(payload)
		if err != nil {
			rows.Close()
			return Workbook{}, err
		}
		item.sheetID = destinationSheetID
		copiedBlocks = append(copiedBlocks, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Workbook{}, err
	}
	rows.Close()
	for _, item := range copiedBlocks {
		if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, item.sheetID, item.blockRow, item.blockColumn, item.data, now); err != nil {
			return Workbook{}, err
		}
	}
	rows, err = tx.Query(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE w.id=$1 ORDER BY d.created_at,d.id`, source.ID)
	if err != nil {
		return Workbook{}, err
	}
	copiedValidations := make([]DataValidation, 0)
	for rows.Next() {
		rule, err := scanDataValidation(rows)
		if err != nil {
			rows.Close()
			return Workbook{}, err
		}
		copiedValidations = append(copiedValidations, rule)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Workbook{}, err
	}
	rows.Close()
	for _, sourceRule := range copiedValidations {
		destinationSheetID, found := sheetIDs[sourceRule.SheetID]
		if !found {
			return Workbook{}, fmt.Errorf("%w: data validation references an unknown sheet", ErrInvalid)
		}
		sourceRule.ID = identity.New()
		sourceRule.WorkbookID = duplicated.ID
		sourceRule.WorkbookVersion = 1
		sourceRule.SheetID = destinationSheetID
		sourceRule.CreateKey = "copy:" + sourceRule.ID
		sourceRule.Revision = 1
		sourceRule.CreatedBy = ownerID
		sourceRule.UpdatedBy = ownerID
		sourceRule.CreatedAt = now
		sourceRule.UpdatedAt = now
		if err := insertDataValidationTx(ctx, tx, sourceRule); err != nil {
			return Workbook{}, err
		}
	}
	copiedConditionalFormats, err := listWorkbookConditionalFormatsTx(ctx, tx, source.ID)
	if err != nil {
		return Workbook{}, err
	}
	for _, sourceRule := range copiedConditionalFormats {
		destinationSheetID, found := sheetIDs[sourceRule.SheetID]
		if !found {
			return Workbook{}, fmt.Errorf("%w: conditional format references an unknown sheet", ErrInvalid)
		}
		sourceRule.ID = identity.New()
		sourceRule.WorkbookID = duplicated.ID
		sourceRule.WorkbookVersion = 1
		sourceRule.SheetID = destinationSheetID
		sourceRule.CreateKey = "copy:" + sourceRule.ID
		sourceRule.Revision = 1
		sourceRule.CreatedBy, sourceRule.UpdatedBy = ownerID, ownerID
		sourceRule.CreatedAt, sourceRule.UpdatedAt = now, now
		if err := insertConditionalFormatTx(ctx, tx, sourceRule); err != nil {
			return Workbook{}, err
		}
	}
	copiedRanges, err := listNamedRangesTx(ctx, tx, source.ID)
	if err != nil {
		return Workbook{}, err
	}
	for _, sourceRange := range copiedRanges {
		destinationSheetID, found := sheetIDs[sourceRange.SheetID]
		if !found {
			return Workbook{}, fmt.Errorf("%w: named range references an unknown sheet", ErrInvalid)
		}
		sourceRange.ID = identity.New()
		sourceRange.WorkbookID = duplicated.ID
		sourceRange.WorkbookVersion = 1
		sourceRange.SheetID = destinationSheetID
		sourceRange.CreateKey = "copy:" + sourceRange.ID
		sourceRange.Revision = 1
		sourceRange.CreatedBy, sourceRange.UpdatedBy = ownerID, ownerID
		sourceRange.CreatedAt, sourceRange.UpdatedAt = now, now
		if err := insertNamedRangeTx(ctx, tx, sourceRange); err != nil {
			return Workbook{}, err
		}
	}
	copiedCharts, err := listChartsTx(ctx, tx, source.ID)
	if err != nil {
		return Workbook{}, err
	}
	for _, sourceChart := range copiedCharts {
		destinationSheetID, found := sheetIDs[sourceChart.SheetID]
		if !found {
			return Workbook{}, fmt.Errorf("%w: chart references an unknown sheet", ErrInvalid)
		}
		destinationSourceSheetID := ""
		if sourceChart.SourceRange != "#REF!" {
			var found bool
			destinationSourceSheetID, found = sheetIDs[sourceChart.SourceSheetID]
			if !found {
				return Workbook{}, fmt.Errorf("%w: chart source references an unknown sheet", ErrInvalid)
			}
		}
		sourceChart.ID = identity.New()
		sourceChart.WorkbookID = duplicated.ID
		sourceChart.WorkbookVersion = 1
		sourceChart.SheetID = destinationSheetID
		sourceChart.SourceSheetID = destinationSourceSheetID
		sourceChart.CreateKey = "copy:" + sourceChart.ID
		sourceChart.Revision = 1
		sourceChart.CreatedBy, sourceChart.UpdatedBy = ownerID, ownerID
		sourceChart.CreatedAt, sourceChart.UpdatedAt = now, now
		if err := insertChartTx(ctx, tx, sourceChart); err != nil {
			return Workbook{}, err
		}
	}
	copiedPivots, err := listPivotsTx(ctx, tx, source.ID)
	if err != nil {
		return Workbook{}, err
	}
	for _, sourcePivot := range copiedPivots {
		destinationSheetID, found := sheetIDs[sourcePivot.SheetID]
		if !found {
			return Workbook{}, fmt.Errorf("%w: pivot references an unknown sheet", ErrInvalid)
		}
		destinationSourceSheetID := ""
		if sourcePivot.SourceRange != "#REF!" {
			var found bool
			destinationSourceSheetID, found = sheetIDs[sourcePivot.SourceSheetID]
			if !found {
				return Workbook{}, fmt.Errorf("%w: pivot source references an unknown sheet", ErrInvalid)
			}
		}
		sourcePivot.ID = identity.New()
		sourcePivot.WorkbookID = duplicated.ID
		sourcePivot.WorkbookVersion = 1
		sourcePivot.SheetID = destinationSheetID
		sourcePivot.SourceSheetID = destinationSourceSheetID
		sourcePivot.CreateKey = "copy:" + sourcePivot.ID
		sourcePivot.SourceVersion = 0
		sourcePivot.LastRefreshedAt = nil
		sourcePivot.Revision = 1
		sourcePivot.CreatedBy, sourcePivot.UpdatedBy = ownerID, ownerID
		sourcePivot.CreatedAt, sourcePivot.UpdatedAt = now, now
		if err := insertPivotTx(ctx, tx, sourcePivot); err != nil {
			return Workbook{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Workbook{}, err
	}
	return duplicated, nil
}

func (r *PostgresRepository) UpdateWorkbook(ctx context.Context, id string, input UpdateWorkbookInput) (Workbook, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Workbook{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current Workbook
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id,title,owner_id,favorite,version,created_at,updated_at,link_access,link_role,sharing_locked,viewer_can_copy FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&current.ID, &current.WorkspaceID, &current.Title, &current.OwnerID, &current.Favorite, &current.Version, &current.CreatedAt, &current.UpdatedAt, &current.LinkAccess, &current.LinkRole, &current.SharingLocked, &current.ViewerCanCopy)
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

func (r *PostgresRepository) DeleteWorkbook(ctx context.Context, id, deletedBy string) error {
	command, err := r.pool.Exec(ctx, `UPDATE workbooks SET deleted_at=$2,updated_at=$2,deleted_by=$3 WHERE id=$1 AND deleted_at IS NULL`, id, r.now(), deletedBy)
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
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	} else if err != nil {
		return Sheet{}, err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE workbook_id=$1 AND lower(name)=lower($2))`, workbookID, name).Scan(&duplicate); err != nil {
		return Sheet{}, err
	}
	if duplicate {
		return Sheet{}, ErrDuplicateName
	}
	var position int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(position),-1)+1 FROM sheets WHERE workbook_id=$1`, workbookID).Scan(&position); err != nil {
		return Sheet{}, err
	}
	now := r.now()
	sheet := Sheet{ID: identity.New(), WorkbookID: workbookID, Name: name, Position: position, Color: input.Color, Layout: defaultSheetLayout(), CreatedAt: now}
	properties, _ := json.Marshal(sheetProperties{Color: input.Color, Layout: sheet.Layout})
	if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6)`, sheet.ID, workbookID, name, position, properties, now); err != nil {
		return Sheet{}, mapPostgresError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, workbookID, now); err != nil {
		return Sheet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Sheet{}, err
	}
	return sheet, nil
}

func (r *PostgresRepository) DuplicateSheet(ctx context.Context, sheetID string, input DuplicateSheetInput) (Sheet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Sheet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	if err := tx.QueryRow(ctx, `SELECT workbook_id::text FROM sheets WHERE id=$1`, sheetID).Scan(&workbookID); errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	} else if err != nil {
		return Sheet{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT id::text FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&workbookID); errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	} else if err != nil {
		return Sheet{}, err
	}
	var source Sheet
	var propertiesData []byte
	if err := tx.QueryRow(ctx, `SELECT id::text,workbook_id::text,name,position,properties,created_at FROM sheets WHERE id=$1 FOR UPDATE`, sheetID).Scan(&source.ID, &source.WorkbookID, &source.Name, &source.Position, &propertiesData, &source.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	} else if err != nil {
		return Sheet{}, err
	}
	rows, err := tx.Query(ctx, `SELECT name FROM sheets WHERE workbook_id=$1 ORDER BY position,id`, workbookID)
	if err != nil {
		return Sheet{}, err
	}
	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return Sheet{}, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Sheet{}, err
	}
	rows.Close()
	name, err := availableDuplicateName(source.Name, input.Name, names)
	if err != nil {
		return Sheet{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE sheets SET position=position+1 WHERE workbook_id=$1 AND position>$2`, workbookID, source.Position); err != nil {
		return Sheet{}, err
	}
	now := r.now()
	duplicated := Sheet{ID: identity.New(), WorkbookID: workbookID, Name: name, Position: source.Position + 1, CreatedAt: now}
	var properties sheetProperties
	_ = json.Unmarshal(propertiesData, &properties)
	duplicated.Color, duplicated.Hidden, duplicated.Layout = properties.Color, properties.Hidden, normalizeSheetLayout(properties.Layout)
	if _, err := tx.Exec(ctx, `INSERT INTO sheets(id,workbook_id,name,position,properties,created_at) VALUES($1,$2,$3,$4,$5,$6)`, duplicated.ID, workbookID, name, duplicated.Position, propertiesData, now); err != nil {
		return Sheet{}, mapPostgresError(err)
	}
	type copiedBlock struct {
		row, column int
		payload     []byte
	}
	blockRows, err := tx.Query(ctx, `SELECT block_row,block_column,payload FROM cell_blocks WHERE sheet_id=$1 ORDER BY block_row,block_column`, source.ID)
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
			cell.SheetID = duplicated.ID
			cell.UpdatedAt = now
			payload[coordinate] = cell
		}
		data, _ := json.Marshal(payload)
		if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5)`, duplicated.ID, block.row, block.column, data, now); err != nil {
			return Sheet{}, err
		}
	}
	validationRows, err := tx.Query(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE d.sheet_id=$1 ORDER BY d.created_at,d.id`, source.ID)
	if err != nil {
		return Sheet{}, err
	}
	validations := make([]DataValidation, 0)
	for validationRows.Next() {
		rule, err := scanDataValidation(validationRows)
		if err != nil {
			validationRows.Close()
			return Sheet{}, err
		}
		validations = append(validations, rule)
	}
	if err := validationRows.Err(); err != nil {
		validationRows.Close()
		return Sheet{}, err
	}
	validationRows.Close()
	for _, rule := range validations {
		rule.ID = identity.New()
		rule.SheetID = duplicated.ID
		rule.CreateKey = "copy:" + rule.ID
		rule.Revision = 1
		rule.CreatedAt = now
		rule.UpdatedAt = now
		if err := insertDataValidationTx(ctx, tx, rule); err != nil {
			return Sheet{}, err
		}
	}
	conditionalFormats, err := listConditionalFormatsTx(ctx, tx, source.ID)
	if err != nil {
		return Sheet{}, err
	}
	for _, rule := range conditionalFormats {
		rule.ID = identity.New()
		rule.SheetID = duplicated.ID
		rule.CreateKey = "copy:" + rule.ID
		rule.Revision = 1
		rule.CreatedAt, rule.UpdatedAt = now, now
		if err := insertConditionalFormatTx(ctx, tx, rule); err != nil {
			return Sheet{}, err
		}
	}
	chartRows, err := tx.Query(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.sheet_id=$1 ORDER BY c.created_at,c.id`, source.ID)
	if err != nil {
		return Sheet{}, err
	}
	charts := make([]Chart, 0)
	for chartRows.Next() {
		chart, scanErr := scanChart(chartRows)
		if scanErr != nil {
			chartRows.Close()
			return Sheet{}, scanErr
		}
		charts = append(charts, chart)
	}
	if err := chartRows.Err(); err != nil {
		chartRows.Close()
		return Sheet{}, err
	}
	chartRows.Close()
	for _, chart := range charts {
		chart.ID = identity.New()
		chart.SheetID = duplicated.ID
		if chart.SourceSheetID == source.ID {
			chart.SourceSheetID = duplicated.ID
		}
		chart.CreateKey = "copy:" + chart.ID
		chart.Revision = 1
		chart.CreatedAt, chart.UpdatedAt = now, now
		if err := insertChartTx(ctx, tx, chart); err != nil {
			return Sheet{}, err
		}
	}
	pivotRows, err := tx.Query(ctx, `SELECT `+pivotColumns+` FROM pivots p JOIN workbooks w ON w.id=p.workbook_id WHERE p.sheet_id=$1 ORDER BY p.created_at,p.id`, source.ID)
	if err != nil {
		return Sheet{}, err
	}
	pivots := make([]Pivot, 0)
	for pivotRows.Next() {
		pivot, scanErr := scanPivot(pivotRows)
		if scanErr != nil {
			pivotRows.Close()
			return Sheet{}, scanErr
		}
		pivots = append(pivots, pivot)
	}
	if err := pivotRows.Err(); err != nil {
		pivotRows.Close()
		return Sheet{}, err
	}
	pivotRows.Close()
	for _, pivot := range pivots {
		pivot.ID = identity.New()
		pivot.SheetID = duplicated.ID
		if pivot.SourceSheetID == source.ID {
			pivot.SourceSheetID = duplicated.ID
		}
		pivot.CreateKey = "copy:" + pivot.ID
		pivot.SourceVersion = 0
		pivot.LastRefreshedAt = nil
		pivot.Revision = 1
		pivot.CreatedAt, pivot.UpdatedAt = now, now
		if err := insertPivotTx(ctx, tx, pivot); err != nil {
			return Sheet{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, workbookID, now); err != nil {
		return Sheet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Sheet{}, err
	}
	return duplicated, nil
}

func (r *PostgresRepository) UpdateSheet(ctx context.Context, sheetID string, input UpdateSheetInput) (Sheet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Sheet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	err = tx.QueryRow(ctx, `SELECT workbook_id::text FROM sheets WHERE id=$1`, sheetID).Scan(&workbookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	}
	if err != nil {
		return Sheet{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT id::text FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&workbookID); errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	} else if err != nil {
		return Sheet{}, err
	}
	var sheet Sheet
	var propertiesData []byte
	if err := tx.QueryRow(ctx, `SELECT id::text,workbook_id::text,name,position,properties,created_at FROM sheets WHERE id=$1 FOR UPDATE`, sheetID).Scan(&sheet.ID, &sheet.WorkbookID, &sheet.Name, &sheet.Position, &propertiesData, &sheet.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
		return Sheet{}, ErrNotFound
	} else if err != nil {
		return Sheet{}, err
	}
	var properties sheetProperties
	_ = json.Unmarshal(propertiesData, &properties)
	properties.Layout = normalizeSheetLayout(properties.Layout)
	sheet.Color, sheet.Hidden, sheet.Layout = properties.Color, properties.Hidden, properties.Layout
	original := sheet
	originalProperties := properties
	if input.Name != nil {
		sheet.Name = strings.TrimSpace(*input.Name)
		if sheet.Name == "" {
			return Sheet{}, fmt.Errorf("%w: sheet name cannot be empty", ErrInvalid)
		}
		var duplicate bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE workbook_id=$1 AND id<>$2 AND lower(name)=lower($3))`, workbookID, sheetID, sheet.Name).Scan(&duplicate); err != nil {
			return Sheet{}, err
		}
		if duplicate {
			return Sheet{}, ErrDuplicateName
		}
	}
	if input.Position != nil {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sheets WHERE workbook_id=$1`, workbookID).Scan(&count); err != nil {
			return Sheet{}, err
		}
		if *input.Position < 0 || *input.Position >= count {
			return Sheet{}, fmt.Errorf("%w: position must be between 0 and %d", ErrInvalid, count-1)
		}
		sheet.Position = *input.Position
	}
	if input.Color != nil {
		properties.Color = *input.Color
	}
	if input.Hidden != nil {
		if *input.Hidden && !properties.Hidden {
			var visible int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM sheets WHERE workbook_id=$1 AND id<>$2 AND coalesce((properties->>'hidden')::boolean,false)=false`, workbookID, sheetID).Scan(&visible); err != nil {
				return Sheet{}, err
			}
			if visible == 0 {
				return Sheet{}, fmt.Errorf("%w: at least one sheet must stay visible", ErrInvalid)
			}
		}
		properties.Hidden = *input.Hidden
	}
	sheet.Color, sheet.Hidden, sheet.Layout = properties.Color, properties.Hidden, properties.Layout
	if reflect.DeepEqual(sheet, original) && reflect.DeepEqual(properties, originalProperties) {
		return sheet, tx.Commit(ctx)
	}
	if sheet.Position != original.Position {
		if original.Position < sheet.Position {
			if _, err := tx.Exec(ctx, `UPDATE sheets SET position=position-1 WHERE workbook_id=$1 AND id<>$2 AND position>$3 AND position<=$4`, workbookID, sheetID, original.Position, sheet.Position); err != nil {
				return Sheet{}, err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE sheets SET position=position+1 WHERE workbook_id=$1 AND id<>$2 AND position>=$3 AND position<$4`, workbookID, sheetID, sheet.Position, original.Position); err != nil {
			return Sheet{}, err
		}
	}
	propertiesData, _ = json.Marshal(properties)
	if _, err := tx.Exec(ctx, `UPDATE sheets SET name=$2,position=$3,properties=$4 WHERE id=$1`, sheetID, sheet.Name, sheet.Position, propertiesData); err != nil {
		return Sheet{}, mapPostgresError(err)
	}
	if sheet.Name != original.Name {
		type renamedBlock struct {
			sheetID     string
			row, column int
			payload     map[string]Cell
		}
		blocks := make([]renamedBlock, 0)
		rows, queryErr := tx.Query(ctx, `SELECT b.sheet_id::text,b.block_row,b.block_column,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1 FOR UPDATE OF b`, workbookID)
		if queryErr != nil {
			return Sheet{}, queryErr
		}
		for rows.Next() {
			var block renamedBlock
			var data []byte
			if scanErr := rows.Scan(&block.sheetID, &block.row, &block.column, &data); scanErr != nil {
				rows.Close()
				return Sheet{}, scanErr
			}
			if json.Unmarshal(data, &block.payload) != nil {
				rows.Close()
				return Sheet{}, fmt.Errorf("%w: stored cell block is invalid", ErrInvalid)
			}
			changed := false
			for key, cell := range block.payload {
				renamed := formula.RenameSheetReferences(cell.Formula, original.Name, sheet.Name)
				if renamed != cell.Formula {
					cell.Formula = renamed
					block.payload[key] = cell
					changed = true
				}
			}
			if changed {
				blocks = append(blocks, block)
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return Sheet{}, rowsErr
		}
		rows.Close()
		for _, block := range blocks {
			data, _ := json.Marshal(block.payload)
			if _, updateErr := tx.Exec(ctx, `UPDATE cell_blocks SET payload=$4,updated_at=$5 WHERE sheet_id=$1 AND block_row=$2 AND block_column=$3`, block.sheetID, block.row, block.column, data, r.now()); updateErr != nil {
				return Sheet{}, updateErr
			}
		}
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
	err = tx.QueryRow(ctx, `SELECT workbook_id::text FROM sheets WHERE id=$1`, sheetID).Scan(&workbookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT id::text FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&workbookID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var position, count int
	err = tx.QueryRow(ctx, `SELECT position,(SELECT count(*) FROM sheets WHERE workbook_id=$2) FROM sheets WHERE id=$1 FOR UPDATE`, sheetID, workbookID).Scan(&position, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if count <= 1 {
		return fmt.Errorf("%w: a workbook must contain at least one sheet", ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `UPDATE charts SET source_sheet_id=NULL,source_range='#REF!',revision=revision+1,updated_at=$2 WHERE source_sheet_id=$1 AND sheet_id<>$1`, sheetID, r.now()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE pivots SET source_sheet_id=NULL,source_range='#REF!',source_version=0,cached_result=NULL,refreshed_at=NULL,revision=revision+1,updated_at=$2 WHERE source_sheet_id=$1 AND sheet_id<>$1`, sheetID, r.now()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sheets WHERE id=$1`, sheetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sheets SET position=position-1 WHERE workbook_id=$1 AND position>$2`, workbookID, position); err != nil {
		return err
	}
	sheetList, err := r.listSheets(ctx, tx, workbookID)
	if err != nil {
		return err
	}
	sheets := make(map[string]Sheet, len(sheetList))
	existing := make(map[string]map[cellKey]Cell, len(sheetList))
	var currentSheetID string
	for _, sheet := range sheetList {
		sheets[sheet.ID] = sheet
		existing[sheet.ID] = make(map[cellKey]Cell)
		if currentSheetID == "" {
			currentSheetID = sheet.ID
		}
	}
	type deleteBlockKey struct {
		sheetID     string
		row, column int
	}
	payloads := make(map[deleteBlockKey]map[string]Cell)
	rows, err := tx.Query(ctx, `SELECT b.sheet_id::text,b.block_row,b.block_column,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1 FOR UPDATE OF b`, workbookID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var block deleteBlockKey
		var data []byte
		if err := rows.Scan(&block.sheetID, &block.row, &block.column, &data); err != nil {
			rows.Close()
			return err
		}
		payload := make(map[string]Cell)
		if json.Unmarshal(data, &payload) != nil {
			rows.Close()
			return fmt.Errorf("%w: stored cell block is invalid", ErrInvalid)
		}
		payloads[block] = payload
		for _, cell := range payload {
			existing[block.sheetID][cellKey{cell.Row, cell.Column}] = cell
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
	groups := make(map[deleteBlockKey][]CellInput)
	for _, input := range expanded {
		block := deleteBlockKey{sheetID: input.SheetID, row: (input.Row - 1) / cellBlockSize, column: (input.Column - 1) / cellBlockSize}
		groups[block] = append(groups[block], input)
	}
	now := r.now()
	for block, inputs := range groups {
		payload := payloads[block]
		if payload == nil {
			payload = make(map[string]Cell)
		}
		for _, input := range inputs {
			coordinate := coordinateKey(input.Row, input.Column)
			cell := Cell{SheetID: block.sheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), Note: input.Note, SpillSource: input.SpillSource, UpdatedAt: now}
			if isEmptyCell(cell) {
				delete(payload, coordinate)
			} else {
				payload[coordinate] = cell
			}
		}
		if len(payload) == 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM cell_blocks WHERE sheet_id=$1 AND block_row=$2 AND block_column=$3`, block.sheetID, block.row, block.column); err != nil {
				return err
			}
		} else {
			data, _ := json.Marshal(payload)
			if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sheet_id,block_row,block_column) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, block.sheetID, block.row, block.column, data, now); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, workbookID, now); err != nil {
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
	if len(mutation.StylePatch) > 0 {
		if err := ValidateStylePatch(mutation.StylePatch); err != nil {
			return MutationResult{}, err
		}
	}
	if mutation.Border != nil {
		if err := ValidateBorderCommand(*mutation.Border); err != nil {
			return MutationResult{}, err
		}
	}
	formatMutation := len(mutation.StylePatch) > 0 || mutation.Border != nil
	noteMutation := mutation.NotePatch != nil
	for _, cell := range mutation.Cells {
		if cell.Row < 1 || cell.Column < 1 {
			return MutationResult{}, fmt.Errorf("%w: row and column must be positive", ErrInvalid)
		}
		if !formatMutation {
			if err := ValidateCellStyle(cell); err != nil {
				return MutationResult{}, err
			}
		}
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	var currentVersion int64
	var workbookOwner string
	err = tx.QueryRow(ctx, `SELECT w.id::text,w.version,w.owner_id FROM workbooks w JOIN sheets s ON s.workbook_id=w.id WHERE s.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF w`, mutation.SheetID).Scan(&workbookID, &currentVersion, &workbookOwner)
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
	if mutation.RequireExactVersion && mutation.BaseVersion != currentVersion {
		return MutationResult{}, ErrVersionConflict
	}

	conflicts := make([]CellConflict, 0)
	if mutation.Expected == nil {
		conflicts, err = r.findConflicts(ctx, tx, workbookID, mutation.SheetID, mutation.BaseVersion, mutation.ActorID, mutation.ClientID, mutation.Cells)
		if err != nil {
			return MutationResult{}, err
		}
	}
	type blockKey struct {
		sheetID     string
		row, column int
	}
	payloads := make(map[blockKey]map[string]Cell)
	sheetList, err := r.listSheets(ctx, tx, workbookID)
	if err != nil {
		return MutationResult{}, err
	}
	sheets := make(map[string]Sheet, len(sheetList))
	existing := make(map[string]map[cellKey]Cell, len(sheetList))
	for _, sheet := range sheetList {
		sheets[sheet.ID] = sheet
		existing[sheet.ID] = make(map[cellKey]Cell)
	}
	rows, err := tx.Query(ctx, `SELECT b.sheet_id::text,b.block_row,b.block_column,b.payload FROM cell_blocks b JOIN sheets s ON s.id=b.sheet_id WHERE s.workbook_id=$1 FOR UPDATE OF b`, workbookID)
	if err != nil {
		return MutationResult{}, err
	}
	for rows.Next() {
		var block blockKey
		var data []byte
		if err := rows.Scan(&block.sheetID, &block.row, &block.column, &data); err != nil {
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
			existing[block.sheetID][cellKey{cell.Row, cell.Column}] = cell
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MutationResult{}, err
	}
	rows.Close()

	effective := make([]CellInput, 0, len(mutation.Cells))
	for _, input := range mutation.Cells {
		current := existing[mutation.SheetID][cellKey{input.Row, input.Column}]
		if mutation.Expected != nil {
			expected, exists := mutation.Expected[coordinateKey(input.Row, input.Column)]
			if !exists {
				return MutationResult{}, fmt.Errorf("%w: expected cell state is missing", ErrInvalid)
			}
			if !cellsEqual(current, expected) {
				changedVersion := currentVersion
				conflicts = append(conflicts, CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: changedVersion, BaseCell: conflictSnapshotFromCell(expected), ConflictingCell: conflictSnapshotFromCell(current), SubmittedCell: conflictSnapshotFromInput(input), PreviousValue: cloneJSON(current.Value), SubmittedValue: cloneJSON(input.Value)})
				continue
			}
		}
		for index := range conflicts {
			if conflicts[index].Row == input.Row && conflicts[index].Column == input.Column {
				if emptyConflictSnapshot(conflicts[index].ConflictingCell) {
					conflicts[index].ConflictingCell = conflictSnapshotFromCell(current)
					conflicts[index].PreviousValue = cloneJSON(current.Value)
				}
				if emptyConflictSnapshot(conflicts[index].SubmittedCell) {
					conflicts[index].SubmittedCell = conflictSnapshotFromInput(input)
				}
			}
		}
		if formatMutation {
			input, err = applyCellFormatting(current, input, mutation.StylePatch, mutation.Border)
			if err != nil {
				return MutationResult{}, err
			}
			if stylesEqual(current.Style, input.Style) {
				continue
			}
		}
		if noteMutation {
			if current.Note == *mutation.NotePatch {
				continue
			}
			input = applyCellNote(current, input, *mutation.NotePatch)
		}
		input.SheetID = mutation.SheetID
		effective = append(effective, input)
	}
	if mutation.Expected != nil && len(conflicts) > 0 && strings.HasPrefix(mutation.OperationType, "conflict.resolve.") {
		return MutationResult{}, ErrVersionConflict
	}
	if len(effective) == 0 && formatMutation {
		result := MutationResult{WorkbookID: workbookID, SheetID: mutation.SheetID, BaseVersion: mutation.BaseVersion, ServerVersion: currentVersion, Conflicts: conflicts, CreatedAt: r.now()}
		return result, tx.Commit(ctx)
	}
	// Protection is checked before anything is applied: a paste that touches a
	// protected block is refused whole rather than applied in part.
	protections, err := listProtectedRangesTx(ctx, tx, mutation.SheetID)
	if err != nil {
		return MutationResult{}, err
	}
	if blocked, _ := CheckProtectedRanges(protections, mutation.ActorID, workbookOwner, effective); len(blocked) > 0 {
		return MutationResult{}, &ProtectionFailure{Violations: blocked}
	}
	var expanded []CellInput
	var recalculated []CellCoordinate
	var formulaErrors []CellFormulaError
	var validationWarnings []ValidationViolation
	if formatMutation {
		expanded = append([]CellInput(nil), effective...)
	} else {
		namedRanges, rangeErr := listNamedRangesTx(ctx, tx, workbookID)
		if rangeErr != nil {
			return MutationResult{}, rangeErr
		}
		expanded, recalculated, formulaErrors, err = recalculateCellInputs(sheets, existing, mutation.SheetID, effective, false, formulaNamedRanges(namedRanges), r.importsFor(ctx, workbookID, existing, effective))
		if err != nil {
			return MutationResult{}, err
		}
		rules, err := listDataValidationsTx(ctx, tx, mutation.SheetID)
		if err != nil {
			return MutationResult{}, err
		}
		// A range dropdown checks against the list as it stands right now, so
		// the source is read inside the same transaction as the write.
		for index := range rules {
			rules[index] = r.resolveValidationSource(ctx, tx, rules[index])
		}
		validationWarnings, err = ValidateCellInputs(rules, existing[mutation.SheetID], inputsForSheet(expanded, mutation.SheetID), effective)
		if err != nil {
			return MutationResult{}, err
		}
	}
	before := make(map[string]Cell, len(expanded))
	after := make(map[string]Cell, len(expanded))
	groups := make(map[blockKey][]CellInput)
	for _, input := range expanded {
		sheetID := input.SheetID
		if sheetID == "" {
			sheetID = mutation.SheetID
		}
		key := blockKey{sheetID: sheetID, row: (input.Row - 1) / cellBlockSize, column: (input.Column - 1) / cellBlockSize}
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
			operationKey := operationCoordinateKey(block.sheetID, input.Row, input.Column)
			before[operationKey] = payload[coordinate]
			cell := Cell{SheetID: block.sheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), Note: input.Note, SpillSource: input.SpillSource, UpdatedAt: now}
			if isEmptyCell(cell) {
				delete(payload, coordinate)
			} else {
				payload[coordinate] = cell
			}
			after[operationKey] = cell
		}
		if len(payload) == 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM cell_blocks WHERE sheet_id=$1 AND block_row=$2 AND block_column=$3`, block.sheetID, block.row, block.column); err != nil {
				return MutationResult{}, err
			}
		} else {
			data, _ := json.Marshal(payload)
			if _, err := tx.Exec(ctx, `INSERT INTO cell_blocks(sheet_id,block_row,block_column,payload,updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sheet_id,block_row,block_column) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, block.sheetID, block.row, block.column, data, now); err != nil {
				return MutationResult{}, err
			}
		}
	}
	serverVersion := currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{OperationID: identity.New(), WorkbookID: workbookID, SheetID: mutation.SheetID, BaseVersion: mutation.BaseVersion, ServerVersion: serverVersion, AppliedCells: len(effective), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, ValidationWarnings: validationWarnings, CreatedAt: now}
	conflicts = finalizeCellConflicts(conflicts, mutation, result, func(row, column int) (Cell, bool) {
		cell, ok := after[operationCoordinateKey(mutation.SheetID, row, column)]
		if !ok {
			cell, ok = after[coordinateKey(row, column)]
		}
		return cell, ok
	}, now)
	result.Conflicts = conflicts
	operationType := mutation.OperationType
	if operationType == "" {
		operationType = "cells.batch"
	}
	document, _ := json.Marshal(operationDocument{Before: before, After: after, SubmittedCells: submittedCoordinates(effective), Conflicts: conflicts, AppliedCells: len(effective), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, ValidationWarnings: validationWarnings, UndoOfOperationID: mutation.UndoOfOperationID})
	_, err = tx.Exec(ctx, `INSERT INTO cell_operations(operation_id,idempotency_key,workbook_id,sheet_id,actor_id,client_id,base_version,server_version,operation_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, result.OperationID, mutation.IdempotencyKey, workbookID, mutation.SheetID, mutation.ActorID, mutation.ClientID, mutation.BaseVersion, serverVersion, operationType, document, now)
	if err != nil {
		return MutationResult{}, mapPostgresError(err)
	}
	for _, conflict := range conflicts {
		baseCell, _ := json.Marshal(conflict.BaseCell)
		conflictingCell, _ := json.Marshal(conflict.ConflictingCell)
		submittedCell, _ := json.Marshal(conflict.SubmittedCell)
		appliedCell, _ := json.Marshal(conflict.AppliedCell)
		if _, err := tx.Exec(ctx, `INSERT INTO cell_conflicts(id,workbook_id,sheet_id,operation_id,row_number,column_number,base_version,changed_at_version,server_version,actor_id,client_id,conflicting_actor_id,base_cell,conflicting_cell,submitted_cell,applied_cell,status,resolution,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'',1,$18,$18)`, conflict.ID, conflict.WorkbookID, conflict.SheetID, conflict.OperationID, conflict.Row, conflict.Column, conflict.BaseVersion, conflict.ChangedAtVersion, conflict.ServerVersion, conflict.ActorID, conflict.ClientID, conflict.ConflictingActorID, baseCell, conflictingCell, submittedCell, appliedCell, ConflictStatusOpen, now); err != nil {
			return MutationResult{}, mapPostgresError(err)
		}
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
			coordinate, err := parseOperationCoordinateKey(sheetID, key)
			if err != nil {
				return MutationResult{}, err
			}
			if coordinate.SheetID == "" || coordinate.SheetID == sheetID {
				coordinates = append(coordinates, coordinate)
			}
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
		cells = append(cells, inputFromCell(coordinate.Row, coordinate.Column, operationDocumentCell(document.Before, sheetID, coordinate.Row, coordinate.Column)))
		expected[key] = cloneCell(operationDocumentCell(document.After, sheetID, coordinate.Row, coordinate.Column))
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
	document := snapshotDocument{SchemaVersion: 8, Sheets: make([]snapshotSheet, 0), Blocks: make([]snapshotBlock, 0), Filters: make([]FilterView, 0), Validations: make([]DataValidation, 0), ConditionalFormats: make([]ConditionalFormat, 0), NamedRanges: make([]NamedRange, 0), Charts: make([]Chart, 0), Pivots: make([]Pivot, 0)}
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
	if err := rows.Err(); err != nil {
		return snapshotDocument{}, err
	}
	rows, err = tx.Query(ctx, `SELECT f.id::text,f.sheet_id::text,f.actor_id,f.idempotency_key,f.name,f.cell_range,f.header_rows,f.criteria,f.active,f.created_at,f.updated_at FROM filter_views f JOIN sheets s ON s.id=f.sheet_id WHERE s.workbook_id=$1 ORDER BY f.created_at,f.id`, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	for rows.Next() {
		view, scanErr := scanFilterView(rows)
		if scanErr != nil {
			rows.Close()
			return snapshotDocument{}, scanErr
		}
		document.Filters = append(document.Filters, view)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshotDocument{}, err
	}
	rows, err = tx.Query(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE w.id=$1 ORDER BY d.created_at,d.id`, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	for rows.Next() {
		rule, err := scanDataValidation(rows)
		if err != nil {
			rows.Close()
			return snapshotDocument{}, err
		}
		document.Validations = append(document.Validations, rule)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshotDocument{}, err
	}
	document.ConditionalFormats, err = listWorkbookConditionalFormatsTx(ctx, tx, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	document.NamedRanges, err = listNamedRangesTx(ctx, tx, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	document.Charts, err = listChartsTx(ctx, tx, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	document.Pivots, err = listPivotsTx(ctx, tx, workbookID)
	if err != nil {
		return snapshotDocument{}, err
	}
	return document, nil
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
	if _, err := tx.Exec(ctx, `DELETE FROM data_validations USING sheets WHERE data_validations.sheet_id=sheets.id AND sheets.workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM conditional_formats USING sheets WHERE conditional_formats.sheet_id=sheets.id AND sheets.workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM named_ranges WHERE workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM charts WHERE workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM pivots WHERE workbook_id=$1`, workbookID); err != nil {
		return MutationResult{}, err
	}
	if snapshot.SchemaVersion >= 5 {
		if _, err := tx.Exec(ctx, `DELETE FROM filter_views USING sheets WHERE filter_views.sheet_id=sheets.id AND sheets.workbook_id=$1`, workbookID); err != nil {
			return MutationResult{}, err
		}
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
	if snapshot.SchemaVersion >= 3 {
		for _, rule := range snapshot.Validations {
			if _, found := desiredSheetIDs[rule.SheetID]; !found {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains a validation for an unknown sheet", ErrInvalid)
			}
			normalized, _, err := NormalizeDataValidation(rule)
			if err != nil {
				return MutationResult{}, err
			}
			normalized.CreateKey = "restore:" + normalized.ID
			normalized.WorkbookID = workbookID
			normalized.WorkbookVersion = base + 1
			if normalized.Revision < 1 {
				normalized.Revision = 1
			}
			if normalized.CreatedBy == "" {
				normalized.CreatedBy = actorID
			}
			normalized.UpdatedBy = actorID
			if normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = now
			}
			normalized.UpdatedAt = now
			if err := insertDataValidationTx(ctx, tx, normalized); err != nil {
				return MutationResult{}, err
			}
		}
	}
	if snapshot.SchemaVersion >= 8 {
		for _, rule := range snapshot.ConditionalFormats {
			if _, found := desiredSheetIDs[rule.SheetID]; !found {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains a conditional format for an unknown sheet", ErrInvalid)
			}
			normalized, _, err := NormalizeConditionalFormat(rule)
			if err != nil {
				return MutationResult{}, err
			}
			normalized.CreateKey = "restore:" + normalized.ID
			normalized.WorkbookID = workbookID
			normalized.WorkbookVersion = base + 1
			if normalized.Revision < 1 {
				normalized.Revision = 1
			}
			if normalized.CreatedBy == "" {
				normalized.CreatedBy = actorID
			}
			normalized.UpdatedBy = actorID
			if normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = now
			}
			normalized.UpdatedAt = now
			if err := insertConditionalFormatTx(ctx, tx, normalized); err != nil {
				return MutationResult{}, err
			}
		}
	}
	if snapshot.SchemaVersion >= 5 {
		for _, view := range snapshot.Filters {
			if _, found := desiredSheetIDs[view.SheetID]; !found {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains a filter view for an unknown sheet", ErrInvalid)
			}
			normalized, _, err := NormalizeFilterView(view)
			if err != nil {
				return MutationResult{}, err
			}
			normalized.CreateKey = "restore:" + normalized.ID
			normalized.UpdatedAt = now
			if normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = now
			}
			if err := insertFilterViewForStructure(ctx, tx, normalized); err != nil {
				return MutationResult{}, err
			}
		}
	}
	if snapshot.SchemaVersion >= 4 {
		for _, item := range snapshot.NamedRanges {
			if _, found := desiredSheetIDs[item.SheetID]; !found {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains a named range for an unknown sheet", ErrInvalid)
			}
			normalized, err := normalizeStoredNamedRange(item)
			if err != nil {
				return MutationResult{}, err
			}
			normalized.CreateKey = "restore:" + normalized.ID
			normalized.WorkbookID = workbookID
			normalized.WorkbookVersion = base + 1
			if normalized.Revision < 1 {
				normalized.Revision = 1
			}
			if normalized.CreatedBy == "" {
				normalized.CreatedBy = actorID
			}
			normalized.UpdatedBy = actorID
			if normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = now
			}
			normalized.UpdatedAt = now
			if err := insertNamedRangeTx(ctx, tx, normalized); err != nil {
				return MutationResult{}, err
			}
		}
	}
	if snapshot.SchemaVersion >= 6 {
		for _, item := range snapshot.Charts {
			if _, found := desiredSheetIDs[item.SheetID]; !found {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains a chart on an unknown sheet", ErrInvalid)
			}
			if item.SourceRange != "#REF!" {
				if _, found := desiredSheetIDs[item.SourceSheetID]; !found {
					return MutationResult{}, fmt.Errorf("%w: version snapshot contains a chart source on an unknown sheet", ErrInvalid)
				}
			}
			normalized, err := normalizeChart(item, true)
			if err != nil {
				return MutationResult{}, err
			}
			normalized.WorkbookID = workbookID
			normalized.WorkbookVersion = base + 1
			normalized.CreateKey = "restore:" + normalized.ID
			if normalized.Revision < 1 {
				normalized.Revision = 1
			}
			if normalized.CreatedBy == "" {
				normalized.CreatedBy = actorID
			}
			normalized.UpdatedBy = actorID
			if normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = now
			}
			normalized.UpdatedAt = now
			if err := insertChartTx(ctx, tx, normalized); err != nil {
				return MutationResult{}, err
			}
		}
	}
	if snapshot.SchemaVersion >= 7 {
		for _, item := range snapshot.Pivots {
			if _, found := desiredSheetIDs[item.SheetID]; !found {
				return MutationResult{}, fmt.Errorf("%w: version snapshot contains a pivot on an unknown sheet", ErrInvalid)
			}
			if item.SourceRange != "#REF!" {
				if _, found := desiredSheetIDs[item.SourceSheetID]; !found {
					return MutationResult{}, fmt.Errorf("%w: version snapshot contains a pivot source on an unknown sheet", ErrInvalid)
				}
			}
			normalized, err := normalizePivot(item, true)
			if err != nil {
				return MutationResult{}, err
			}
			normalized.WorkbookID = workbookID
			normalized.WorkbookVersion = base + 1
			normalized.CreateKey = "restore:" + normalized.ID
			normalized.SourceVersion = 0
			normalized.LastRefreshedAt = nil
			if normalized.Revision < 1 {
				normalized.Revision = 1
			}
			if normalized.CreatedBy == "" {
				normalized.CreatedBy = actorID
			}
			normalized.UpdatedBy = actorID
			if normalized.CreatedAt.IsZero() {
				normalized.CreatedAt = now
			}
			normalized.UpdatedAt = now
			if err := insertPivotTx(ctx, tx, normalized); err != nil {
				return MutationResult{}, err
			}
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
		item.Color, item.Hidden, item.Layout = properties.Color, properties.Hidden, normalizeSheetLayout(properties.Layout)
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
	result.ValidationWarnings = document.ValidationWarnings
	result.Conflicts = document.Conflicts
	result.BackupVersionID = document.BackupVersionID
	result.StructuralAxis = document.StructuralAxis
	result.StructuralAction = document.StructuralAction
	result.StructuralIndex = document.StructuralIndex
	result.StructuralCount = document.StructuralCount
	return result, true, nil
}

func (r *PostgresRepository) findConflicts(ctx context.Context, tx pgx.Tx, workbookID, sheetID string, baseVersion int64, actorID, clientID string, inputs []CellInput) ([]CellConflict, error) {
	if baseVersion < 1 {
		baseVersion = 0
	}
	rows, err := tx.Query(ctx, `SELECT coalesce(sheet_id::text,''),server_version,operation_type,payload,actor_id FROM cell_operations WHERE workbook_id=$1 AND server_version>$2 AND ($4='' OR actor_id<>$3 OR client_id<>$4) ORDER BY server_version`, workbookID, baseVersion, actorID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make(map[string]CellInput, len(inputs))
	for _, input := range inputs {
		targets[coordinateKey(input.Row, input.Column)] = input
	}
	byCoordinate := make(map[string]CellConflict)
	baseCaptured := make(map[string]bool)
	for rows.Next() {
		var operationSheetID string
		var version int64
		var operationType string
		var data []byte
		var conflictingActor string
		if err := rows.Scan(&operationSheetID, &version, &operationType, &data, &conflictingActor); err != nil {
			return nil, err
		}
		if structuralOperationType(operationType) {
			for coordinate, input := range targets {
				current := byCoordinate[coordinate]
				current.Row, current.Column, current.ChangedAtVersion = input.Row, input.Column, version
				current.ConflictingActorID = conflictingActor
				current.SubmittedCell = conflictSnapshotFromInput(input)
				current.SubmittedValue = cloneJSON(input.Value)
				byCoordinate[coordinate] = current
			}
			continue
		}
		var document operationDocument
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, err
		}
		for coordinate, changed := range document.After {
			located, parseErr := parseOperationCoordinateKey(operationSheetID, coordinate)
			if parseErr != nil {
				return nil, parseErr
			}
			if located.SheetID != sheetID {
				continue
			}
			if input, ok := targets[coordinateKey(located.Row, located.Column)]; ok {
				key := coordinateKey(located.Row, located.Column)
				current := byCoordinate[key]
				if !baseCaptured[key] {
					current.BaseCell = conflictSnapshotFromCell(operationDocumentCell(document.Before, operationSheetID, located.Row, located.Column))
					baseCaptured[key] = true
				}
				current.Row, current.Column, current.ChangedAtVersion = input.Row, input.Column, version
				current.ConflictingActorID = conflictingActor
				current.ConflictingCell = conflictSnapshotFromCell(changed)
				current.SubmittedCell = conflictSnapshotFromInput(input)
				current.PreviousValue = cloneJSON(changed.Value)
				current.SubmittedValue = cloneJSON(input.Value)
				byCoordinate[key] = current
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

func operationCoordinateKey(sheetID string, row, column int) string {
	return sheetID + "!" + coordinateKey(row, column)
}

func parseOperationCoordinateKey(defaultSheetID, value string) (CellCoordinate, error) {
	sheetID := defaultSheetID
	if index := strings.LastIndexByte(value, '!'); index >= 0 {
		sheetID = value[:index]
		value = value[index+1:]
	}
	coordinate, err := parseCoordinateKey(value)
	coordinate.SheetID = sheetID
	return coordinate, err
}

func operationDocumentCell(values map[string]Cell, sheetID string, row, column int) Cell {
	if cell, ok := values[operationCoordinateKey(sheetID, row, column)]; ok {
		return cell
	}
	return values[coordinateKey(row, column)]
}

func mapPostgresError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return ErrDuplicateName
	}
	return err
}
