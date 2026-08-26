package formula

import "fmt"

// 데이터 표는 목표값 찾기와 반대 방향의 물음이다. 목표값 찾기가 "얼마여야
// 이 값이 나오나" 라면, 데이터 표는 "이 값이 이만큼일 때 결과가 어떻게
// 되나" 를 한 번에 늘어놓는다.
//
//	이자율이 3%, 3.5%, 4%, 4.5% 일 때 월 상환액은 각각 얼마인가
//	단가가 이만큼이고 수량이 저만큼일 때 이익은 얼마인가
//
// 셈하는 방법은 같다 — 바꿀 칸에 값을 넣고 다시 셈해 찾을 칸을 읽는다.
// 목표값 찾기가 그 일을 되풀이하며 답을 좁혀 가는 것이라면, 데이터 표는
// 사람이 준 값을 그대로 하나씩 넣어 볼 뿐이다.

// DataTableMaxCells 는 한 번에 셈할 수 있는 칸의 수다. 표 하나가 워크북을
// 그만큼 다시 셈하게 하므로, 열 다섯 줄 다섯이면 스물다섯 번이다.
const DataTableMaxCells = 400

type DataTableInput struct {
	// Target 은 읽을 수식 칸이다.
	Target string
	// RowInput 은 가로로 늘어놓은 값을 넣을 칸이다. 비우면 한 방향 표다.
	RowInput string
	// ColumnInput 은 세로로 늘어놓은 값을 넣을 칸이다.
	ColumnInput string
	// RowValues 와 ColumnValues 는 사람이 적어 둔 가정들이다.
	RowValues    []float64
	ColumnValues []float64
}

type DataTableResult struct {
	// Values 는 세로 값마다 한 줄, 가로 값마다 한 칸이다. 한 방향 표는
	// 줄마다 한 칸이다.
	Values [][]*float64 `json:"values"`
	// Failures 는 셈하지 못한 자리를 사람 말로 적어 둔 것이다. 그 자리는
	// Values 에서 비어 있다.
	Failures []DataTableFailure `json:"failures,omitempty"`
}

// DataTableFailure 는 어느 가정에서 셈이 어긋났는지 말한다. 빈 칸만 남기면
// 사람은 왜 비었는지 알 수 없다 — 0 으로 채우는 것보다는 낫지만, 까닭을
// 적어 주는 편이 훨씬 낫다.
type DataTableFailure struct {
	Row    int    `json:"row"`
	Column int    `json:"column"`
	Reason string `json:"reason"`
}

// DataTable 은 가정을 하나씩 넣어 보며 결과를 표로 만든다.
func (e *Evaluator) DataTable(cells map[string]CellState, input DataTableInput) (DataTableResult, error) {
	target := normalizeAddress(input.Target)
	if _, _, valid := SplitCellKey(target); !valid {
		return DataTableResult{}, fmt.Errorf("찾을 셀 주소가 올바르지 않습니다")
	}
	columnInput := normalizeAddress(input.ColumnInput)
	rowInput := normalizeAddress(input.RowInput)
	if columnInput == "" && rowInput == "" {
		return DataTableResult{}, fmt.Errorf("값을 넣을 셀을 하나는 정해야 합니다")
	}
	for _, address := range []string{columnInput, rowInput} {
		if address == "" {
			continue
		}
		if _, _, valid := SplitCellKey(address); !valid {
			return DataTableResult{}, fmt.Errorf("값을 넣을 셀 주소가 올바르지 않습니다")
		}
		// 수식이 든 칸에 값을 넣으면 그 수식이 사라진다. 되돌릴 수 없는
		// 일이므로 셈하기 전에 막는다.
		if cell, found := cells[address]; found && cell.Formula != "" {
			return DataTableResult{}, fmt.Errorf("값을 넣을 셀 %s 에 수식이 있습니다", address)
		}
	}
	rows, columns := len(input.ColumnValues), len(input.RowValues)
	if columns == 0 {
		columns = 1
	}
	if rows == 0 {
		rows = 1
	}
	if rows*columns > DataTableMaxCells {
		return DataTableResult{}, fmt.Errorf("데이터 표는 한 번에 %d칸까지입니다", DataTableMaxCells)
	}
	// 시트를 한 번만 베낀다. 칸마다 베끼면 그 비용이 계산 자체보다 커진다.
	working := make(map[string]CellState, len(cells))
	for address, cell := range cells {
		working[address] = cell
	}
	result := DataTableResult{Values: make([][]*float64, rows)}
	for rowIndex := 0; rowIndex < rows; rowIndex++ {
		result.Values[rowIndex] = make([]*float64, columns)
		for columnIndex := 0; columnIndex < columns; columnIndex++ {
			changed := make([]string, 0, 2)
			if columnInput != "" && rowIndex < len(input.ColumnValues) {
				working[columnInput] = CellState{Value: input.ColumnValues[rowIndex]}
				changed = append(changed, columnInput)
			}
			if rowInput != "" && columnIndex < len(input.RowValues) {
				working[rowInput] = CellState{Value: input.RowValues[columnIndex]}
				changed = append(changed, rowInput)
			}
			if len(changed) == 0 {
				return DataTableResult{}, fmt.Errorf("넣을 값이 없습니다")
			}
			value, err := dataTableCell(e, working, changed, target)
			if err != nil {
				result.Failures = append(result.Failures, DataTableFailure{Row: rowIndex, Column: columnIndex, Reason: err.Error()})
				continue
			}
			result.Values[rowIndex][columnIndex] = &value
		}
	}
	return result, nil
}

func dataTableCell(evaluator *Evaluator, working map[string]CellState, changed []string, target string) (float64, error) {
	recalculated, err := evaluator.Recalculate(working, changed)
	if err != nil {
		return 0, err
	}
	for _, cell := range recalculated.Cells {
		if cell.Address != target {
			continue
		}
		if cell.Error != nil {
			return 0, fmt.Errorf("%s", cell.Error.Code)
		}
		number, ok := numericValue(cell.Value)
		if !ok {
			return 0, fmt.Errorf("숫자가 아닙니다")
		}
		return number, nil
	}
	// 찾을 칸이 바뀌지 않았다는 것은 넣은 값이 그 칸에 닿지 않는다는 뜻이다.
	// 표를 0 으로 채우면 사람은 그 가정이 결과를 바꾸지 않는다고 읽는다.
	return 0, fmt.Errorf("넣은 값이 찾을 셀에 닿지 않습니다")
}
