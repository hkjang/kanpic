package workbook

import (
	"sort"
	"strings"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

// memoryImportReader resolves IMPORTRANGE against the in-memory store. Every
// method assumes the repository lock is already held, because imports are
// resolved in the middle of a mutation that owns it.
type memoryImportReader struct{ repository *MemoryRepository }

func (m memoryImportReader) importOwner(workbookID string) (string, error) {
	state, ok := m.repository.workbooks[workbookID]
	if !ok {
		return "", ErrNotFound
	}
	return state.workbook.OwnerID, nil
}

func (m memoryImportReader) importReadable(sourceWorkbookID, ownerID string) (bool, error) {
	state, ok := m.repository.workbooks[sourceWorkbookID]
	if !ok || state.deletedAt != nil {
		return false, ErrNotFound
	}
	sharing, err := m.repository.sharingLocked(sourceWorkbookID)
	if err != nil {
		return false, err
	}
	principal := AccessPrincipal{UserID: ownerID, Authenticated: ownerID != ""}
	access := resolveAccess(sourceWorkbookID, principal, sharing, m.repository.departmentClosureLocked(principal))
	return access.CanRead, nil
}

func (m memoryImportReader) importSheet(sourceWorkbookID, sheetName string) (string, error) {
	state, ok := m.repository.workbooks[sourceWorkbookID]
	if !ok {
		return "", ErrNotFound
	}
	sheets := make([]Sheet, 0, len(state.sheets))
	for _, sheet := range state.sheets {
		sheets = append(sheets, sheet)
	}
	sort.Slice(sheets, func(i, j int) bool { return sheets[i].Position < sheets[j].Position })
	for _, sheet := range sheets {
		if sheetName == "" {
			return sheet.ID, nil
		}
		if strings.EqualFold(strings.TrimSpace(sheet.Name), strings.TrimSpace(sheetName)) {
			return sheet.ID, nil
		}
	}
	return "", ErrNotFound
}

func (m memoryImportReader) importCells(sheetID string, selected cellrange.Range) ([]Cell, error) {
	state, _, err := m.repository.sheetState(sheetID)
	if err != nil {
		return nil, err
	}
	result := make([]Cell, 0)
	for _, cell := range state.cells[sheetID] {
		if selected.Contains(cell.Row, cell.Column) {
			result = append(result, cell)
		}
	}
	return result, nil
}

// importsForLocked gathers and resolves the workbook's IMPORTRANGE calls.
func (r *MemoryRepository) importsForLocked(workbookID string, cells map[string]map[cellKey]Cell, submitted []CellInput) map[string]formula.ImportedRange {
	return resolveImportRequests(memoryImportReader{repository: r}, workbookID, collectImportRequests(cells, submitted))
}
