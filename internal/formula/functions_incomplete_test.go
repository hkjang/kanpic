package formula

import "testing"

// 카이제곱·t·F·베타는 모두 정규화된 불완전 감마와 불완전 베타로 풀린다.
// 기댓값은 여기 쓴 급수·연분수와 **다른 방법** 으로 맞췄다 — 확률밀도를
// 적분한 값과 교과서 표다. 같은 방법으로 검증하면 정의를 잘못 이해한 것을
// 잡을 수 없다.
func TestTestDistributionsMatchTablesAndIntegration(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		// 카이제곱 표: 자유도 10, 상측 5% 는 18.307 이다.
		{"=ROUND(CHIDIST(18.307,10),10)", "0.0500005891"},
		{"=ROUND(CHIINV(0.05,10),6)", "18.307038"},
		{"=ROUND(CHIDIST(3,5),10)", "0.6999858359"},
		// t 표: 자유도 10, 양측 5% 는 2.2281 이다. TINV 는 양측이다 —
		// 한쪽으로 읽으면 조용히 다른 값이 된다.
		{"=ROUND(TDIST(2.2281,10,2),10)", "0.0500032936"},
		{"=ROUND(TINV(0.05,10),6)", "2.228139"},
		{"=ROUND(TDIST(1.5,20,1),10)", "0.0746178856"},
		// F 표: 자유도 5와 10, 상측 5% 는 3.3258 이다.
		{"=ROUND(FDIST(3.3258,5,10),10)", "0.0500014015"},
		{"=ROUND(FINV(0.05,5,10),6)", "3.325835"},
		// 자유도 6과 4 에서 F=2 는 정확히 67/256 이다.
		{"=ROUND(FDIST(2,6,4),10)", "0.26171875"},
		{"=ROUND(BETADIST(0.5,8,10),10)", "0.6854705811"},
		{"=ROUND(BETADIST(2,8,10,1,3),10)", "0.6854705811"},
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
	// 되돌리는 쪽은 왕복해야 제자리로 온다. 꼬리에서 구간을 잘못 잡으면
	// 여기서 어긋난다.
	for _, item := range []struct{ expression, expected string }{
		{"=ROUND(CHIINV(CHIDIST(7.5,4),4),9)", "7.5"},
		{"=ROUND(TINV(TDIST(2.5,8,2),8),9)", "2.5"},
		{"=ROUND(FINV(FDIST(2.5,3,7),3,7),9)", "2.5"},
		{"=ROUND(BETAINV(BETADIST(2,8,10,1,3),8,10,1,3),8)", "2"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error != nil || display(result.Value) != item.expected {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.expected)
		}
	}
	for _, item := range []struct{ expression, code string }{
		{"=CHIDIST(-1,10)", "#NUM!"}, {"=CHIDIST(1,0)", "#NUM!"}, {"=CHIINV(0,10)", "#NUM!"},
		{"=TDIST(-1,10,1)", "#NUM!"}, {"=TDIST(1,10,3)", "#NUM!"}, {"=TINV(0,10)", "#NUM!"},
		{"=FDIST(-1,5,10)", "#NUM!"}, {"=FINV(0,5,10)", "#NUM!"}, {"=FDIST(1,0,10)", "#NUM!"},
		{"=BETADIST(5,8,10,1,3)", "#NUM!"}, {"=BETADIST(0.5,0,10)", "#NUM!"}, {"=BETAINV(2,8,10)", "#NUM!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
}

// 두 표본을 견주는 검정. 기댓값 두 개는 처음에 적분으로 잡았다가 여덟째
// 자리에서 갈렸는데, 자유도 3 의 카이제곱 닫힌 형태와 꼬리까지 세는 치환
// 적분으로 다시 재니 **엔진 쪽이 맞았다.** 적분이 특이점과 잘린 꼬리에서
// 약했던 것이다.
func TestHypothesisTestsCompareTwoSamples(t *testing.T) {
	t.Parallel()
	engine := New()
	const first = "{6,7,9,15,21}"
	const second = "{20,28,31,38,40}"
	for _, item := range []struct{ expression, expected string }{
		{"=ROUND(CHITEST({58,35,75,42},{45,55,60,50}),10)", "0.0011032066"},
		{"=ROUND(FTEST(" + first + "," + second + "),10)", "0.6483178468"},
		// 1 은 대응표본, 2 는 등분산, 3 은 웰치다. 셋이 서로 다른 값이어야
		// 한다 — 같으면 종류를 보지 않고 있다는 뜻이다.
		{"=ROUND(TTEST(" + first + "," + second + ",2,1),10)", "0.0002413396"},
		{"=ROUND(TTEST(" + first + "," + second + ",2,2),10)", "0.0025154775"},
		{"=ROUND(TTEST(" + first + "," + second + ",2,3),10)", "0.002866267"},
		// 한쪽은 양쪽의 절반이다.
		{"=ROUND(TTEST(" + first + "," + second + ",1,2),10)", "0.0012577387"},
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
		{"=TTEST(" + first + "," + second + ",3,2)", "#NUM!"},
		{"=TTEST(" + first + "," + second + ",2,4)", "#NUM!"},
		{"=CHITEST({1,2},{1,0})", "#DIV/0!"},
		{"=CHITEST({1,2},{1,2,3})", "#N/A"},
		{"=TTEST({1,2,3},{1,2},2,1)", "#N/A"},
		{"=FTEST({1,1,1},{1,2,3})", "#DIV/0!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
}
