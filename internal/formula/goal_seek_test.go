package formula

import (
	"math"
	"strings"
	"testing"
)

func sheet(cells map[string]CellState) map[string]CellState { return cells }

// 대출 상환액을 정해 놓고 이자율을 되묻는 것이 목표값 찾기가 있는 이유다.
func TestGoalSeekFindsTheInputThatReachesTheGoal(t *testing.T) {
	t.Parallel()
	// B3 = 원금 * 이자율. 상환액이 120이 되려면 이자율은 0.12 여야 한다.
	cells := sheet(map[string]CellState{
		"B1": {Value: 1000.0},
		"B2": {Value: 0.05},
		"B3": {Formula: "=B1*B2"},
	})
	result, err := New().GoalSeek(cells, GoalSeekInput{Target: "B3", Changing: "B2", Goal: 120})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Converged {
		t.Fatalf("did not converge: %+v", result)
	}
	if math.Abs(result.Value-0.12) > 1e-6 {
		t.Fatalf("value=%v, want 0.12", result.Value)
	}
	// 움직이기 전 값도 알려 줘야 사람이 얼마나 움직이는지 안다.
	if math.Abs(result.Before-50) > 1e-9 {
		t.Fatalf("before=%v, want 50", result.Before)
	}
}

// 한 칸만 건너뛰는 것이 아니라 수식 사슬을 따라간다.
func TestGoalSeekFollowsAChainOfFormulas(t *testing.T) {
	t.Parallel()
	cells := sheet(map[string]CellState{
		"A1": {Value: 10.0},
		"A2": {Formula: "=A1*2"},
		"A3": {Formula: "=A2+5"},
		"A4": {Formula: "=A3*A3"},
	})
	result, err := New().GoalSeek(cells, GoalSeekInput{Target: "A4", Changing: "A1", Goal: 625})
	if err != nil {
		t.Fatal(err)
	}
	// (2x+5)^2 = 625 → 2x+5 = 25 → x = 10 … 이미 정답이므로 그대로 둔다.
	if !result.Converged || math.Abs(result.Value-10) > 1e-6 {
		t.Fatalf("result=%+v", result)
	}
	cells["A1"] = CellState{Value: 1.0}
	moved, err := New().GoalSeek(cells, GoalSeekInput{Target: "A4", Changing: "A1", Goal: 625})
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Converged || math.Abs(moved.Value-10) > 1e-4 {
		t.Fatalf("moved=%+v", moved)
	}
}

// 비선형도 푼다. 이것이 대수가 아니라 수치 탐색인 이유다.
func TestGoalSeekSolvesANonLinearFormula(t *testing.T) {
	t.Parallel()
	cells := sheet(map[string]CellState{
		"A1": {Value: 2.0},
		"A2": {Formula: "=A1*A1*A1"},
	})
	result, err := New().GoalSeek(cells, GoalSeekInput{Target: "A2", Changing: "A1", Goal: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Converged || math.Abs(result.Value-10) > 1e-4 {
		t.Fatalf("result=%+v", result)
	}
}

// 답이 없으면 마지막으로 시도한 숫자를 답인 척 내밀면 안 된다.
func TestGoalSeekSaysWhenItCannotGetThere(t *testing.T) {
	t.Parallel()
	// x*x 는 음수가 되지 않는다.
	cells := sheet(map[string]CellState{"A1": {Value: 2.0}, "A2": {Formula: "=A1*A1"}})
	result, err := New().GoalSeek(cells, GoalSeekInput{Target: "A2", Changing: "A1", Goal: -100})
	if err != nil {
		t.Fatal(err)
	}
	if result.Converged {
		t.Fatalf("claimed to reach an impossible goal: %+v", result)
	}
	if result.Reason == "" {
		t.Fatalf("stopped without saying why: %+v", result)
	}
}

// 영향을 받지 않는 칸을 바꿔 봐야 소용이 없다. 백 번 돌려 보고 포기하는 것이
// 아니라, 물어보는 순간 그렇다고 말할 수 있다.
func TestGoalSeekRefusesATargetItCannotMove(t *testing.T) {
	t.Parallel()
	cells := sheet(map[string]CellState{
		"A1": {Value: 5.0}, "B1": {Value: 3.0}, "C1": {Formula: "=B1*2"},
	})
	_, err := New().GoalSeek(cells, GoalSeekInput{Target: "C1", Changing: "A1", Goal: 100})
	if err == nil {
		t.Fatal("a target the changing cell does not reach was accepted")
	}
	if !strings.Contains(err.Error(), "영향") {
		t.Fatalf("err=%v", err)
	}
}

func TestGoalSeekRefusesWhatItCannotDo(t *testing.T) {
	t.Parallel()
	cells := sheet(map[string]CellState{
		"A1": {Value: 5.0},
		"A2": {Formula: "=A1*2"},
		"A3": {Value: "글자"},
		"A4": {Formula: "=A3&\"!\""},
	})
	for name, input := range map[string]GoalSeekInput{
		// 수식이 든 칸을 바꾸면 그 수식이 사라진다.
		"a formula as the changing cell": {Target: "A2", Changing: "A2", Goal: 1},
		"the same cell twice":            {Target: "A1", Changing: "A1", Goal: 1},
		"a value as the target":          {Target: "A1", Changing: "A3", Goal: 1},
		"a bad address":                  {Target: "돼지", Changing: "A1", Goal: 1},
	} {
		if _, err := New().GoalSeek(cells, input); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// 숫자가 아닌 결과는 목표에 가까워질 수가 없다.
	if _, err := New().GoalSeek(cells, GoalSeekInput{Target: "A4", Changing: "A1", Goal: 1}); err == nil {
		t.Fatal("a text result was accepted")
	}
}
