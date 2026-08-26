package formula

import (
	"reflect"
	"testing"
)

func groupCells() map[string]any {
	return map[string]any{
		"A1": "영업1팀", "B1": 100.0, "C1": "1분기",
		"A2": "영업2팀", "B2": 200.0, "C2": "1분기",
		"A3": "영업1팀", "B3": 50.0, "C3": "2분기",
		"A4": "영업1팀", "B4": 25.0, "C4": "1분기",
	}
}

func evaluateMatrix(t *testing.T, expression string) [][]any {
	t.Helper()
	result := New().Evaluate(expression, groupCells())
	if result.Error != nil {
		t.Fatalf("%s: %v", expression, result.Error)
	}
	matrix, ok := result.Value.([][]any)
	if !ok {
		t.Fatalf("%s = %#v, 표가 아니다", expression, result.Value)
	}
	return matrix
}

func TestGroupByAggregatesByLabel(t *testing.T) {
	t.Parallel()
	got := evaluateMatrix(t, "=GROUPBY(A1:A4,B1:B4,SUM)")
	want := [][]any{{"영업1팀", 175.0}, {"영업2팀", 200.0}, {"총합계", 375.0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GROUPBY = %#v, 원하는 것은 %#v", got, want)
	}
}

// 총계를 끄면 묶음만 남는다. 정렬을 끄면 표에 적힌 차례 그대로 둔다.
func TestGroupByHonoursTotalAndSortOptions(t *testing.T) {
	t.Parallel()
	if got := evaluateMatrix(t, "=GROUPBY(A1:A4,B1:B4,SUM,,0)"); len(got) != 2 {
		t.Errorf("총계를 껐는데 줄이 %d 개다: %#v", len(got), got)
	}
	// 내림차순이면 영업2팀이 먼저다.
	got := evaluateMatrix(t, "=GROUPBY(A1:A4,B1:B4,SUM,,0,-1)")
	if got[0][0] != "영업2팀" {
		t.Errorf("내림차순이 아니다: %#v", got)
	}
}

// 함수는 LAMBDA 로 적어도 되고 이름만 적어도 된다.
func TestGroupByTakesEitherFormOfFunction(t *testing.T) {
	t.Parallel()
	byName := evaluateMatrix(t, "=GROUPBY(A1:A4,B1:B4,SUM,,0)")
	byLambda := evaluateMatrix(t, "=GROUPBY(A1:A4,B1:B4,LAMBDA(v,SUM(v)),,0)")
	if !reflect.DeepEqual(byName, byLambda) {
		t.Errorf("줄여 적은 것과 LAMBDA 가 다르다:\n%#v\n%#v", byName, byLambda)
	}
	if got := evaluateMatrix(t, "=GROUPBY(A1:A4,B1:B4,COUNT,,0)"); got[0][1] != 3.0 {
		t.Errorf("COUNT = %#v, 영업1팀은 3줄이다", got[0][1])
	}
}

func TestPivotByCrossesRowsAndColumns(t *testing.T) {
	t.Parallel()
	got := evaluateMatrix(t, "=PIVOTBY(A1:A4,C1:C4,B1:B4,SUM)")
	want := [][]any{
		{nil, "1분기", "2분기", "총합계"},
		{"영업1팀", 125.0, 50.0, 175.0},
		// 영업2팀은 2분기에 자료가 없다. 0 이 아니라 비어 있어야 한다 —
		// 0 을 적으면 "그만큼 팔았다" 는 뜻이 되어 없는 것과 구별되지 않는다.
		{"영업2팀", 200.0, nil, 200.0},
		{"총합계", 325.0, 50.0, 375.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PIVOTBY = %#v,\n원하는 것은 %#v", got, want)
	}
}

// 엑셀이 받는 인수를 다 받지는 않는다. 받지 않는 자리에 값을 적으면 조용히
// 다르게 셈하지 않고 무엇까지 되는지 말한다.
func TestGroupBySaysWhatItSupports(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct{ expression, code string }{
		{"=GROUPBY(A1:A4,B1:B4,SUM,,1,1,TRUE)", "#VALUE!"},
		{"=GROUPBY(A1:A4,B1:B4,\"SUM\")", "#VALUE!"},
		{"=GROUPBY(A1:A4,B1:B3,SUM)", "#VALUE!"},
		{"=PIVOTBY(A1:A4,C1:C4,B1:B4,SUM,,1,1,1)", "#VALUE!"},
	} {
		result := New().Evaluate(testCase.expression, groupCells())
		if result.Error == nil || result.Error.Code != testCase.code {
			t.Errorf("%s = %#v err=%v, %s 여야 한다", testCase.expression, result.Value, result.Error, testCase.code)
		}
	}
}
