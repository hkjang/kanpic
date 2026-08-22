package workbook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

type cellKey struct{ row, column int }

type operation struct {
	result        MutationResult
	before        map[scopedCellKey]Cell
	after         map[scopedCellKey]Cell
	submitted     []CellCoordinate
	actorID       string
	clientID      string
	operationType string
	undoOf        string
	structural    bool
}

type snapshot struct {
	version            Version
	workbook           Workbook
	sheets             map[string]Sheet
	cells              map[string]map[cellKey]Cell
	filters            map[string]FilterView
	validations        map[string]DataValidation
	protections        map[string]ProtectedRange
	conditionalFormats map[string]ConditionalFormat
	namedRanges        map[string]NamedRange
	charts             map[string]Chart
	pivots             map[string]Pivot
}

type workbookState struct {
	workbook         Workbook
	deletedAt        *time.Time
	deletedBy        string
	sheets           map[string]Sheet
	cells            map[string]map[cellKey]Cell
	operations       []operation
	idempotent       map[string]MutationResult
	layoutIdempotent map[string]SheetLayoutResult
	versions         []snapshot
}

type MemoryRepository struct {
	mu                 sync.RWMutex
	resolveMu          sync.Mutex
	workbooks          map[string]*workbookState
	sheetToWB          map[string]string
	now                func() time.Time
	imports            map[string]string
	filters            map[string]FilterView
	validations        map[string]DataValidation
	protections        map[string]ProtectedRange
	conditionalFormats map[string]ConditionalFormat
	namedRanges        map[string]NamedRange
	charts             map[string]Chart
	pivots             map[string]Pivot
	pivotCache         map[string]PivotData
	comments           map[string]CommentThread
	notifications      map[string]MentionNotification
	conflicts          map[string]CellConflict
	shares             map[string]map[string]WorkbookShare
	departments        map[string]Department
	departmentMembers  map[string][]string
	accessRequests     map[string]AccessRequest
	favorites          map[string]map[string]bool
	trash              map[string]*workbookState
	directory          map[string]DirectoryUser
	userRoles          map[string][]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		workbooks:          make(map[string]*workbookState),
		sheetToWB:          make(map[string]string),
		now:                func() time.Time { return time.Now().UTC() },
		imports:            make(map[string]string),
		filters:            make(map[string]FilterView),
		validations:        make(map[string]DataValidation),
		protections:        make(map[string]ProtectedRange),
		conditionalFormats: make(map[string]ConditionalFormat),
		namedRanges:        make(map[string]NamedRange),
		charts:             make(map[string]Chart),
		pivots:             make(map[string]Pivot),
		pivotCache:         make(map[string]PivotData),
		comments:           make(map[string]CommentThread),
		notifications:      make(map[string]MentionNotification),
		conflicts:          make(map[string]CellConflict),
		shares:             make(map[string]map[string]WorkbookShare),
		departments:        make(map[string]Department),
		departmentMembers:  make(map[string][]string),
		accessRequests:     make(map[string]AccessRequest),
		favorites:          make(map[string]map[string]bool),
		trash:              make(map[string]*workbookState),
		directory:          make(map[string]DirectoryUser),
		userRoles:          make(map[string][]string),
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
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now, LinkAccess: LinkAccessRestricted, LinkRole: RoleViewer, ViewerCanCopy: true}
	state := &workbookState{workbook: wb, sheets: make(map[string]Sheet), cells: make(map[string]map[cellKey]Cell), idempotent: make(map[string]MutationResult)}
	sheetIDs := make([]string, 0, len(input.Sheets))
	for position, imported := range input.Sheets {
		sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: strings.TrimSpace(imported.Name), Position: position, Color: imported.Color, Layout: importedSheetLayout(imported.Layout), CreatedAt: now}
		sheetIDs = append(sheetIDs, sheet.ID)
		state.sheets[sheet.ID] = sheet
		state.cells[sheet.ID] = make(map[cellKey]Cell, len(imported.Cells))
		for _, inputCell := range imported.Cells {
			if inputCell.Row < 1 || inputCell.Column < 1 {
				return Workbook{}, fmt.Errorf("%w: row and column must be positive", ErrInvalid)
			}
			cell := Cell{SheetID: sheet.ID, Row: inputCell.Row, Column: inputCell.Column, Value: cloneJSON(inputCell.Value), Formula: inputCell.Formula, Style: cloneJSON(inputCell.Style), Note: inputCell.Note, UpdatedAt: now}
			if !isEmptyCell(cell) {
				state.cells[sheet.ID][cellKey{cell.Row, cell.Column}] = cell
			}
		}
	}
	// Validation rules travel with the sheet they came from, and go through the
	// same normalisation a request does.
	for position, imported := range input.Sheets {
		sheetID := sheetIDs[position]
		for index, rule := range imported.ConditionalFormats {
			created, ok := importedConditionalInput(rule, index)
			if !ok {
				continue
			}
			normalized, _, err := NewConditionalFormat(sheetID, input.ActorID, created)
			if err != nil {
				continue
			}
			normalized.ID, normalized.WorkbookID = identity.New(), wb.ID
			normalized.CreatedAt, normalized.UpdatedAt, normalized.Revision = now, now, 1
			r.conditionalFormats[normalized.ID] = normalized
		}
		for index, rule := range imported.Validations {
			created, ok := importedValidationInput(rule, index)
			if !ok {
				continue
			}
			normalized, _, err := NewDataValidation(sheetID, input.ActorID, created)
			if err != nil {
				continue
			}
			normalized.ID, normalized.WorkbookID = identity.New(), wb.ID
			normalized.CreatedAt, normalized.UpdatedAt, normalized.Revision = now, now, 1
			r.validations[normalized.ID] = normalized
		}
	}
	currentSheetID := ""
	for sheetID := range state.sheets {
		currentSheetID = sheetID
		break
	}
	expanded, _, _, err := recalculateCellInputs(state.sheets, state.cells, currentSheetID, nil, true, nil, nil)
	if err != nil {
		return Workbook{}, err
	}
	for _, inputCell := range expanded {
		key := cellKey{inputCell.Row, inputCell.Column}
		cell := Cell{SheetID: inputCell.SheetID, Row: inputCell.Row, Column: inputCell.Column, Value: cloneJSON(inputCell.Value), Formula: inputCell.Formula, Style: cloneJSON(inputCell.Style), SpillSource: inputCell.SpillSource, UpdatedAt: now}
		// Recalculation rewrites the cell from its formula and knows nothing
		// about the note hanging on it.
		if previous, exists := state.cells[inputCell.SheetID][key]; exists {
			cell.Note = previous.Note
		}
		if isEmptyCell(cell) {
			delete(state.cells[inputCell.SheetID], key)
		} else {
			state.cells[inputCell.SheetID][key] = cell
		}
	}
	for sheetID := range state.sheets {
		r.sheetToWB[sheetID] = wb.ID
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

func (r *MemoryRepository) SearchWorkbook(_ context.Context, workbookID string, input SearchWorkbookInput) (WorkbookSearchResult, error) {
	normalized, err := normalizeSearchInput(input)
	if err != nil {
		return WorkbookSearchResult{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return WorkbookSearchResult{}, ErrNotFound
	}
	sheets := make([]Sheet, 0, len(state.sheets))
	for _, sheet := range state.sheets {
		sheets = append(sheets, sheet)
	}
	sort.Slice(sheets, func(i, j int) bool { return sheets[i].Position < sheets[j].Position })
	matches := make([]WorkbookSearchMatch, 0, normalized.Limit+1)
	matcher, err := newSearchMatcher(normalized)
	if err != nil {
		return WorkbookSearchResult{}, err
	}
	skipped := 0
	for _, sheet := range sheets {
		if normalized.SheetID != "" && sheet.ID != normalized.SheetID {
			continue
		}
		cells := make([]Cell, 0, len(state.cells[sheet.ID]))
		for _, cell := range state.cells[sheet.ID] {
			cells = append(cells, cell)
		}
		sort.Slice(cells, func(i, j int) bool {
			if cells[i].Row == cells[j].Row {
				return cells[i].Column < cells[j].Column
			}
			return cells[i].Row < cells[j].Row
		})
		for _, cell := range cells {
			fields := matcher.fields(cell)
			if len(fields) == 0 {
				continue
			}
			if skipped < normalized.Offset {
				skipped++
				continue
			}
			matches = append(matches, newSearchMatch(sheet, cell, fields))
			if len(matches) > normalized.Limit {
				break
			}
		}
		if len(matches) > normalized.Limit {
			break
		}
	}
	result := WorkbookSearchResult{WorkbookID: workbookID, WorkbookVersion: state.workbook.Version, Query: normalized.Query, Items: matches}
	if len(result.Items) > normalized.Limit {
		result.Items = result.Items[:normalized.Limit]
		next := normalized.Offset + normalized.Limit
		result.NextOffset = &next
	}
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
	wb := Workbook{ID: identity.New(), WorkspaceID: input.WorkspaceID, Title: title, OwnerID: input.OwnerID, Version: 1, CreatedAt: now, UpdatedAt: now, LinkAccess: LinkAccessRestricted, LinkRole: RoleViewer, ViewerCanCopy: true}
	sheet := Sheet{ID: identity.New(), WorkbookID: wb.ID, Name: "Sheet1", Position: 0, Layout: defaultSheetLayout(), CreatedAt: now}
	state := &workbookState{workbook: wb, sheets: map[string]Sheet{sheet.ID: sheet}, cells: map[string]map[cellKey]Cell{sheet.ID: {}}, idempotent: make(map[string]MutationResult)}
	r.workbooks[wb.ID] = state
	r.sheetToWB[sheet.ID] = wb.ID
	return r.workbookWithSheets(state), nil
}

func (r *MemoryRepository) ListWorkbooks(_ context.Context, workspaceID string, principal AccessPrincipal) ([]Workbook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	closure := r.departmentClosureLocked(principal)
	favorites := r.favorites[strings.ToLower(strings.TrimSpace(principal.UserID))]
	items := make([]Workbook, 0, len(r.workbooks))
	for id, state := range r.workbooks {
		if workspaceID != "" && state.workbook.WorkspaceID != workspaceID {
			continue
		}
		sharing, err := r.sharingLocked(id)
		if err != nil {
			return nil, err
		}
		access := resolveAccess(id, principal, sharing, closure)
		if access.Role == RoleNone {
			continue
		}
		item := r.workbookWithSheets(state)
		item.Favorite = favorites[id]
		item.AccessRole, item.AccessSource, item.SharedCount = access.Role, access.Source, len(sharing.Shares)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
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

func (r *MemoryRepository) DuplicateWorkbook(_ context.Context, id string, input DuplicateWorkbookInput) (Workbook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	source, ok := r.workbooks[id]
	if !ok {
		return Workbook{}, ErrNotFound
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = source.workbook.Title + " 복사본"
	}
	now := r.now()
	ownerID := strings.TrimSpace(input.OwnerID)
	if ownerID == "" {
		ownerID = source.workbook.OwnerID
	}
	copyWorkbook := Workbook{ID: identity.New(), WorkspaceID: source.workbook.WorkspaceID, Title: title, OwnerID: ownerID, Version: 1, CreatedAt: now, UpdatedAt: now}
	copyState := &workbookState{workbook: copyWorkbook, sheets: make(map[string]Sheet, len(source.sheets)), cells: make(map[string]map[cellKey]Cell, len(source.cells)), idempotent: make(map[string]MutationResult)}
	sheetIDs := make(map[string]string, len(source.sheets))
	for _, sourceSheet := range source.sheets {
		copySheet := cloneSheet(sourceSheet)
		copySheet.ID = identity.New()
		copySheet.WorkbookID = copyWorkbook.ID
		copySheet.CreatedAt = now
		copyState.sheets[copySheet.ID] = copySheet
		sheetIDs[sourceSheet.ID] = copySheet.ID
		copyState.cells[copySheet.ID] = make(map[cellKey]Cell, len(source.cells[sourceSheet.ID]))
		for coordinate, sourceCell := range source.cells[sourceSheet.ID] {
			copyCell := cloneCell(sourceCell)
			copyCell.SheetID = copySheet.ID
			copyCell.UpdatedAt = now
			copyState.cells[copySheet.ID][coordinate] = copyCell
		}
		r.sheetToWB[copySheet.ID] = copyWorkbook.ID
	}
	for _, sourceRule := range r.validations {
		copySheetID, found := sheetIDs[sourceRule.SheetID]
		if !found {
			continue
		}
		copyRule := cloneDataValidation(sourceRule)
		copyRule.ID = identity.New()
		copyRule.WorkbookID = copyWorkbook.ID
		copyRule.WorkbookVersion = 1
		copyRule.SheetID = copySheetID
		copyRule.CreateKey = "copy:" + copyRule.ID
		copyRule.Revision = 1
		copyRule.CreatedBy = ownerID
		copyRule.UpdatedBy = ownerID
		copyRule.CreatedAt = now
		copyRule.UpdatedAt = now
		r.validations[copyRule.ID] = copyRule
	}
	for _, sourceRule := range r.conditionalFormats {
		copySheetID, found := sheetIDs[sourceRule.SheetID]
		if !found {
			continue
		}
		copyRule := cloneConditionalFormat(sourceRule)
		copyRule.ID = identity.New()
		copyRule.WorkbookID = copyWorkbook.ID
		copyRule.WorkbookVersion = 1
		copyRule.SheetID = copySheetID
		copyRule.CreateKey = "copy:" + copyRule.ID
		copyRule.Revision = 1
		copyRule.CreatedBy, copyRule.UpdatedBy = ownerID, ownerID
		copyRule.CreatedAt, copyRule.UpdatedAt = now, now
		r.conditionalFormats[copyRule.ID] = copyRule
	}
	for _, sourceRange := range r.namedRanges {
		if sourceRange.WorkbookID != source.workbook.ID {
			continue
		}
		copyRange := cloneNamedRange(sourceRange)
		copyRange.ID = identity.New()
		copyRange.WorkbookID = copyWorkbook.ID
		copyRange.WorkbookVersion = 1
		copyRange.SheetID = sheetIDs[sourceRange.SheetID]
		copyRange.CreateKey = "copy:" + copyRange.ID
		copyRange.Revision = 1
		copyRange.CreatedBy, copyRange.UpdatedBy = ownerID, ownerID
		copyRange.CreatedAt, copyRange.UpdatedAt = now, now
		r.namedRanges[copyRange.ID] = copyRange
	}
	for _, sourceChart := range r.charts {
		if sourceChart.WorkbookID != source.workbook.ID {
			continue
		}
		copyChart := cloneChart(sourceChart)
		copyChart.ID = identity.New()
		copyChart.WorkbookID = copyWorkbook.ID
		copyChart.WorkbookVersion = 1
		copyChart.SheetID = sheetIDs[sourceChart.SheetID]
		copyChart.SourceSheetID = sheetIDs[sourceChart.SourceSheetID]
		copyChart.CreateKey = "copy:" + copyChart.ID
		copyChart.Revision = 1
		copyChart.CreatedBy, copyChart.UpdatedBy = ownerID, ownerID
		copyChart.CreatedAt, copyChart.UpdatedAt = now, now
		r.charts[copyChart.ID] = copyChart
	}
	for _, sourcePivot := range r.pivots {
		if sourcePivot.WorkbookID != source.workbook.ID {
			continue
		}
		copyPivot := clonePivot(sourcePivot)
		copyPivot.ID = identity.New()
		copyPivot.WorkbookID = copyWorkbook.ID
		copyPivot.WorkbookVersion = 1
		copyPivot.SheetID = sheetIDs[sourcePivot.SheetID]
		if sourcePivot.SourceRange == "#REF!" {
			copyPivot.SourceSheetID = ""
		} else {
			copyPivot.SourceSheetID = sheetIDs[sourcePivot.SourceSheetID]
		}
		copyPivot.CreateKey = "copy:" + copyPivot.ID
		copyPivot.Revision, copyPivot.SourceVersion, copyPivot.LastRefreshedAt = 1, 0, nil
		copyPivot.CreatedBy, copyPivot.UpdatedBy = ownerID, ownerID
		copyPivot.CreatedAt, copyPivot.UpdatedAt = now, now
		r.pivots[copyPivot.ID] = copyPivot
	}
	r.workbooks[copyWorkbook.ID] = copyState
	return r.workbookWithSheets(copyState), nil
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

func (r *MemoryRepository) DeleteWorkbook(_ context.Context, id, deletedBy string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[id]
	if !ok {
		return ErrNotFound
	}
	deleted := r.now()
	state.deletedAt, state.deletedBy = &deleted, deletedBy
	r.trash[id] = state
	for sheetID := range state.sheets {
		for filterID, view := range r.filters {
			if view.SheetID == sheetID {
				delete(r.filters, filterID)
			}
		}
		for validationID, rule := range r.validations {
			if rule.SheetID == sheetID {
				delete(r.validations, validationID)
			}
		}
		for formatID, rule := range r.conditionalFormats {
			if rule.SheetID == sheetID {
				delete(r.conditionalFormats, formatID)
			}
		}
		delete(r.sheetToWB, sheetID)
	}
	for rangeID, item := range r.namedRanges {
		if item.WorkbookID == id {
			delete(r.namedRanges, rangeID)
		}
	}
	for chartID, item := range r.charts {
		if item.WorkbookID == id {
			delete(r.charts, chartID)
		}
	}
	for pivotID, item := range r.pivots {
		if item.WorkbookID == id {
			delete(r.pivots, pivotID)
			delete(r.pivotCache, pivotID)
		}
	}
	for threadID, thread := range r.comments {
		if thread.WorkbookID == id {
			delete(r.comments, threadID)
		}
	}
	for notificationID, notification := range r.notifications {
		if notification.WorkbookID == id {
			delete(r.notifications, notificationID)
		}
	}
	for conflictID, conflict := range r.conflicts {
		if conflict.WorkbookID == id {
			delete(r.conflicts, conflictID)
		}
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
	sheet := Sheet{ID: identity.New(), WorkbookID: workbookID, Name: name, Position: len(state.sheets), Color: input.Color, Layout: defaultSheetLayout(), CreatedAt: r.now()}
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
	duplicated := Sheet{ID: identity.New(), WorkbookID: source.WorkbookID, Name: name, Position: source.Position + 1, Color: source.Color, Hidden: source.Hidden, Layout: cloneSheetLayout(source.Layout), CreatedAt: now}
	state.sheets[duplicated.ID] = duplicated
	state.cells[duplicated.ID] = make(map[cellKey]Cell, len(state.cells[source.ID]))
	for key, sourceCell := range state.cells[source.ID] {
		cell := cloneCell(sourceCell)
		cell.SheetID = duplicated.ID
		cell.UpdatedAt = now
		state.cells[duplicated.ID][key] = cell
	}
	for _, sourceRule := range r.validations {
		if sourceRule.SheetID != source.ID {
			continue
		}
		copyRule := cloneDataValidation(sourceRule)
		copyRule.ID = identity.New()
		copyRule.WorkbookID = source.WorkbookID
		copyRule.SheetID = duplicated.ID
		copyRule.CreateKey = "copy:" + copyRule.ID
		copyRule.Revision = 1
		copyRule.CreatedAt = now
		copyRule.UpdatedAt = now
		r.validations[copyRule.ID] = copyRule
	}
	for _, sourceRule := range r.conditionalFormats {
		if sourceRule.SheetID != source.ID {
			continue
		}
		copyRule := cloneConditionalFormat(sourceRule)
		copyRule.ID = identity.New()
		copyRule.SheetID = duplicated.ID
		copyRule.CreateKey = "copy:" + copyRule.ID
		copyRule.Revision = 1
		copyRule.CreatedAt, copyRule.UpdatedAt = now, now
		r.conditionalFormats[copyRule.ID] = copyRule
	}
	for _, sourceChart := range r.charts {
		if sourceChart.SheetID != source.ID {
			continue
		}
		copyChart := cloneChart(sourceChart)
		copyChart.ID = identity.New()
		copyChart.SheetID = duplicated.ID
		if copyChart.SourceSheetID == source.ID {
			copyChart.SourceSheetID = duplicated.ID
		}
		copyChart.CreateKey = "copy:" + copyChart.ID
		copyChart.Revision = 1
		copyChart.CreatedAt, copyChart.UpdatedAt = now, now
		r.charts[copyChart.ID] = copyChart
	}
	for _, sourcePivot := range r.pivots {
		if sourcePivot.SheetID != source.ID {
			continue
		}
		copyPivot := clonePivot(sourcePivot)
		copyPivot.ID = identity.New()
		copyPivot.SheetID = duplicated.ID
		if copyPivot.SourceSheetID == source.ID {
			copyPivot.SourceSheetID = duplicated.ID
		}
		copyPivot.CreateKey = "copy:" + copyPivot.ID
		copyPivot.Revision, copyPivot.SourceVersion, copyPivot.LastRefreshedAt = 1, 0, nil
		copyPivot.CreatedAt, copyPivot.UpdatedAt = now, now
		r.pivots[copyPivot.ID] = copyPivot
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
		if *input.Hidden && !next.Hidden && visibleSheetCount(state.sheets, sheetID) == 0 {
			return Sheet{}, fmt.Errorf("%w: at least one sheet must stay visible", ErrInvalid)
		}
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
	if reflect.DeepEqual(next, sheet) {
		return sheet, nil
	}
	if next.Name != sheet.Name {
		for formulaSheetID, cells := range state.cells {
			for key, cell := range cells {
				renamed := formula.RenameSheetReferences(cell.Formula, sheet.Name, next.Name)
				if renamed != cell.Formula {
					cell.Formula = renamed
					state.cells[formulaSheetID][key] = cell
				}
			}
		}
	}
	state.sheets[sheetID] = next
	r.bump(state)
	return next, nil
}

func (r *MemoryRepository) DeleteSheet(_ context.Context, sheetID, actorID string) (SheetDeletion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, deleted, err := r.sheetState(sheetID)
	if err != nil {
		return SheetDeletion{}, err
	}
	if len(state.sheets) == 1 {
		return SheetDeletion{}, fmt.Errorf("%w: a workbook must contain at least one sheet", ErrInvalid)
	}
	// Deleting a sheet throws away every cell in it and there is no cell-level
	// undo for it, so the snapshot taken here is the only way back.
	backup := Version{ID: identity.New(), WorkbookID: state.workbook.ID, WorkbookVersion: state.workbook.Version, Name: sheetDeletionBackupName(deleted.Name), ActorID: actorID, CreatedAt: r.now()}
	state.versions = append(state.versions, snapshot{version: backup, workbook: state.workbook, sheets: cloneSheets(state.sheets), cells: cloneAllCells(state.cells), filters: cloneFiltersForSheets(r.filters, state.sheets), validations: cloneValidationsForSheets(r.validations, state.sheets), conditionalFormats: cloneConditionalFormatsForSheets(r.conditionalFormats, state.sheets), namedRanges: cloneNamedRangesForWorkbook(r.namedRanges, state.workbook.ID), charts: cloneChartsForWorkbook(r.charts, state.workbook.ID), pivots: clonePivotsForWorkbook(r.pivots, state.workbook.ID)})
	nextSheets, nextCells := cloneSheets(state.sheets), cloneAllCells(state.cells)
	delete(nextSheets, sheetID)
	delete(nextCells, sheetID)
	removedRanges := make(map[string]NamedRange)
	for id, item := range r.namedRanges {
		if item.WorkbookID == state.workbook.ID && item.SheetID == sheetID {
			removedRanges[id] = item
			delete(r.namedRanges, id)
		}
	}
	for id, sheet := range nextSheets {
		if sheet.Position > deleted.Position {
			sheet.Position--
			nextSheets[id] = sheet
		}
	}
	var currentSheetID string
	for id := range nextSheets {
		currentSheetID = id
		break
	}
	expanded, _, _, err := recalculateCellInputs(nextSheets, nextCells, currentSheetID, nil, true, formulaNamedRanges(r.namedRangesForWorkbookLocked(state.workbook.ID)), r.importsForLocked(state.workbook.ID, nextCells, nil))
	if err != nil {
		for id, item := range removedRanges {
			r.namedRanges[id] = item
		}
		state.versions = state.versions[:len(state.versions)-1]
		return SheetDeletion{}, err
	}
	now := r.now()
	for _, input := range expanded {
		key := cellKey{input.Row, input.Column}
		cell := Cell{SheetID: input.SheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), Note: input.Note, SpillSource: input.SpillSource, UpdatedAt: now}
		if isEmptyCell(cell) {
			delete(nextCells[input.SheetID], key)
		} else {
			nextCells[input.SheetID][key] = cell
		}
	}
	state.sheets, state.cells = nextSheets, nextCells
	for filterID, view := range r.filters {
		if view.SheetID == sheetID {
			delete(r.filters, filterID)
		}
	}
	for validationID, rule := range r.validations {
		if rule.SheetID == sheetID {
			delete(r.validations, validationID)
		}
	}
	for formatID, rule := range r.conditionalFormats {
		if rule.SheetID == sheetID {
			delete(r.conditionalFormats, formatID)
		}
	}
	for threadID, thread := range r.comments {
		if thread.SheetID == sheetID {
			delete(r.comments, threadID)
		}
	}
	for notificationID, notification := range r.notifications {
		if notification.SheetID == sheetID {
			delete(r.notifications, notificationID)
		}
	}
	for conflictID, conflict := range r.conflicts {
		if conflict.SheetID == sheetID {
			delete(r.conflicts, conflictID)
		}
	}
	for chartID, chart := range r.charts {
		if chart.SheetID == sheetID {
			delete(r.charts, chartID)
			continue
		}
		if chart.SourceSheetID == sheetID {
			chart.SourceSheetID, chart.SourceRange = "", "#REF!"
			chart.Revision++
			chart.UpdatedAt = now
			r.charts[chartID] = chart
		}
	}
	for pivotID, pivot := range r.pivots {
		if pivot.SheetID == sheetID {
			delete(r.pivots, pivotID)
			delete(r.pivotCache, pivotID)
			continue
		}
		if pivot.SourceSheetID == sheetID {
			pivot.SourceSheetID, pivot.SourceRange = "", "#REF!"
			pivot.Revision++
			pivot.SourceVersion, pivot.LastRefreshedAt = 0, nil
			pivot.UpdatedAt = now
			r.pivots[pivotID] = pivot
			delete(r.pivotCache, pivotID)
		}
	}
	delete(r.sheetToWB, sheetID)
	r.bump(state)
	return SheetDeletion{WorkbookID: state.workbook.ID, SheetName: deleted.Name, BackupVersionID: backup.ID, ServerVersion: state.workbook.Version}, nil
}

// sheetDeletionBackupName reads in the version list the way the structural
// backups do, so one glance says what was thrown away.
func sheetDeletionBackupName(name string) string {
	return "시트 " + name + " 삭제 전 자동 백업"
}

func (r *MemoryRepository) ApplyCells(_ context.Context, mutation CellMutation) (MutationResult, error) {
	if strings.TrimSpace(mutation.IdempotencyKey) == "" {
		return MutationResult{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	// A sort rewrites a whole range at once and has its own, higher ceiling.
	limit := MaxPasteCells
	if mutation.OperationType == "range.sort" {
		limit = MaxSortCells
	}
	if len(mutation.Cells) == 0 || len(mutation.Cells) > limit {
		return MutationResult{}, fmt.Errorf("%w: cells must contain 1 to %d entries", ErrInvalid, limit)
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
	if !formatMutation {
		for _, input := range mutation.Cells {
			if err := ValidateCellStyle(input); err != nil {
				return MutationResult{}, err
			}
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
	if mutation.RequireExactVersion && mutation.BaseVersion != state.workbook.Version {
		return MutationResult{}, ErrVersionConflict
	}
	// Somebody may have inserted or deleted rows since this write was composed,
	// which moves every address after them. Rebase before touching anything.
	rebasedCells, droppedCells, movedCells := rebaseCellInputs(mutation.Cells, memoryStructuralChangesSince(state.operations, mutation.SheetID, mutation.BaseVersion))
	mutation.Cells = rebasedCells
	if len(mutation.Cells) == 0 {
		return MutationResult{OperationID: identity.New(), WorkbookID: state.workbook.ID, SheetID: mutation.SheetID, BaseVersion: mutation.BaseVersion, ServerVersion: state.workbook.Version,
			RecalculatedCells: []CellCoordinate{}, FormulaErrors: []CellFormulaError{}, ValidationWarnings: []ValidationViolation{}, Conflicts: []CellConflict{},
			DroppedCells: droppedCells, CreatedAt: r.now()}, nil
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
				conflicts = append(conflicts, CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: changedVersion, BaseCell: conflictSnapshotFromCell(expected), ConflictingCell: conflictSnapshotFromCell(current), SubmittedCell: conflictSnapshotFromInput(input), PreviousValue: cloneJSON(current.Value), SubmittedValue: cloneJSON(input.Value)})
				continue
			}
			// A write that claims no base version is saying it does not know
			// what it is replacing. There is nothing to call a conflict
			// against, and treating every past operation as newer both
			// invents conflicts and costs a scan of the whole history.
		} else if mutation.BaseVersion >= 1 && mutation.BaseVersion < state.workbook.Version {
			if changedVersion := latestChange(state.operations, mutation.SheetID, coord, mutation.BaseVersion, mutation.ActorID, mutation.ClientID); changedVersion > 0 {
				baseCell, conflictingCell, conflictingActor := memoryConflictDetails(state.operations, mutation.SheetID, coord, mutation.BaseVersion, mutation.ActorID, mutation.ClientID)
				if emptyConflictSnapshot(conflictingCell) {
					conflictingCell = conflictSnapshotFromCell(current)
				}
				conflicts = append(conflicts, CellConflict{Row: input.Row, Column: input.Column, ChangedAtVersion: changedVersion, ConflictingActorID: conflictingActor, BaseCell: baseCell, ConflictingCell: conflictingCell, SubmittedCell: conflictSnapshotFromInput(input), PreviousValue: cloneJSON(current.Value), SubmittedValue: cloneJSON(input.Value)})
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
		result := MutationResult{WorkbookID: state.workbook.ID, SheetID: mutation.SheetID, BaseVersion: mutation.BaseVersion, ServerVersion: state.workbook.Version, Conflicts: conflicts, CreatedAt: r.now()}
		state.idempotent[key] = result
		return result, nil
	}
	var expanded []CellInput
	var recalculated []CellCoordinate
	var formulaErrors []CellFormulaError
	var validationWarnings []ValidationViolation
	// Protection is checked before anything is applied: a paste that touches a
	// protected block is refused whole rather than applied in part.
	if blocked, _ := CheckProtectedRanges(r.protectionsForSheetLocked(mutation.SheetID), mutation.ActorID, state.workbook.OwnerID, effective); len(blocked) > 0 {
		return MutationResult{}, &ProtectionFailure{Violations: blocked}
	}
	if formatMutation {
		expanded = append([]CellInput(nil), effective...)
	} else {
		expanded, recalculated, formulaErrors, err = recalculateCellInputs(state.sheets, state.cells, mutation.SheetID, effective, false, formulaNamedRanges(r.namedRangesForWorkbookLocked(state.workbook.ID)), r.importsForLocked(state.workbook.ID, state.cells, effective))
		if err != nil {
			return MutationResult{}, err
		}
		validationWarnings, err = ValidateCellInputs(r.dataValidationsForSheetLocked(mutation.SheetID), state.cells[mutation.SheetID], inputsForSheet(expanded, mutation.SheetID), effective)
		if err != nil {
			return MutationResult{}, err
		}
	}
	before := make(map[scopedCellKey]Cell, len(expanded))
	after := make(map[scopedCellKey]Cell, len(expanded))
	now := r.now()
	for _, input := range expanded {
		sheetID := input.SheetID
		if sheetID == "" {
			sheetID = mutation.SheetID
		}
		coord := cellKey{input.Row, input.Column}
		scoped := scopedCellKey{sheetID: sheetID, cellKey: coord}
		current := state.cells[sheetID][coord]
		before[scoped] = cloneCell(current)
		cell := Cell{SheetID: sheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), Note: input.Note, SpillSource: input.SpillSource, UpdatedAt: now}
		if isEmptyCell(cell) {
			delete(state.cells[sheetID], coord)
		} else {
			state.cells[sheetID][coord] = cell
		}
		after[scoped] = cloneCell(cell)
	}
	baseVersion := mutation.BaseVersion
	r.bump(state)
	result := MutationResult{OperationID: identity.New(), WorkbookID: state.workbook.ID, SheetID: mutation.SheetID, BaseVersion: baseVersion, ServerVersion: state.workbook.Version, AppliedCells: len(effective), RecalculatedCells: recalculated, FormulaErrors: formulaErrors, ValidationWarnings: validationWarnings, RebasedCells: movedCells, DroppedCells: droppedCells, CreatedAt: now}
	conflicts = finalizeCellConflicts(conflicts, mutation, result, func(row, column int) (Cell, bool) {
		cell, ok := after[scopedCellKey{sheetID: mutation.SheetID, cellKey: cellKey{row: row, column: column}}]
		return cell, ok
	}, now)
	result.Conflicts = conflicts
	operationType := mutation.OperationType
	if operationType == "" {
		operationType = "cells.batch"
	}
	state.operations = append(state.operations, operation{result: result, before: before, after: after, submitted: submittedCoordinates(effective), actorID: mutation.ActorID, clientID: mutation.ClientID, operationType: operationType, undoOf: mutation.UndoOfOperationID})
	for _, conflict := range conflicts {
		r.conflicts[conflict.ID] = cloneCellConflict(conflict)
	}
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
			if key.sheetID == target.result.SheetID {
				coordinates = append(coordinates, CellCoordinate{Row: key.cellKey.row, Column: key.cellKey.column})
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
		key := scopedCellKey{sheetID: target.result.SheetID, cellKey: cellKey{coordinate.Row, coordinate.Column}}
		cells = append(cells, inputFromCell(coordinate.Row, coordinate.Column, target.before[key]))
		expected[coordinateKey(coordinate.Row, coordinate.Column)] = cloneCell(target.after[key])
	}
	return r.ApplyCells(ctx, CellMutation{SheetID: target.result.SheetID, ActorID: input.ActorID, ClientID: input.ClientID, BaseVersion: target.result.ServerVersion, IdempotencyKey: input.IdempotencyKey, Cells: cells, Expected: expected, OperationType: "operation.undo", UndoOfOperationID: input.OperationID})
}

func (r *MemoryRepository) ListCellConflicts(_ context.Context, workbookID string, includeResolved bool) ([]CellConflict, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, exists := r.workbooks[workbookID]
	if !exists {
		return nil, ErrNotFound
	}
	result := make([]CellConflict, 0)
	for _, stored := range r.conflicts {
		if stored.WorkbookID != workbookID || (!includeResolved && stored.Status != ConflictStatusOpen) {
			continue
		}
		conflict := cloneCellConflict(stored)
		current := state.cells[conflict.SheetID][cellKey{row: conflict.Row, column: conflict.Column}]
		conflict.CurrentCell = conflictSnapshotFromCell(current)
		result = append(result, conflict)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Status != result[j].Status {
			return result[i].Status == ConflictStatusOpen
		}
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (r *MemoryRepository) GetCellConflict(_ context.Context, conflictID string) (CellConflict, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stored, exists := r.conflicts[conflictID]
	if !exists {
		return CellConflict{}, ErrNotFound
	}
	conflict := cloneCellConflict(stored)
	if state := r.workbooks[conflict.WorkbookID]; state != nil {
		current := state.cells[conflict.SheetID][cellKey{row: conflict.Row, column: conflict.Column}]
		conflict.CurrentCell = conflictSnapshotFromCell(current)
	}
	return conflict, nil
}

func (r *MemoryRepository) ResolveCellConflict(ctx context.Context, conflictID string, input ResolveCellConflictInput) (CellConflictResolutionResult, error) {
	if err := validateConflictResolution(input); err != nil {
		return CellConflictResolutionResult{}, err
	}
	r.resolveMu.Lock()
	defer r.resolveMu.Unlock()

	conflict, err := r.GetCellConflict(ctx, conflictID)
	if err != nil {
		return CellConflictResolutionResult{}, err
	}
	if conflict.Status == ConflictStatusResolved {
		if conflict.Resolution != input.Resolution {
			return CellConflictResolutionResult{}, ErrRevision
		}
		return CellConflictResolutionResult{Conflict: conflict, Operation: MutationResult{OperationID: conflict.ResolutionOperationID, WorkbookID: conflict.WorkbookID, SheetID: conflict.SheetID, ServerVersion: conflict.ResolutionServerVersion, Duplicate: true}}, nil
	}
	if conflict.Revision != input.ExpectedRevision {
		return CellConflictResolutionResult{}, ErrRevision
	}

	r.mu.RLock()
	state := r.workbooks[conflict.WorkbookID]
	if state == nil {
		r.mu.RUnlock()
		return CellConflictResolutionResult{}, ErrNotFound
	}
	baseVersion := state.workbook.Version
	current := cloneCell(state.cells[conflict.SheetID][cellKey{row: conflict.Row, column: conflict.Column}])
	r.mu.RUnlock()

	target := conflictSnapshotFromCell(current)
	if input.Resolution == ConflictRestorePrevious {
		if !cellsEqual(current, cellFromConflictSnapshot(conflict.SheetID, conflict.Row, conflict.Column, conflict.AppliedCell)) {
			return CellConflictResolutionResult{}, ErrVersionConflict
		}
		target = conflict.ConflictingCell
	}
	result, err := r.ApplyCells(ctx, CellMutation{
		SheetID: conflict.SheetID, ActorID: input.ActorID, ClientID: input.ClientID,
		BaseVersion: baseVersion, IdempotencyKey: input.IdempotencyKey,
		Cells:         []CellInput{inputFromConflictSnapshot(conflict.Row, conflict.Column, target)},
		Expected:      map[string]Cell{coordinateKey(conflict.Row, conflict.Column): current},
		OperationType: "conflict.resolve." + input.Resolution,
	})
	if err != nil {
		return CellConflictResolutionResult{}, err
	}
	if result.AppliedCells != 1 {
		return CellConflictResolutionResult{}, ErrVersionConflict
	}

	r.mu.Lock()
	stored := r.conflicts[conflictID]
	if stored.Status != ConflictStatusOpen || stored.Revision != input.ExpectedRevision {
		r.mu.Unlock()
		return CellConflictResolutionResult{}, ErrRevision
	}
	now := r.now()
	stored.Status = ConflictStatusResolved
	stored.Resolution = input.Resolution
	stored.Revision++
	stored.ResolvedBy = input.ActorID
	stored.ResolutionOperationID = result.OperationID
	stored.ResolutionServerVersion = result.ServerVersion
	stored.ResolvedAt = &now
	stored.UpdatedAt = now
	current = cloneCell(r.workbooks[stored.WorkbookID].cells[stored.SheetID][cellKey{row: stored.Row, column: stored.Column}])
	stored.CurrentCell = conflictSnapshotFromCell(current)
	r.conflicts[conflictID] = stored
	r.mu.Unlock()
	return CellConflictResolutionResult{Conflict: cloneCellConflict(stored), Operation: result}, nil
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
	state.versions = append(state.versions, snapshot{version: version, workbook: state.workbook, sheets: cloneSheets(state.sheets), cells: cloneAllCells(state.cells), filters: cloneFiltersForSheets(r.filters, state.sheets), validations: cloneValidationsForSheets(r.validations, state.sheets), conditionalFormats: cloneConditionalFormatsForSheets(r.conditionalFormats, state.sheets), namedRanges: cloneNamedRangesForWorkbook(r.namedRanges, state.workbook.ID), charts: cloneChartsForWorkbook(r.charts, state.workbook.ID), pivots: clonePivotsForWorkbook(r.pivots, state.workbook.ID)})
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
			state.versions = append(state.versions, snapshot{version: backupVersion, workbook: state.workbook, sheets: cloneSheets(state.sheets), cells: cloneAllCells(state.cells), filters: cloneFiltersForSheets(r.filters, state.sheets), validations: cloneValidationsForSheets(r.validations, state.sheets), conditionalFormats: cloneConditionalFormatsForSheets(r.conditionalFormats, state.sheets), namedRanges: cloneNamedRangesForWorkbook(r.namedRanges, state.workbook.ID), charts: cloneChartsForWorkbook(r.charts, state.workbook.ID), pivots: clonePivotsForWorkbook(r.pivots, state.workbook.ID)})
			for sheetID := range state.sheets {
				delete(r.sheetToWB, sheetID)
			}
			for filterID, view := range r.filters {
				if _, found := state.sheets[view.SheetID]; found {
					delete(r.filters, filterID)
				}
			}
			state.workbook.Title = snap.workbook.Title
			state.workbook.Favorite = snap.workbook.Favorite
			state.sheets = cloneSheets(snap.sheets)
			state.cells = cloneAllCells(snap.cells)
			for filterID, view := range snap.filters {
				r.filters[filterID] = cloneFilterView(view)
			}
			for validationID, rule := range r.validations {
				if rule.WorkbookID == state.workbook.ID {
					delete(r.validations, validationID)
				}
			}
			for validationID, rule := range snap.validations {
				r.validations[validationID] = cloneDataValidation(rule)
			}
			for formatID, rule := range r.conditionalFormats {
				if rule.WorkbookID == state.workbook.ID {
					delete(r.conditionalFormats, formatID)
				}
			}
			for formatID, rule := range snap.conditionalFormats {
				copyRule := cloneConditionalFormat(rule)
				copyRule.WorkbookVersion = base + 1
				copyRule.UpdatedBy, copyRule.UpdatedAt = actorID, r.now()
				r.conditionalFormats[formatID] = copyRule
			}
			for rangeID, item := range r.namedRanges {
				if item.WorkbookID == state.workbook.ID {
					delete(r.namedRanges, rangeID)
				}
			}
			for rangeID, item := range snap.namedRanges {
				r.namedRanges[rangeID] = cloneNamedRange(item)
			}
			for chartID, item := range r.charts {
				if item.WorkbookID == state.workbook.ID {
					delete(r.charts, chartID)
				}
			}
			for chartID, item := range snap.charts {
				r.charts[chartID] = cloneChart(item)
			}
			for pivotID, item := range r.pivots {
				if item.WorkbookID == state.workbook.ID {
					delete(r.pivots, pivotID)
					delete(r.pivotCache, pivotID)
				}
			}
			for pivotID, item := range snap.pivots {
				copyPivot := clonePivot(item)
				copyPivot.SourceVersion, copyPivot.LastRefreshedAt = 0, nil
				r.pivots[pivotID] = copyPivot
			}
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
		result[id] = cloneSheet(sheet)
	}
	return result
}

func cloneChartsForWorkbook(source map[string]Chart, workbookID string) map[string]Chart {
	result := make(map[string]Chart)
	for id, item := range source {
		if item.WorkbookID == workbookID {
			result[id] = cloneChart(item)
		}
	}
	return result
}

func cloneValidationsForSheets(source map[string]DataValidation, sheets map[string]Sheet) map[string]DataValidation {
	result := make(map[string]DataValidation)
	for id, rule := range source {
		if _, found := sheets[rule.SheetID]; found {
			result[id] = cloneDataValidation(rule)
		}
	}
	return result
}

func cloneFiltersForSheets(source map[string]FilterView, sheets map[string]Sheet) map[string]FilterView {
	result := make(map[string]FilterView)
	for id, view := range source {
		if _, found := sheets[view.SheetID]; found {
			result[id] = cloneFilterView(view)
		}
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
		wb.Sheets = append(wb.Sheets, cloneSheet(sheet))
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
		if op.result.ServerVersion <= afterVersion {
			continue
		}
		if clientID != "" && op.actorID == actorID && op.clientID == clientID {
			continue
		}
		if op.structural {
			version = op.result.ServerVersion
			continue
		}
		if _, ok := op.after[scopedCellKey{sheetID: sheetID, cellKey: key}]; ok {
			version = op.result.ServerVersion
		}
	}
	return version
}

func memoryConflictDetails(operations []operation, sheetID string, key cellKey, afterVersion int64, actorID, clientID string) (CellConflictSnapshot, CellConflictSnapshot, string) {
	var base CellConflictSnapshot
	var conflicting CellConflictSnapshot
	var conflictingActor string
	found := false
	coordinate := scopedCellKey{sheetID: sheetID, cellKey: key}
	for _, op := range operations {
		if op.result.ServerVersion <= afterVersion || (clientID != "" && op.actorID == actorID && op.clientID == clientID) {
			continue
		}
		if op.structural {
			conflictingActor = op.actorID
			continue
		}
		changed, ok := op.after[coordinate]
		if !ok {
			continue
		}
		if !found {
			base = conflictSnapshotFromCell(op.before[coordinate])
			found = true
		}
		conflicting = conflictSnapshotFromCell(changed)
		conflictingActor = op.actorID
	}
	return base, conflicting, conflictingActor
}

func isEmptyCell(cell Cell) bool {
	return len(bytes.TrimSpace(cell.Value)) == 0 && cell.Formula == "" && len(bytes.TrimSpace(cell.Style)) == 0 && cell.SpillSource == "" && cell.Note == ""
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

// memoryStructuralChangesSince lists the row and column edits applied to a
// sheet after a version, oldest first, so a write composed against that
// version can be moved onto the sheet as it is now.
func memoryStructuralChangesSince(operations []operation, sheetID string, baseVersion int64) []formula.StructuralChange {
	if baseVersion < 1 {
		return nil
	}
	changes := make([]formula.StructuralChange, 0)
	for _, item := range operations {
		if !item.structural || item.result.SheetID != sheetID || item.result.ServerVersion <= baseVersion {
			continue
		}
		if change, ok := structuralChangeFromResult(item.result); ok {
			changes = append(changes, change)
		}
	}
	return changes
}
