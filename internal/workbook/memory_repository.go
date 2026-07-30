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
	result        MutationResult
	before        map[cellKey]Cell
	after         map[cellKey]Cell
	submitted     []CellCoordinate
	actorID       string
	clientID      string
	operationType string
	undoOf        string
}

type snapshot struct {
	version  Version
	workbook Workbook
	sheets   map[string]Sheet
	cells    map[string]map[cellKey]Cell
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
	changed := false
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return Workbook{}, fmt.Errorf("%w: title cannot be empty", ErrInvalid)
		}
		if title != state.workbook.Title {
			state.workbook.Title = title
			changed = true
		}
	}
	if input.Favorite != nil && *input.Favorite != state.workbook.Favorite {
		state.workbook.Favorite = *input.Favorite
		changed = true
	}
	if changed {
		r.bump(state)
	}
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

func (r *MemoryRepository) DuplicateSheet(_ context.Context, sheetID string, input DuplicateSheetInput) (Sheet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, source, err := r.sheetState(sheetID)
	if err != nil {
		return Sheet{}, err
	}
	name, err := availableDuplicateName(source.Name, input.Name, sheetNames(state.sheets))
	if err != nil {
		return Sheet{}, err
	}
	for id, sheet := range state.sheets {
		if sheet.Position > source.Position {
			sheet.Position++
			state.sheets[id] = sheet
		}
	}
	now := r.now()
	duplicated := Sheet{ID: identity.New(), WorkbookID: source.WorkbookID, Name: name, Position: source.Position + 1, Color: source.Color, Hidden: source.Hidden, CreatedAt: now}
	state.sheets[duplicated.ID] = duplicated
	state.cells[duplicated.ID] = make(map[cellKey]Cell, len(state.cells[source.ID]))
	for key, sourceCell := range state.cells[source.ID] {
		cell := cloneCell(sourceCell)
		cell.SheetID = duplicated.ID
		cell.UpdatedAt = now
		state.cells[duplicated.ID][key] = cell
	}
	r.sheetToWB[duplicated.ID] = source.WorkbookID
	r.bump(state)
	return duplicated, nil
}

func (r *MemoryRepository) UpdateSheet(_ context.Context, sheetID string, input UpdateSheetInput) (Sheet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, sheet, err := r.sheetState(sheetID)
	if err != nil {
		return Sheet{}, err
	}
	next := sheet
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
		next.Name = name
	}
	if input.Position != nil && (*input.Position < 0 || *input.Position >= len(state.sheets)) {
		return Sheet{}, fmt.Errorf("%w: position must be between 0 and %d", ErrInvalid, len(state.sheets)-1)
	}
	if input.Color != nil {
		next.Color = *input.Color
	}
	if input.Hidden != nil {
		next.Hidden = *input.Hidden
	}
	if input.Position != nil && *input.Position != sheet.Position {
		target := *input.Position
		for id, candidate := range state.sheets {
			if id == sheetID {
				continue
			}
			if sheet.Position < target && candidate.Position > sheet.Position && candidate.Position <= target {
				candidate.Position--
			} else if sheet.Position > target && candidate.Position >= target && candidate.Position < sheet.Position {
				candidate.Position++
			}
			state.sheets[id] = candidate
		}
		next.Position = target
	}
	if next == sheet {
		return sheet, nil
	}
	state.sheets[sheetID] = next
	r.bump(state)
	return next, nil
}

func (r *MemoryRepository) DeleteSheet(_ context.Context, sheetID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, deleted, err := r.sheetState(sheetID)
	if err != nil {
		return err
	}
	if len(state.sheets) == 1 {
		return fmt.Errorf("%w: a workbook must contain at least one sheet", ErrInvalid)
	}
	delete(state.sheets, sheetID)
	delete(state.cells, sheetID)
	delete(r.sheetToWB, sheetID)
	for id, sheet := range state.sheets {
		if sheet.Position > deleted.Position {
			sheet.Position--
			state.sheets[id] = sheet
		}
	}
	r.bump(state)
	return nil
}

func (r *MemoryRepository) ApplyCells(_ context.Context, mutation CellMutation) (MutationResult, error) {
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
	effective := make([]CellInput, 0, len(mutation.Cells))
	for _, input := range mutation.Cells {
		if input.Row < 1 || input.Column < 1 {
			return MutationResult{}, fmt.Errorf("%w: row and column must be positive", ErrInvalid)
		}
		coord := cellKey{input.Row, input.Column}
		current := state.cells[mutation.SheetID][coord]
		if mutation.Expected != nil {
			expected, exists := mutation.Expected[coordinateKey(input.Row, input.Column)]
			if !exists {
				return MutationResult{}, fmt.Errorf("%w: expected cell state is missing", ErrInvalid)
			}
			if !cellsEqual(current, expected) {
				changedVersion := latestChange(state.operations, mutation.SheetID, coord, mutation.BaseVersion, mutation.ActorID, mutation.ClientID)
				if changedVersion == 0 {
					changedVersion = state.workbook.Version
				}
				conflicts = append(conflicts, CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: changedVersion, PreviousValue: cloneJSON(current.Value), SubmittedValue: cloneJSON(input.Value)})
				continue
			}
		} else if mutation.BaseVersion < state.workbook.Version {
			if changedVersion := latestChange(state.operations, mutation.SheetID, coord, mutation.BaseVersion, mutation.ActorID, mutation.ClientID); changedVersion > 0 {
				conflicts = append(conflicts, CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: changedVersion, PreviousValue: cloneJSON(current.Value), SubmittedValue: cloneJSON(input.Value)})
			}
		}
		if len(mutation.StylePatch) > 0 {
			input, err = applyStylePatch(current, input, mutation.StylePatch)
			if err != nil {
				return MutationResult{}, err
			}
			if stylesEqual(current.Style, input.Style) {
				continue
			}
		}
		effective = append(effective, input)
	}
	if len(effective) == 0 && len(mutation.StylePatch) > 0 {
		result := MutationResult{WorkbookID: state.workbook.ID, SheetID: mutation.SheetID, BaseVersion: mutation.BaseVersion, ServerVersion: state.workbook.Version, Conflicts: conflicts, CreatedAt: r.now()}
		state.idempotent[key] = result
		return result, nil
	}
	var expanded []CellInput
	var recalculated []CellCoordinate
	var formulaErrors []CellFormulaError
	if len(mutation.StylePatch) > 0 {
		expanded = append([]CellInput(nil), effective...)
	} else {
		expanded, recalculated, formulaErrors, err = recalculateCellInputs(state.cells[mutation.SheetID], effective)
		if err != nil {
			return MutationResult{}, err
		}
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
	result := MutationResult{OperationID: identity.New(), WorkbookID: state.workbook.ID, SheetID: mutation.SheetID, BaseVersion: baseVersion, ServerVersion: state.workbook.Version, AppliedCells: len(effective), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, Conflicts: conflicts, CreatedAt: now}
	operationType := mutation.OperationType
	if operationType == "" {
		operationType = "cells.batch"
	}
	state.operations = append(state.operations, operation{result: result, before: before, after: after, submitted: submittedCoordinates(effective), actorID: mutation.ActorID, clientID: mutation.ClientID, operationType: operationType, undoOf: mutation.UndoOfOperationID})
	state.idempotent[key] = result
	return result, nil
}

func (r *MemoryRepository) UndoOperation(ctx context.Context, input UndoOperationInput) (MutationResult, error) {
	if strings.TrimSpace(input.OperationID) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return MutationResult{}, fmt.Errorf("%w: operation_id and idempotency_key are required", ErrInvalid)
	}
	r.mu.RLock()
	var target *operation
	for _, state := range r.workbooks {
		for index := range state.operations {
			candidate := state.operations[index]
			if candidate.result.OperationID == input.OperationID && candidate.actorID == input.ActorID {
				copy := candidate
				target = &copy
				break
			}
		}
		if target != nil {
			break
		}
	}
	r.mu.RUnlock()
	if target == nil {
		return MutationResult{}, ErrNotFound
	}
	coordinates := append([]CellCoordinate(nil), target.submitted...)
	if len(coordinates) == 0 {
		for key := range target.after {
			coordinates = append(coordinates, CellCoordinate{Row: key.row, Column: key.column})
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
		key := cellKey{coordinate.Row, coordinate.Column}
		cells = append(cells, inputFromCell(coordinate.Row, coordinate.Column, target.before[key]))
		expected[coordinateKey(coordinate.Row, coordinate.Column)] = cloneCell(target.after[key])
	}
	return r.ApplyCells(ctx, CellMutation{SheetID: target.result.SheetID, ActorID: input.ActorID, ClientID: input.ClientID, BaseVersion: target.result.ServerVersion, IdempotencyKey: input.IdempotencyKey, Cells: cells, Expected: expected, OperationType: "operation.undo", UndoOfOperationID: input.OperationID})
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
	state.versions = append(state.versions, snapshot{version: version, workbook: state.workbook, sheets: cloneSheets(state.sheets), cells: cloneAllCells(state.cells)})
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
			backupVersion := Version{ID: identity.New(), WorkbookID: state.workbook.ID, WorkbookVersion: base, Name: "복원 전 자동 백업", ActorID: actorID, CreatedAt: r.now()}
			state.versions = append(state.versions, snapshot{version: backupVersion, workbook: state.workbook, sheets: cloneSheets(state.sheets), cells: cloneAllCells(state.cells)})
			for sheetID := range state.sheets {
				delete(r.sheetToWB, sheetID)
			}
			state.workbook.Title = snap.workbook.Title
			state.workbook.Favorite = snap.workbook.Favorite
			state.sheets = cloneSheets(snap.sheets)
			state.cells = cloneAllCells(snap.cells)
			for sheetID := range state.sheets {
				r.sheetToWB[sheetID] = state.workbook.ID
			}
			r.bump(state)
			result := MutationResult{OperationID: identity.New(), WorkbookID: state.workbook.ID, BaseVersion: base, ServerVersion: state.workbook.Version, CreatedAt: r.now()}
			state.operations = append(state.operations, operation{result: result})
			return result, nil
		}
	}
	return MutationResult{}, ErrNotFound
}

func cloneSheets(source map[string]Sheet) map[string]Sheet {
	result := make(map[string]Sheet, len(source))
	for id, sheet := range source {
		result[id] = sheet
	}
	return result
}

func sheetNames(source map[string]Sheet) []string {
	result := make([]string, 0, len(source))
	for _, sheet := range source {
		result = append(result, sheet.Name)
	}
	return result
}

func availableDuplicateName(sourceName, requested string, existing []string) (string, error) {
	used := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		used[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	if name := strings.TrimSpace(requested); name != "" {
		if _, found := used[strings.ToLower(name)]; found {
			return "", ErrDuplicateName
		}
		return name, nil
	}
	base := strings.TrimSpace(sourceName) + " 복사본"
	if _, found := used[strings.ToLower(base)]; !found {
		return base, nil
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s (%d)", base, suffix)
		if _, found := used[strings.ToLower(candidate)]; !found {
			return candidate, nil
		}
	}
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
