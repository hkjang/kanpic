package formula

import "testing"

// 채권과 단기 증권은 모두 날짜 사이의 연 단위 길이에 달려 있다. 기준을
// 잘못 세면 값이 그럴듯하게 틀리므로, 기댓값을 파이썬으로 따로 계산해
// 맞췄다.
func TestDiscountSecuritiesMatchIndependentlyComputedValues(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{`=ROUND(DISC("2018-01-25","2018-06-15",97.975,100,1),10)`, "0.0524202128"},
		{`=ROUND(INTRATE("2018-02-15","2018-05-15",100000,104044.44,2),10)`, "0.1635953258"},
		{`=ROUND(RECEIVED("2018-02-15","2018-05-15",1000000,0.0575,2),4)`, "1014420.2659"},
		{`=ROUND(PRICEDISC("2018-02-16","2019-03-01",0.0525,100,2),10)`, "94.4875"},
		{`=ROUND(YIELDDISC("2018-02-16","2018-03-01",99.795,100,2),10)`, "0.0568858468"},
		{`=ROUND(PRICEMAT("2018-02-15","2018-04-13","2017-11-11",0.061,0.061),10)`, "99.9844988756"},
		{`=ROUND(YIELDMAT("2018-02-15","2018-04-13","2017-11-11",0.061,99.98),10)`, "0.0612776185"},
		{`=ROUND(TBILLPRICE("2018-03-31","2018-06-01",0.0914),10)`, "98.4258888889"},
		{`=ROUND(TBILLYIELD("2018-03-31","2018-06-01",98.45),10)`, "0.0914169629"},
		{`=ROUND(TBILLEQ("2018-03-31","2018-06-01",0.0914),10)`, "0.0941514936"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s -> %s %s", item.expression, result.Error.Code, result.Error.Message)
			continue
		}
		if actual := display(result.Value); actual != item.expected {
			t.Errorf("%s=%s, 기대=%s", item.expression, actual, item.expected)
		}
	}
	// 할인율로 매긴 가격에서 수익률을 되물으면 할인율과 **다른** 값이
	// 나온다. 두 셈이 나누는 밑이 다르기 때문이다 — 같은 값이 나오면
	// 오히려 한쪽이 틀린 것이다.
	for _, item := range []struct{ expression, expected string }{
		{`=ROUND(YIELDDISC("2018-02-16","2018-03-01",PRICEDISC("2018-02-16","2018-03-01",0.05,100,2),100,2),9)`, "0.050090441"},
		{`=ROUND(TBILLYIELD("2018-03-31","2018-06-01",TBILLPRICE("2018-03-31","2018-06-01",0.0914)),9)`, "0.092861747"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error != nil || display(result.Value) != item.expected {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.expected)
		}
	}
	for _, item := range []struct{ expression, code string }{
		// 결제일이 만기일보다 뒤면 셈이 되지 않는다.
		{`=DISC("2018-06-15","2018-01-25",97.975,100)`, "#NUM!"},
		{`=PRICEMAT("2018-02-15","2018-04-13","2018-03-11",0.061,0.061)`, "#NUM!"},
		// 단기 국채는 만기가 한 해를 넘지 않는다.
		{`=TBILLPRICE("2018-03-31","2020-06-01",0.0914)`, "#NUM!"},
		{`=DISC("2018-01-25","2018-06-15",97.975,100,7)`, "#NUM!"},
		{`=RECEIVED("2018-01-25","2019-06-15",1000000,2,2)`, "#NUM!"},
		{`=DISC("2018-01-25","2018-06-15",0,100)`, "#NUM!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
}

// 분수 표기와 두 가지 간단한 셈.
func TestDollarFractionsAndSimpleFinance(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		// 1.02 는 1과 16분의 2 다. 분모의 자리 수만큼 밀어 읽는다.
		{"=DOLLARDE(1.02,16)", "1.125"},
		{"=DOLLARDE(1.1,32)", "1.3125"},
		{"=DOLLARFR(1.125,16)", "1.02"},
		{"=ROUND(DOLLARFR(1.3125,32),10)", "1.1"},
		{"=ROUND(FVSCHEDULE(1,{0.09,0.11,0.1}),10)", "1.33089"},
		{"=ROUND(ISPMT(0.1,1,12,4000000),6)", "-366666.666667"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s -> %s %s", item.expression, result.Error.Code, result.Error.Message)
			continue
		}
		if actual := display(result.Value); actual != item.expected {
			t.Errorf("%s=%s, 기대=%s", item.expression, actual, item.expected)
		}
	}
	// 두 방향은 서로를 되돌린다.
	if result := engine.Evaluate("=ROUND(DOLLARFR(DOLLARDE(1.07,16),16),10)", map[string]any{}); result.Error != nil || display(result.Value) != "1.07" {
		t.Errorf("왕복=%#v", result)
	}
	for _, item := range []struct{ expression, code string }{
		{"=DOLLARDE(1.02,0)", "#DIV/0!"},
		{"=DOLLARDE(1.02,-1)", "#NUM!"},
		{"=ISPMT(0.1,1,0,4000000)", "#DIV/0!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
}
