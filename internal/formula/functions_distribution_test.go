package formula

import "testing"

// 분포 함수는 값이 조금 어긋나도 사람이 알아채지 못한다. 그래서 기댓값을
// 기억으로 적지 않고 파이썬 statistics·math 로 따로 계산해 맞췄다.
func TestNormalFamilyMatchesIndependentlyComputedValues(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{"=ROUND(NORMSDIST(1.333333),10)", "0.9087887256"},
		{"=NORMSDIST(0)", "0.5"},
		{"=ROUND(GAUSS(2),10)", "0.4772498681"},
		{"=ROUND(NORMDIST(42,40,1.5,TRUE),10)", "0.9087887803"},
		{"=ROUND(NORMDIST(42,40,1.5,FALSE),10)", "0.1093400498"},
		// 되돌리기는 유리식만으로는 아홉째 자리에서 어긋난다. 한 번 다듬어
		// 넣은 값이 다시 정규분포를 지나도 제자리로 온다.
		{"=ROUND(NORMSINV(0.908789),9)", "1.333334673"},
		{"=ROUND(NORMINV(0.908789,40,1.5),9)", "42.00000201"},
		{"=ROUND(NORMSINV(NORMSDIST(1.2345)),9)", "1.2345"},
		{"=ROUND(LOGNORMDIST(4,3.5,1.2),10)", "0.0390835557"},
		{"=ROUND(LOGINV(0.039084,3.5,1.2),9)", "4.000025219"},
		{"=ROUND(CONFIDENCE(0.05,2.5,50),10)", "0.6929519122"},
		{"=ROUND(FISHER(0.75),10)", "0.9729550745"},
		{"=ROUND(FISHERINV(0.972955),10)", "0.7499999674"},
		{"=ROUND(STANDARDIZE(42,40,1.5),10)", "1.3333333333"},
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
	for _, item := range []struct{ expression, code string }{
		{"=NORMSINV(0)", "#NUM!"}, {"=NORMSINV(1)", "#NUM!"},
		{"=FISHER(1)", "#NUM!"}, {"=FISHER(-1)", "#NUM!"},
		{"=NORMDIST(1,0,0,TRUE)", "#NUM!"}, {"=LOGNORMDIST(0,3.5,1.2)", "#NUM!"},
		{"=CONFIDENCE(0,2.5,50)", "#NUM!"}, {"=STANDARDIZE(1,0,0)", "#NUM!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
}

// 셈으로 나오는 분포들. 이항과 포아송은 큰 수에서 곱해 나가면 넘치므로
// 감마의 로그를 지나 셈한다.
func TestDiscreteDistributions(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{"=ROUND(EXPONDIST(0.2,10,TRUE),10)", "0.8646647168"},
		{"=ROUND(EXPONDIST(0.2,10,FALSE),10)", "1.3533528324"},
		{"=ROUND(POISSON(2,5,TRUE),10)", "0.1246520195"},
		{"=ROUND(POISSON(2,5,FALSE),10)", "0.0842243375"},
		{"=ROUND(BINOMDIST(6,10,0.5,FALSE),10)", "0.205078125"},
		{"=ROUND(BINOMDIST(6,10,0.5,TRUE),10)", "0.828125"},
		{"=ROUND(NEGBINOMDIST(10,5,0.25),10)", "0.0550486604"},
		{"=ROUND(HYPGEOMDIST(1,4,8,20),10)", "0.3632610939"},
		{"=ROUND(WEIBULL(105,20,100,TRUE),10)", "0.9295813901"},
		{"=ROUND(WEIBULL(105,20,100,FALSE),10)", "0.035588864"},
		// 누적이 0.75 를 처음 넘는 자리는 4 다.
		{"=CRITBINOM(6,0.5,0.75)", "4"},
		// 큰 수에서도 넘치지 않는다. 곱해 나가면 여기서 무한대가 된다.
		{"=ROUND(BINOMDIST(500,1000,0.5,FALSE),10)", "0.0252250182"},
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
	for _, expression := range []string{
		"=EXPONDIST(-1,10,TRUE)", "=POISSON(-1,5,TRUE)", "=BINOMDIST(11,10,0.5,FALSE)",
		"=HYPGEOMDIST(5,4,8,20)", "=CRITBINOM(6,0.5,0)", "=WEIBULL(105,0,100,TRUE)",
	} {
		if result := engine.Evaluate(expression, map[string]any{}); result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", expression, result.Value)
		}
	}
}

// 흩어진 정도와 범위를 짝지어 세는 것들.
func TestSpreadAndRangeStatistics(t *testing.T) {
	t.Parallel()
	engine := New()
	const sample = "{4,5,6,7,5,4,3}"
	const ranked = "{13,12,11,8,4,3,2,1,1,1}"
	for _, item := range []struct{ expression, expected string }{
		{"=ROUND(AVEDEV(" + sample + "),10)", "1.0204081633"},
		{"=ROUND(DEVSQ(" + sample + "),10)", "10.8571428571"},
		{"=ROUND(SKEW(" + sample + "),9)", "0.352133025"},
		{"=ROUND(KURT(" + sample + "),9)", "-0.302493075"},
		// 엑셀은 자리 수를 반올림하지 않고 잘라 낸다. 0.5555… 는 0.555 다.
		{"=PERCENTRANK(" + ranked + ",2)", "0.333"},
		{"=PERCENTRANK(" + ranked + ",4)", "0.555"},
		{"=PERCENTRANK(" + ranked + ",5,4)", "0.5833"},
		{"=PERCENTRANK(" + ranked + ",8)", "0.666"},
		// 자르는 일도 십진수로 해야 한다. 4.6 의 순위는 0.575 인데 이진
		// 실수로 다섯 자리를 밀면 57499.999… 가 되어 0.57499 가 나왔다.
		{"=PERCENTRANK({0,1,2,3,4,5,6,7,8},4.6,5)", "0.575"},
		{"=ROUND(TRIMMEAN({4,5,6,7,2,3,4,5,1,2,3},0.2),10)", "3.7777777778"},
		{"=ROUND(PROB({0,1,2,3},{0.2,0.3,0.1,0.4},2),10)", "0.1"},
		{"=ROUND(PROB({0,1,2,3},{0.2,0.3,0.1,0.4},1,3),10)", "0.8"},
		{"=ROUND(STEYX({2,3,9,1,8,7,5},{6,5,11,7,5,4,4}),10)", "3.3057189502"},
		{"=ROUND(ZTEST({3,6,7,8,6,5,4,2,1,9},4),10)", "0.0905741969"},
		{"=ROUND(ZTEST({3,6,7,8,6,5,4,2,1,9},4,1),10)", "0.0002521091"},
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
	// 가장 자주 나온 값이 여럿이면 모두 돌려주고, 처음 나온 차례를 지킨다.
	modes := engine.Evaluate("=MODE.MULT({1,2,3,4,3,2,1,2,3})", map[string]any{})
	if modes.Error != nil {
		t.Fatalf("MODE.MULT -> %#v", modes.Error)
	}
	found, err := toArray(modes.Value)
	if err != nil || found.rows != 2 || display(found.values[0]) != "2" || display(found.values[1]) != "3" {
		t.Errorf("MODE.MULT=%#v %v", found, err)
	}
	if result := engine.Evaluate("=MODE.MULT({1,2,3})", map[string]any{}); result.Error == nil || result.Error.Code != "#N/A" {
		t.Errorf("되풀이 없는 MODE.MULT=%#v", result)
	}
	for _, item := range []struct{ expression, code string }{
		{"=PERCENTRANK(" + ranked + ",99)", "#N/A"},
		// 확률의 합이 1 이 아니면 확률표가 아니다.
		{"=PROB({0,1},{0.2,0.3},1)", "#NUM!"},
		{"=STEYX({1,2},{1,2})", "#DIV/0!"},
		{"=SKEW({1,2})", "#DIV/0!"},
		{"=KURT({1,2,3})", "#DIV/0!"},
		{"=SKEW({2,2,2})", "#DIV/0!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
}
