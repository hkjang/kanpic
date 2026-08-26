package formula

import "testing"

// 엑셀은 =MAP(A1:A3,ABS) 처럼 LAMBDA(v,ABS(v)) 를 줄여 적는 것을 받아 준다.
// 사람이 쓰는 모양이 이쪽이므로 그대로 받아야 한다.
func TestFunctionNamesCanBePassedWithoutLambda(t *testing.T) {
	t.Parallel()
	evaluator := New()
	cells := map[string]any{"A1": -1.0, "A2": 2.0, "A3": -3.0}
	for expression, want := range map[string]float64{
		"=INDEX(MAP(A1:A3,ABS),1,1)":        1,
		"=INDEX(MAP(A1:A3,ABS),3,1)":        3,
		"=SUM(MAP(A1:A3,ABS))":              6,
		"=INDEX(BYROW(A1:A3,SUM),2,1)":      2,
		"=REDUCE(0,A1:A3,LAMBDA(a,b,a+b))":  -2,
		"=SUM(MAP(A1:A3,LAMBDA(v,ABS(v))))": 6,
	} {
		result := evaluator.Evaluate(expression, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", expression, result.Error)
			continue
		}
		if got, ok := result.Value.(float64); !ok || got != want {
			t.Errorf("%s = %#v, 원하는 것은 %v", expression, result.Value, want)
		}
	}
}

// 칸에 =SUM 이라고 적은 것은 이름을 잘못 적은 것이다. 함수를 값으로 넘기는
// 것은 MAP 같은 자리에서만 뜻이 있다.
func TestABareFunctionNameIsStillAMistakeInACell(t *testing.T) {
	t.Parallel()
	evaluator := New()
	for _, expression := range []string{"=SUM", "=ABS", "=NOSUCHNAME"} {
		result := evaluator.Evaluate(expression, nil)
		if result.Error == nil || result.Error.Code != "#NAME?" {
			t.Errorf("%s = %#v, err=%v — #NAME? 여야 한다", expression, result.Value, result.Error)
		}
	}
}

// 사람이 SUM 이라는 이름 범위를 만들어 두었다면 그쪽을 뜻했을 것이다.
func TestANamedRangeWinsOverTheFunctionOfTheSameName(t *testing.T) {
	t.Parallel()
	evaluator := NewScopedWithNames("s1", map[string]string{"Sheet1": "s1"}, map[string]NamedRange{
		"SUM": {SheetID: "s1", Range: "A1:A1"},
	})
	result := evaluator.Evaluate("=SUM+1", map[string]any{CellKey("s1", "A1"): 41.0})
	if result.Error != nil || result.Value != 42.0 {
		t.Errorf("이름 범위를 쓰지 않았다: %#v err=%v", result.Value, result.Error)
	}
}
