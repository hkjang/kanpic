package workbook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

type cellKey struct{ row, column int }

type operation struct {
	result   MutationResult
	before   map[cellKey]Cell
	after    map[cellKey]Cell
	actorID  string
	clientID string
}

type snapshot struct {
	version Version
	cells   map[string]map[cellKey]Cell
}

type workbookState struct {
	workbook   Workbook
	sheets     map[string]Sheet
	cells      map[string]map[cellKey]Cell
	operations []operation
	idempotent map[string]MutationResult
	versions   []snapshot
}

type MemoryRepository struct {
	mu        sync.RWMutex
	workbooks map[string]*workbookState
	sheetToWB map[string]string
	now       func() time.Time
	imports   map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		workbooks: make(map[string]*workbookState),
		sheetToWB: make(map[string]string),
		now:       func() time.Time { return time.Now().UTC() },
		imports:   make(map[string]string),
	}
}

func (r *MemoryRepository) ImportWorkbook(_ context.Context, input ImportWorkbookInput) (Workbook, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" || len(input.Sheets) == 0 {
		return Workbook{}, fmt.Errorf("%w: title and at least one sheet are required", ErrInvalid)
	}
	if input.IdempotencyKey == "" {
		return Workbook{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
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
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := input.ActorID + ":" + input.IdempotencyKey
	if workbookID, exists := r.imports[key]; exists {
		state, ok := r.workbooks[workbookID]
		if !ok {
			return Workbook{}, ErrNotFound
		}
		return r.workbookWithSheets(state), nil
	}
	now := r.now()
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now}
	state := &workbookState{workbook: wb, sheets: make(map[string]Sheet), cells: make(map[string]map[cellKey]Cell), idempotent: make(map[string]MutationResult)}
	for position, imported := range input.Sheets {
		sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: strings.TrimSpace(imported.Name), Position: position, Color: imported.Color, CreatedAt: now}
		state.sheets[sheet.ID] = sheet
		state.cells[sheet.ID] = make(map[cellKey]Cell, len(imported.Cells))
		for _, inputCell := range imported.Cells {
			if inputCell.Row < 1 || inputCell.Column < 1 {
				return Workbook{}, fmt.Errorf("%w: row and column must be positive", ErrInvalid)
			}
			cell := Cell{SheetID: sheet.ID, Row: inputCell.Row, Column: inputCell.Column, Value: cloneJSON(inputCell.Value), Formula: inputCell.Formula, Style: cloneJSON(inputCell.Style), UpdatedAt: now}
			if !isEmptyCell(cell) {
				state.cells[sheet.ID][cellKey{cell.Row, cell.Column}] = cell
			}
		}
		r.sheetToWB[sheet.ID] = wb.ID
	}
	r.workbooks[wb.ID] = state
	r.imports[key] = wb.ID
	return r.workbookWithSheets(state), nil
}

func (r *MemoryRepository) ReadAllCells(_ context.Context, sheetID string) ([]Cell, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return nil, err
	}
	result := make([]Cell, 0, len(state.cells[sheetID]))
	for _, cell := range state.cells[sheetID] {
		result = append(result, cloneCell(cell))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Row == result[j].Row {
			return result[i].Column < result[j].Column
		}
		return result[i].Row < result[j].Row
	})
	return result, nil
}

func (r *MemoryRepository) CreateWorkbook(_ context.Context, input CreateWorkbookInput) (Workbook, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return Workbook{}, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now}
	sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: "Sheet1", Position: 0, CreatedAt: now}
	state := &workbookState{workbook: wb, sheets: map[string]Sheet{sheet.ID: sheet}, cells: map[string]map[cellKey]Cell{sheet.ID: {}}, idempotent: make(map[string]MutationResult)}
	r.workbooks[wb.ID] = state
	r.sheetToWB[sheet.ID] = wb.ID
	return r.workbookWithSheets(state), nil
}

func (r *MemoryRepository) ListWorkbooks(_ context.Context, workspaceID string) ([]Workbook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Workbook, 0, len(r.workbooks))
	for _, state := range r.workbooks {
		if workspaceID == "" || state.workbook.WorkspaceID == workspaceID {
			result = append(result, r.workbookWithSheets(state))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func (r *MemoryRepository) GetWorkbook(_ context.Context, id string) (Workbook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.workbooks[id]
	if !ok {
		return Workbook{}, ErrNotFound
	}
	return r.workbookWithSheets(state), nil
}

func (r *MemoryRepository) UpdateWorkbook(_ context.Context, id string, input UpdateWorkbookInput) (Workbook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[id]
	if !ok {
		return Workbook{}, ErrNotFound
	}
	if input.Title != nil {
		if strings.TrimSpace(*input.Title) == "" {
			return Workbook{}, fmt.Errorf("%w: title cannot be empty", ErrInvalid)
		}
		state.workbook.Title = strings.TrimSpace(*input.Title)
	}
	if input.Favorite != nil {
		state.workbook.Favorite = *input.Favorite
	}
	state.workbook.UpdatedAt = r.now()
	return r.workbookWithSheets(state), nil
}

func (r *MemoryRepository) DeleteWorkbook(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[id]
	if !ok {
		return ErrNotFound
	}
	for sheetID := range state.sheets {
		delete(r.sheetToWB, sheetID)
	}
	delete(r.workbooks, id)
	return nil
}

func (r *MemoryRepository) CreateSheet(_ context.Context, workbookID string, input CreateSheetInput) (Sheet, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Sheet{}, fmt.Errorf("%w: sheet name is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return Sheet{}, ErrNotFound
	}
	for _, sheet := range state.sheets {
		if strings.EqualFold(sheet.Name, name) {
			return Sheet{}, ErrDuplicateName
		}
	}
	sheet := Sheet{ID: identity.New(), WorkbookID: workbookID, Name: name, Position: len(state.sheets), Color: input.Color, CreatedAt: r.now()}
	state.sheets[sheet.ID] = sheet
	state.cells[sheet.ID] = make(map[cellKey]Cell)
	r.sheetToWB[sheet.ID] = workbookID
	r.bump(state)
	return sheet, nil
}

func (r *MemoryRepository) UpdateSheet(_ context.Context, sheetID string, input UpdateSheetInput) (Sheet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, sheet, err := r.sheetState(sheetID)
	if err != nil {
		return Sheet{}, err
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			return Sheet{}, fmt.Errorf("%w: sheet name cannot be empty", ErrInvalid)
		}
		for id, candidate := range state.sheets {
			if id != sheetID && strings.EqualFold(candidate.Name, name) {
				return Sheet{}, ErrDuplicateName
			}
		}
		sheet.Name = name
	}
	if input.Position != nil && *input.Position >= 0 {
		sheet.Position = *input.Position
	}
	if input.Color != nil {
		sheet.Color = *input.Color
	}
	if input.Hidden != nil {
		sheet.Hidden = *input.Hidden
	}
	state.sheets[sheetID] = sheet
	r.bump(state)
	return sheet, nil
}

func (r *MemoryRepository) DeleteSheet(_ context.Context, sheetID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return err
	}
	if len(state.sheets) == 1 {
		return fmt.Errorf("%w: a workbook must contain at least one sheet", ErrInvalid)
	}
	delete(state.sheets, sheetID)
	delete(state.cells, sheetID)
	delete(r.sheetToWB, sheetID)
	r.bump(state)
	return nil
}

func (r *MemoryRepository) ApplyCells(_ context.Context, mutation CellMutation) (MutationResult, error) {
	if strings.TrimSpace(mutation.IdempotencyKey) == "" {
		return MutationResult{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	if len(mutation.Cells) == 0 || len(mutation.Cells) > 1000 {
		return MutationResult{}, fmt.Errorf("%w: cells must contain 1 to 1000 entries", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, _, err := r.sheetState(mutation.SheetID)
	if err != nil {
		return MutationResult{}, err
	}
	key := mutation.ActorID + ":" + mutation.IdempotencyKey
	if existing, ok := state.idempotent[key]; ok {
		existing.Duplicate = true
		return existing, nil
	}
	if mutation.BaseVersion > state.workbook.Version {
		return MutationResult{}, ErrVersionAhead
	}
	conflicts := make([]CellConflict, 0)
	for _, input := range mutation.Cells {
		if input.Row < 1 || input.Column < 1 {
			return MutationResult{}, fmt.Errorf("%w: row and column must be positive", ErrInvalid)
		}
		coord := cellKey{input.Row, input.Column}
		current := state.cells[mutation.SheetID][coord]
		if mutation.BaseVersion < state.workbook.Version {
			if changedVersion := latestChange(state.operations, mutation.SheetID, coord, mutation.BaseVersion, mutation.ActorID, mutation.ClientID); changedVersion > 0 {
				conflicts = append(conflicts, CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: changedVersion, PreviousValue: cloneJSON(current.Value), SubmittedValue: cloneJSON(input.Value)})
			}
		}
	}
	expanded, recalculated, formulaErrors, err := recalculateCellInputs(state.cells[mutation.SheetID], mutation.Cells)
	if err != nil {
		return MutationResult{}, err
	}
	before := make(map[cellKey]Cell, len(expanded))
	after := make(map[cellKey]Cell, len(expanded))
	now := r.now()
	for _, input := range expanded {
		coord := cellKey{input.Row, input.Column}
		current := state.cells[mutation.SheetID][coord]
		before[coord] = cloneCell(current)
		cell := Cell{SheetID: mutation.SheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), UpdatedAt: now}
		if isEmptyCell(cell) {
			delete(state.cells[mutation.SheetID], coord)
		} else {
			state.cells[mutation.SheetID][coord] = cell
		}
		after[coord] = cloneCell(cell)
	}
	baseVersion := mutation.BaseVersion
	r.bump(state)
	result := MutationResult{OperationID: identity.New(), WorkbookID: state.workbook.ID, SheetID: mutation.SheetID, BaseVersion: baseVersion, ServerVersion: state.workbook.Version, AppliedCells: len(mutation.Cells), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, Conflicts: conflicts, CreatedAt: now}
	state.operations = append(state.operations, operation{result: result, before: before, after: after, actorID: mutation.ActorID, clientID: mutation.ClientID})
	state.idempotent[key] = result
	return result, nil
}

func (r *MemoryRepository) ReadRange(_ context.Context, sheetID string, selected cellrange.Range) ([]Cell, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, _, err := r.sheetState(sheetID)
	if err != nil {
		return nil, err
	}
	result := make([]Cell, 0)
	for _, cell := range state.cells[sheetID] {
		if selected.Contains(cell.Row, cell.Column) {
			result = append(result, cloneCell(cell))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Row == result[j].Row {
			return result[i].Column < result[j].Column
		}
		return result[i].Row < result[j].Row
	})
	return result, nil
}

func (r *MemoryRepository) CreateVersion(_ context.Context, workbookID, name, actorID string) (Version, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return Version{}, ErrNotFound
	}
	version := Version{ID: identity.New(), WorkbookID: workbookID, WorkbookVersion: state.workbook.Version, Name: strings.TrimSpace(name), ActorID: actorID, CreatedAt: r.now()}
	state.versions = append(state.versions, snapshot{version: version, cells: cloneAllCells(state.cells)})
	return version, nil
}

func (r *MemoryRepository) ListVersions(_ context.Context, workbookID string) ([]Version, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return nil, ErrNotFound
	}
	result := make([]Version, len(state.versions))
	for i, snap := range state.versions {
		result[len(result)-1-i] = snap.version
	}
	return result, nil
}

func (r *MemoryRepository) RestoreVersion(_ context.Context, versionID, actorID string) (MutationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, state := range r.workbooks {
		for _, snap := range state.versions {
			if snap.version.ID != versionID {
				continue
			}
			base := state.workbook.Version
			state.cells = cloneAllCells(snap.cells)
			r.bump(state)
			result := MutationResult{OperationID: identity.New(), WorkbookID: state.workbook.ID, BaseVersion: base, ServerVersion: state.workbook.Version, CreatedAt: r.now()}
			state.operations = append(state.operations, operation{result: result})
			return result, nil
		}
	}
	return MutationResult{}, ErrNotFound
}

func (r *MemoryRepository) workbookWithSheets(state *workbookState) Workbook {
	wb := state.workbook
	wb.Sheets = make([]Sheet, 0, len(state.sheets))
	for _, sheet := range state.sheets {
		wb.Sheets = append(wb.Sheets, sheet)
	}
	sort.Slice(wb.Sheets, func(i, j int) bool { return wb.Sheets[i].Position < wb.Sheets[j].Position })
	return wb
}

func (r *MemoryRepository) sheetState(sheetID string) (*workbookState, Sheet, error) {
	workbookID, ok := r.sheetToWB[sheetID]
	if !ok {
		return nil, Sheet{}, ErrNotFound
	}
	state := r.workbooks[workbookID]
	return state, state.sheets[sheetID], nil
}

func (r *MemoryRepository) bump(state *workbookState) {
	state.workbook.Version++
	state.workbook.UpdatedAt = r.now()
}

func latestChange(operations []operation, sheetID string, key cellKey, afterVersion int64, actorID, clientID string) int64 {
	var version int64
	for _, op := range operations {
		if op.result.ServerVersion <= afterVersion || op.result.SheetID != sheetID {
			continue
		}
		if clientID != "" && op.actorID == actorID && op.clientID == clientID {
			continue
		}
		if _, ok := op.after[key]; ok {
			version = op.result.ServerVersion
		}
	}
	return version
}

func isEmptyCell(cell Cell) bool {
	return len(bytes.TrimSpace(cell.Value)) == 0 && cell.Formula == "" && len(bytes.TrimSpace(cell.Style)) == 0
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneCell(cell Cell) Cell {
	cell.Value = cloneJSON(cell.Value)
	cell.Style = cloneJSON(cell.Style)
	return cell
}

func cloneAllCells(source map[string]map[cellKey]Cell) map[string]map[cellKey]Cell {
	result := make(map[string]map[cellKey]Cell, len(source))
	for sheetID, cells := range source {
		result[sheetID] = make(map[cellKey]Cell, len(cells))
		for key, cell := range cells {
			result[sheetID][key] = cloneCell(cell)
		}
	}
	return result
}
