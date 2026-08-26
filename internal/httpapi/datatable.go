package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"kanpic/internal/formula"
	"kanpic/internal/workbook"
)

// MaxDataTableCells 는 데이터 표가 읽을 워크북의 크기다. 표의 칸마다 워크북을
// 한 번씩 다시 셈하므로, 목표값 찾기보다 더 조심해야 한다.
const MaxDataTableCells = 50_000

// MaxDataTableWork 는 한 번의 요청이 시킬 수 있는 셈의 양이다.
//
// 워크북 칸 수와 표 칸 수를 따로 묶으면 곱이 얼마가 될지는 아무도 보지 않는다.
// 2,000칸짜리 워크북에 400칸 표가 2.9초였으므로 한 번의 셈은 4마이크로초쯤
// 이다. 여기서 정한 값은 30초 어름이고, 서버의 쓰기 제한(130초)보다 넉넉히
// 아래다 — 제한에 걸려 끊기면 사람은 무엇이 잘못됐는지 알 길이 없다.
const MaxDataTableWork = 8_000_000

type dataTableRequest struct {
	Target       string    `json:"target"`
	RowInput     string    `json:"row_input,omitempty"`
	ColumnInput  string    `json:"column_input,omitempty"`
	RowValues    []float64 `json:"row_values,omitempty"`
	ColumnValues []float64 `json:"column_values,omitempty"`
}

// dataTable 은 가정을 하나씩 넣어 보며 결과를 표로 만든다. 목표값 찾기와
// 반대 방향의 물음이다 — "얼마여야 이 값이 나오나" 가 아니라 "이 값이
// 이만큼일 때 결과가 어떻게 되나" 다.
//
// 아무것도 쓰지 않는다. 답은 제안이고, 시트에 넣을지는 사람이 정한다.
func (s *Server) dataTable(w http.ResponseWriter, r *http.Request) {
	var request dataTableRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	sheetID := r.PathValue("sheetId")
	state, evaluator, err := s.workbookFormulaState(r.Context(), sheetID, MaxDataTableCells, "데이터 표")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	address := func(value string) string {
		value = strings.TrimSpace(value)
		if value == "" {
			return ""
		}
		return formula.CellKey(sheetID, value)
	}
	// 표 한 칸마다 워크북을 통째로 다시 셈한다. 얼마나 걸릴지는 두 수의
	// 곱으로 정해지므로, 셈하기 전에 그 곱을 본다.
	tableCells := max(len(request.ColumnValues), 1) * max(len(request.RowValues), 1)
	if tableCells*len(state) > MaxDataTableWork {
		s.writeError(w, r, fmt.Errorf("%w: 이 워크북에는 표가 너무 큽니다. 가정을 줄이거나 더 작은 시트에서 해 보세요", workbook.ErrInvalid))
		return
	}
	result, err := evaluator.DataTable(state, formula.DataTableInput{
		Target:       formula.CellKey(sheetID, strings.TrimSpace(request.Target)),
		RowInput:     address(request.RowInput),
		ColumnInput:  address(request.ColumnInput),
		RowValues:    request.RowValues,
		ColumnValues: request.ColumnValues,
	})
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %s", workbook.ErrInvalid, err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target":       strings.ToUpper(strings.TrimSpace(request.Target)),
		"row_input":    strings.ToUpper(strings.TrimSpace(request.RowInput)),
		"column_input": strings.ToUpper(strings.TrimSpace(request.ColumnInput)),
		"result":       result,
	})
}
