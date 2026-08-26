package httpapi

import (
	"context"
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
// workbookFormulaState 는 워크북을 통째로 읽어 엔진이 쓸 상태와, 이름·표를
// 모두 아는 계산기를 만든다.
//
// 시트 하나가 아니라 워크북 전체를 읽는 까닭은, 다른 시트를 읽는 요약 수식이
// 흔하기 때문이다. 그 칸들을 빼고 다시 셈하면 실패하지 않는다 — 조용히 빈
// 칸으로 보고 자신 있게 틀린 수를 낸다.
//
// 목표값 찾기와 데이터 표가 이것을 함께 쓴다. 두 곳에 나누어 적으면 한쪽만
// 늘어나고, 그때 한쪽에서만 표를 모르게 된다.
func (s *Server) workbookFormulaState(ctx context.Context, sheetID string, maxCells int, label string) (map[string]formula.CellState, *formula.Evaluator, error) {
	workbookID, err := s.repository.WorkbookIDForResource(ctx, "sheetId", sheetID)
	if err != nil {
		return nil, nil, err
	}
	book, err := s.repository.GetWorkbook(ctx, workbookID)
	if err != nil {
		return nil, nil, err
	}
	state := map[string]formula.CellState{}
	sheetNames := make(map[string]string, len(book.Sheets))
	for _, sheet := range book.Sheets {
		sheetNames[sheet.Name] = sheet.ID
		cells, readErr := s.repository.ReadAllCells(ctx, sheet.ID)
		if readErr != nil {
			return nil, nil, readErr
		}
		if len(state)+len(cells) > maxCells {
			return nil, nil, fmt.Errorf("%w: %s는 최대 %d개 셀까지 계산합니다", workbook.ErrInvalid, label, maxCells)
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
		return nil, nil, err
	}
	formulaNames := make(map[string]formula.NamedRange, len(namedRanges))
	for _, item := range namedRanges {
		formulaNames[item.Name] = formula.NamedRange{SheetID: item.SheetID, Range: item.Range}
	}
	namedFunctions, err := s.repository.ListNamedFunctions(ctx, workbookID)
	if err != nil {
		return nil, nil, err
	}
	tableDefinitions, err := s.tablesForFormula(ctx, workbookID)
	if err != nil {
		return nil, nil, err
	}
	evaluator := formula.NewScopedWithNames(sheetID, sheetNames, formulaNames)
	evaluator.SetNamedFunctions(workbook.NamedFunctionDefinitions(namedFunctions))
	evaluator.SetTables(tableDefinitions)
	return state, evaluator, nil
}

func (s *Server) goalSeek(w http.ResponseWriter, r *http.Request) {
	var request goalSeekRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	sheetID := r.PathValue("sheetId")
	state, evaluator, err := s.workbookFormulaState(r.Context(), sheetID, MaxGoalSeekCells, "목표값 찾기")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	result, err := evaluator.GoalSeek(state, formula.GoalSeekInput{
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
