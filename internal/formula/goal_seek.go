package formula

import (
	"fmt"
	"math"
	"strings"
)

// Goal seek answers the question a spreadsheet is usually built to ask
// backwards: not "what does this come to" but "what would the input have to be
// for this to come to that". A loan payment of 1,200,000 — at what rate? A
// margin of 15% — at what price?
//
// It is a numerical search, not algebra: the target is recalculated with
// different inputs until it lands on the goal. That means it can fail, and when
// it does the honest answer is to say so rather than to return the last number
// it happened to try.

const (
	// GoalSeekMaxIterations bounds the search. Excel stops at 100 and so does
	// this; a formula that has not converged by then is not going to.
	GoalSeekMaxIterations = 100
	// goalSeekTolerance is how close counts as arrived, relative to the goal so
	// that seeking 1,200,000 is not held to the same absolute error as seeking
	// 0.15.
	goalSeekTolerance = 1e-9
)

type GoalSeekInput struct {
	// Target is the formula cell that should reach the goal.
	Target string
	// Changing is the cell to vary. It must hold a value rather than a formula.
	Changing string
	Goal     float64
}

type GoalSeekResult struct {
	// Value is what the changing cell would have to hold.
	Value float64 `json:"value"`
	// Result is what the target became at that value.
	Result float64 `json:"result"`
	// Before is what the target was before the search, so the caller can show
	// the move rather than just the destination.
	Before     float64 `json:"before"`
	Converged  bool    `json:"converged"`
	Iterations int     `json:"iterations"`
	// Reason says why the search stopped short. Empty when it converged.
	Reason string `json:"reason,omitempty"`
}

// GoalSeek varies one cell until another reaches a value.
//
// The cells are the sheet as it stands; nothing is written. What comes back is
// a proposal for the caller to apply or discard.
func (e *Evaluator) GoalSeek(cells map[string]CellState, input GoalSeekInput) (GoalSeekResult, error) {
	target := normalizeAddress(strings.TrimSpace(input.Target))
	changing := normalizeAddress(strings.TrimSpace(input.Changing))
	if _, _, ok := SplitCellKey(target); !ok {
		return GoalSeekResult{}, fmt.Errorf("찾을 셀 주소가 올바르지 않습니다: %q", input.Target)
	}
	if _, _, ok := SplitCellKey(changing); !ok {
		return GoalSeekResult{}, fmt.Errorf("바꿀 셀 주소가 올바르지 않습니다: %q", input.Changing)
	}
	if target == changing {
		return GoalSeekResult{}, fmt.Errorf("찾을 셀과 바꿀 셀이 같습니다")
	}
	if math.IsNaN(input.Goal) || math.IsInf(input.Goal, 0) {
		return GoalSeekResult{}, fmt.Errorf("목표값이 숫자가 아닙니다")
	}
	// 바꿀 칸이 수식이면 값을 넣는 순간 그 수식이 사라진다. 엑셀도 거절한다.
	if strings.TrimSpace(cells[changing].Formula) != "" {
		return GoalSeekResult{}, fmt.Errorf("바꿀 셀에 수식이 있습니다. 값이 든 칸만 바꿀 수 있습니다")
	}
	if strings.TrimSpace(cells[target].Formula) == "" {
		return GoalSeekResult{}, fmt.Errorf("찾을 셀에 수식이 없습니다. 수식이 없으면 바꿀 것이 없습니다")
	}

	// 시트를 한 번만 베낀다. 되풀이마다 베끼면 백 번 도는 동안 시트를 백 번
	// 베끼게 되고, 그 비용이 계산 자체보다 커진다.
	working := make(map[string]CellState, len(cells))
	for address, cell := range cells {
		working[address] = cell
	}
	measure := func(candidate float64) (float64, error) {
		working[changing] = CellState{Value: candidate}
		result, err := e.Recalculate(working, []string{changing})
		if err != nil {
			return 0, err
		}
		for _, cell := range result.Cells {
			if cell.Address != target {
				continue
			}
			if cell.Error != nil {
				return 0, fmt.Errorf("찾을 셀이 %s 오류가 됩니다", cell.Error.Code)
			}
			number, ok := numericValue(cell.Value)
			if !ok {
				return 0, fmt.Errorf("찾을 셀이 숫자가 아닙니다")
			}
			return number, nil
		}
		return 0, fmt.Errorf("찾을 셀이 바꿀 셀의 영향을 받지 않습니다")
	}

	start, _ := numericValue(cells[changing].Value)
	before, err := measure(start)
	if err != nil {
		return GoalSeekResult{}, err
	}
	tolerance := goalSeekTolerance * math.Max(1, math.Abs(input.Goal))
	if math.Abs(before-input.Goal) <= tolerance {
		return GoalSeekResult{Value: start, Result: before, Before: before, Converged: true}, nil
	}

	// 두 번째 점은 첫 점 옆에서 잡는다. 0 에서 시작하면 옆이 1 이다.
	step := math.Abs(start) * 0.01
	if step < 1e-4 {
		step = 1
	}
	previousX, previousF := start, before-input.Goal
	currentX := start + step
	result := GoalSeekResult{Value: start, Result: before, Before: before}
	for iteration := 1; iteration <= GoalSeekMaxIterations; iteration++ {
		currentValue, err := measure(currentX)
		if err != nil {
			result.Reason = err.Error()
			return result, nil
		}
		currentF := currentValue - input.Goal
		result.Iterations, result.Value, result.Result = iteration, currentX, currentValue
		if math.Abs(currentF) <= tolerance {
			result.Converged = true
			return result, nil
		}
		denominator := currentF - previousF
		if denominator == 0 {
			// 두 점의 값이 같다. 평평한 구간이거나 아예 영향을 받지 않는다.
			result.Reason = "값이 더 이상 움직이지 않습니다"
			return result, nil
		}
		nextX := currentX - currentF*(currentX-previousX)/denominator
		if math.IsNaN(nextX) || math.IsInf(nextX, 0) {
			result.Reason = "값이 발산합니다"
			return result, nil
		}
		previousX, previousF, currentX = currentX, currentF, nextX
	}
	result.Reason = fmt.Sprintf("%d번 계산해도 목표에 이르지 못했습니다", GoalSeekMaxIterations)
	return result, nil
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}
