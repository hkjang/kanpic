package formula

import "testing"

// 이름 있는 수식은 팀에서 쓰는 셈을 한 번 정의해 두고 부르는 것이다.
// 안쪽은 LAMBDA 와 같은 기계를 쓴다 — 두 벌을 만들면 한쪽만 고쳐진다.
func TestNamedFunctionsCallStoredFormulas(t *testing.T) {
	t.Parallel()
	engine := New()
	engine.SetNamedFunctions(map[string]NamedFunction{
		"마진율":    {Parameters: []string{"매출", "원가"}, Body: "=(매출-원가)/매출"},
		"TAXED":  {Parameters: []string{"amount"}, Body: "amount*1.1"},
		"GREET":  {Parameters: []string{"name"}, Body: `="안녕, "&name`},
		"NOARGS": {Parameters: nil, Body: "42"},
		"USESUM": {Parameters: []string{"r"}, Body: "SUM(r)*2"},
		"NESTED": {Parameters: []string{"x"}, Body: "TAXED(x)+1"},
	})
	cells := map[string]any{"A1": 10.0, "A2": 20.0}
	for _, item := range []struct{ expression, expected string }{
		// 한글 이름과 한글 매개변수를 쓴다. 팀에서 쓰는 셈의 이름은 대개
		// 우리말이다.
		{"=마진율(100,60)", "0.4"},
		{"=마진율(100,60)*100", "40"},
		{"=TAXED(1000)", "1100"},
		{`=GREET("한글")`, "안녕, 한글"},
		{"=NOARGS()", "42"},
		// 이름 있는 수식끼리 부를 수 있다.
		{"=NESTED(100)", "111"},
		// 내장 함수 안에서도 쓴다.
		{"=SUM(TAXED(100),TAXED(200))", "330"},
		// 대소문자를 가리지 않는다.
		{"=taxed(1000)", "1100"},
	} {
		result := engine.Evaluate(item.expression, cells)
		if result.Error != nil {
			t.Errorf("%s -> %s %s", item.expression, result.Error.Code, result.Error.Message)
			continue
		}
		if actual := display(result.Value); actual != item.expected {
			t.Errorf("%s=%q, 기대=%q", item.expression, actual, item.expected)
		}
	}
	// 본문이 셀을 가리키면 그 셀도 이 수식이 기대는 곳이다. 그러지 않으면
	// 원본이 바뀌어도 다시 셈하지 않는다.
	result := engine.Evaluate("=USESUM(A1:A2)", cells)
	if result.Error != nil || display(result.Value) != "60" {
		t.Fatalf("범위 인수=%#v", result)
	}
	if len(result.Dependencies) != 2 {
		t.Errorf("의존 셀=%v, 기대=[A1 A2]", result.Dependencies)
	}
	if single := engine.Evaluate("=TAXED(A1)", cells); single.Error != nil || len(single.Dependencies) != 1 {
		t.Errorf("셀 인수의 의존=%#v", single)
	}
}

// 답을 낼 수 없는 자리는 그렇다고 말해야 한다. 특히 자기 자신을 부르는
// 정의는 파싱이 끝나지 않으므로 반드시 막아야 한다.
func TestNamedFunctionsRefuseWhatTheyCannotDo(t *testing.T) {
	t.Parallel()
	engine := New()
	engine.SetNamedFunctions(map[string]NamedFunction{
		"TAXED":   {Parameters: []string{"amount"}, Body: "amount*1.1"},
		"LOOPY":   {Parameters: []string{"x"}, Body: "LOOPY(x)+1"},
		"BADBODY": {Parameters: []string{"x"}, Body: "x+"},
		"EMPTY":   {Parameters: []string{"x"}, Body: ""},
	})
	for _, item := range []struct{ expression, code string }{
		// 인수 개수가 맞아야 한다.
		{"=TAXED(1000,2)", "#N/A"},
		{"=TAXED()", "#N/A"},
		{"=LOOPY(1)", "#VALUE!"},
		{"=BADBODY(1)", "#VALUE!"},
		{"=EMPTY(1)", "#VALUE!"},
		// 저장해 두지 않은 이름은 그대로 모르는 함수다.
		{"=UNKNOWNFN(1)", "#NAME?"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
	// 자기 자신을 부르는 오류는 한 문장이어야 한다. 겹겹이 싸면 같은 말이
	// 열여섯 번 쌓여 읽을 수 없다.
	message := engine.Evaluate("=LOOPY(1)", map[string]any{}).Error.Message
	if len(message) > 120 {
		t.Errorf("되돌이 오류가 너무 길다: %q", message)
	}
}

// 이미 있는 함수 이름을 덮어쓰면 그 워크북의 모든 SUM 이 뜻을 잃는다.
func TestBuiltInFunctionsAreRecognised(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"SUM", "sum", "XLOOKUP", "STDEV.P", "LENB", "IMSUM", "LET", "LAMBDA"} {
		if !IsBuiltInFunction(name) {
			t.Errorf("%s 를 이미 있는 함수로 보지 않는다", name)
		}
	}
	for _, name := range []string{"마진율", "MYFUNC", "", "  "} {
		if IsBuiltInFunction(name) {
			t.Errorf("%q 를 이미 있는 함수로 보았다", name)
		}
	}
}
