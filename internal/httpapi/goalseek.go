package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"kanpic/internal/formula"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// MaxGoalSeekCells bounds what a search will read. Goal seek recalculates up to
// a hundred times, so the cost is a hundred times the workbook; past this it is
// not a question anybody should wait on in a dialog.
const MaxGoalSeekCells = 50_000

type goalSeekRequest struct {
	Target   string  `json:"target"`
	Changing string  `json:"changing"`
	Goal     float64 `json:"goal"`
}

// goalSeek answers what an input would have to be for a formula to reach a
// value. Nothing is written: the answer is a proposal, and the editor asks
// before putting it in the cell.
//
// The whole workbook is loaded rather than the one sheet, because a summary
// formula that reads another sheet is the ordinary case. Recalculating without
// those cells would not fail — it would quietly treat them as empty and return
// a confident wrong number.
func (s *Server) goalSeek(w http.ResponseWriter, r *http.Request) {
	var request goalSeekRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	sheetID := r.PathValue("sheetId")
	ctx := r.Context()
	workbookID, err := s.repository.WorkbookIDForResource(ctx, "sheetId", sheetID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	book, err := s.repository.GetWorkbook(ctx, workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	state := map[string]formula.CellState{}
	sheetNames := make(map[string]string, len(book.Sheets))
	for _, sheet := range book.Sheets {
		sheetNames[sheet.Name] = sheet.ID
		cells, readErr := s.repository.ReadAllCells(ctx, sheet.ID)
		if readErr != nil {
			s.writeError(w, r, readErr)
			return
		}
		if len(state)+len(cells) > MaxGoalSeekCells {
			s.writeError(w, r, fmt.Errorf("%w: 목표값 찾기는 최대 %d개 셀까지 계산합니다", workbook.ErrInvalid, MaxGoalSeekCells))
			return
		}
		for _, cell := range cells {
			var value any
			if len(cell.Value) > 0 {
				_ = json.Unmarshal(cell.Value, &value)
			}
			state[formula.CellKey(sheet.ID, cellrange.Address(cell.Row, cell.Column))] = formula.CellState{Value: value, Formula: cell.Formula}
		}
	}
	namedRanges, err := s.repository.ListNamedRanges(ctx, workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	formulaNames := make(map[string]formula.NamedRange, len(namedRanges))
	for _, item := range namedRanges {
		formulaNames[item.Name] = formula.NamedRange{SheetID: item.SheetID, Range: item.Range}
	}
	result, err := formula.NewScopedWithNames(sheetID, sheetNames, formulaNames).GoalSeek(state, formula.GoalSeekInput{
		Target:   formula.CellKey(sheetID, strings.TrimSpace(request.Target)),
		Changing: formula.CellKey(sheetID, strings.TrimSpace(request.Changing)),
		Goal:     request.Goal,
	})
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %s", workbook.ErrInvalid, err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target":   strings.ToUpper(strings.TrimSpace(request.Target)),
		"changing": strings.ToUpper(strings.TrimSpace(request.Changing)),
		"goal":     request.Goal, "result": result,
	})
}
