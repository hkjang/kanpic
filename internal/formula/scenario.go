package formula

import "fmt"

// 시나리오 견주기는 데이터 표와 같은 기계를 쓴다 — 칸에 값을 넣고 다시 셈해
// 결과를 읽는다. 다른 점은 한 번에 여러 칸을 바꾼다는 것이다.
//
//	낙관   단가 12000, 수량 1500  ->  이익 4,200만
//	보수   단가 10000, 수량 1200  ->  이익 1,800만
//
// 회의에서 두 안을 나란히 놓고 보는 그 일이다.

// ScenarioSet 은 견줄 가정 한 벌이다.
type ScenarioSet struct {
	Name   string
	Inputs map[string]float64
}

// ScenarioCompareInput 은 무엇을 견줄지다.
type ScenarioCompareInput struct {
	// Targets 는 읽을 결과 칸들이다.
	Targets []string
	// Sets 는 견줄 가정 벌들이다.
	Sets []ScenarioSet
}

type ScenarioCompareResult struct {
	// Current 는 지금 시트의 값이다. 가정을 넣기 전 모습을 함께 보여 주지
	// 않으면 사람은 무엇에서 무엇으로 바뀌는지 알 수 없다.
	Current []*float64 `json:"current"`
	// Rows 는 벌마다 한 줄이다.
	Rows []ScenarioCompareRow `json:"rows"`
}

type ScenarioCompareRow struct {
	Name     string            `json:"name"`
	Values   []*float64        `json:"values"`
	Failures []ScenarioFailure `json:"failures,omitempty"`
}

// ScenarioFailure 는 어느 결과 칸을 셈하지 못했는지 말한다.
type ScenarioFailure struct {
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// CompareScenarios 는 벌마다 값을 넣어 보며 결과를 늘어놓는다. 아무것도 쓰지
// 않는다 — 시트는 그대로 두고 답만 낸다.
func (e *Evaluator) CompareScenarios(cells map[string]CellState, input ScenarioCompareInput) (ScenarioCompareResult, error) {
	if len(input.Targets) == 0 {
		return ScenarioCompareResult{}, fmt.Errorf("견줄 결과 셀을 하나는 정해야 합니다")
	}
	if len(input.Sets) == 0 {
		return ScenarioCompareResult{}, fmt.Errorf("견줄 시나리오가 없습니다")
	}
	targets := make([]string, 0, len(input.Targets))
	for _, target := range input.Targets {
		address := normalizeAddress(target)
		if _, _, valid := SplitCellKey(address); !valid {
			return ScenarioCompareResult{}, fmt.Errorf("결과 셀 주소가 올바르지 않습니다")
		}
		targets = append(targets, address)
	}
	// 시트를 한 번만 베낀다.
	working := make(map[string]CellState, len(cells))
	for address, cell := range cells {
		working[address] = cell
	}
	// 지금 값도 다시 셈해서 얻는다. 담긴 값을 그대로 읽으면, 아직 셈하지
	// 않은 수식 칸이 빈 것으로 보인다. 가정 칸은 그대로 두고 바뀐 것으로만
	// 표시해, 그 칸에 기대는 수식이 다시 셈되게 한다.
	touched := make([]string, 0)
	for _, set := range input.Sets {
		for address := range set.Inputs {
			touched = append(touched, normalizeAddress(address))
		}
	}
	result := ScenarioCompareResult{Current: currentScenarioTargets(e, working, touched, targets)}
	for _, set := range input.Sets {
		// 벌마다 원래 값으로 되돌린 뒤 그 벌의 가정만 넣는다. 되돌리지 않으면
		// 앞 벌의 가정이 뒤 벌에 섞여, 어느 가정으로 셈한 값인지 알 수 없어진다.
		for address, cell := range cells {
			working[address] = cell
		}
		changed := make([]string, 0, len(set.Inputs))
		for address, value := range set.Inputs {
			normalized := normalizeAddress(address)
			if _, _, valid := SplitCellKey(normalized); !valid {
				return ScenarioCompareResult{}, fmt.Errorf("가정 셀 주소가 올바르지 않습니다")
			}
			// 수식이 든 칸에 값을 넣으면 그 수식이 사라진다.
			if cell, found := cells[normalized]; found && cell.Formula != "" {
				return ScenarioCompareResult{}, fmt.Errorf("가정 셀 %s 에 수식이 있습니다", address)
			}
			working[normalized] = CellState{Value: value}
			changed = append(changed, normalized)
		}
		if len(changed) == 0 {
			return ScenarioCompareResult{}, fmt.Errorf("%s 에 가정이 없습니다", set.Name)
		}
		recalculated, err := e.Recalculate(working, changed)
		if err != nil {
			return ScenarioCompareResult{}, err
		}
		row := ScenarioCompareRow{Name: set.Name, Values: make([]*float64, len(targets))}
		byAddress := make(map[string]RecalculatedCell, len(recalculated.Cells))
		for _, cell := range recalculated.Cells {
			byAddress[cell.Address] = cell
		}
		for index, target := range targets {
			cell, recomputed := byAddress[target]
			if !recomputed {
				// 다시 셈하지 않았다는 것은 이 벌이 그 칸에 닿지 않는다는
				// 뜻이다. 지금 값을 그대로 적으면 가정이 먹힌 것처럼 보인다.
				if value := readScenarioCell(working, target); value != nil {
					row.Values[index] = value
					continue
				}
				row.Failures = append(row.Failures, ScenarioFailure{Target: target, Reason: "숫자가 아닙니다"})
				continue
			}
			if cell.Error != nil {
				row.Failures = append(row.Failures, ScenarioFailure{Target: target, Reason: cell.Error.Code})
				continue
			}
			number, ok := numericValue(cell.Value)
			if !ok {
				row.Failures = append(row.Failures, ScenarioFailure{Target: target, Reason: "숫자가 아닙니다"})
				continue
			}
			row.Values[index] = &number
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

func currentScenarioTargets(evaluator *Evaluator, working map[string]CellState, touched, targets []string) []*float64 {
	values := make([]*float64, len(targets))
	byAddress := map[string]RecalculatedCell{}
	if len(touched) > 0 {
		if recalculated, err := evaluator.Recalculate(working, touched); err == nil {
			for _, cell := range recalculated.Cells {
				byAddress[cell.Address] = cell
			}
		}
	}
	for index, target := range targets {
		if cell, found := byAddress[target]; found && cell.Error == nil {
			if number, ok := numericValue(cell.Value); ok {
				values[index] = &number
				continue
			}
		}
		values[index] = readScenarioCell(working, target)
	}
	return values
}

func readScenarioCell(cells map[string]CellState, target string) *float64 {
	cell, found := cells[target]
	if !found {
		return nil
	}
	number, ok := numericValue(cell.Value)
	if !ok {
		return nil
	}
	return &number
}
