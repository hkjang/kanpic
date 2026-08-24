package formula

import "testing"

// 진법 함수의 값은 **10자리 2의 보수** 다. 그래서 BIN2DEC(1111111111) 은
// 1023 이 아니라 -1 이다. 이것을 놓치면 음수가 큰 양수로 읽힌다.
func TestBaseConversionsUseTwosComplement(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{"=BIN2DEC(1100100)", "100"},
		{"=BIN2DEC(1111111111)", "-1"},
		{"=BIN2DEC(1110011100)", "-100"},
		{"=BIN2HEX(11111011,4)", "00FB"},
		{"=BIN2OCT(1001)", "11"},
		{"=DEC2BIN(9)", "1001"},
		{"=DEC2BIN(9,4)", "1001"},
		{"=DEC2BIN(-100)", "1110011100"},
		{"=DEC2HEX(100)", "64"},
		{"=DEC2HEX(-54)", "FFFFFFFFCA"},
		{"=DEC2OCT(58)", "72"},
		{`=HEX2DEC("A5")`, "165"},
		{`=HEX2DEC("FFFFFFFF5B")`, "-165"},
		{`=HEX2BIN("F",8)`, "00001111"},
		{`=HEX2OCT("F",3)`, "017"},
		{"=OCT2DEC(54)", "44"},
		{"=OCT2DEC(7777777533)", "-165"},
		{"=OCT2BIN(3,3)", "011"},
		{"=OCT2HEX(100)", "40"},
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
	// 자리 수를 넘거나 모자라면 답을 낼 수 없다고 말한다. 잘라서 내면
	// 사람은 틀린 값을 받는다.
	for _, expression := range []string{
		"=DEC2BIN(600)", "=DEC2BIN(9,2)", `=BIN2DEC("11111111111")`, `=HEX2DEC("가나다")`,
	} {
		if result := engine.Evaluate(expression, map[string]any{}); result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", expression, result.Value)
		}
	}
}

// 비트 함수는 음수와 소수를 받지 않고 48비트까지만 센다. 엑셀이 그렇게
// 정해 두었다 — 넘으면 조용히 자르는 대신 #NUM! 이다.
func TestBitwiseAndComparisonFunctions(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{"=BITAND(23,10)", "2"},
		{"=BITOR(23,10)", "31"},
		{"=BITXOR(5,3)", "6"},
		{"=BITLSHIFT(4,2)", "16"},
		{"=BITRSHIFT(13,2)", "3"},
		{"=DELTA(5,4)", "0"},
		{"=DELTA(5,5)", "1"},
		{"=DELTA(0.5,0)", "0"},
		{"=GESTEP(5,4)", "1"},
		{"=GESTEP(-4,-5)", "1"},
		{"=GESTEP(-1)", "0"},
		{"=ROUND(ERF(1),6)", "0.842701"},
		{"=ROUND(ERFC(1),6)", "0.157299"},
		// 두 인수를 주면 그 구간의 값이다.
		{"=ROUND(ERF(0,1),6)", "0.842701"},
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
	for _, expression := range []string{"=BITAND(-1,1)", "=BITAND(1.5,1)", "=BITLSHIFT(1,54)"} {
		result := engine.Evaluate(expression, map[string]any{})
		if result.Error == nil || result.Error.Code != "#NUM!" {
			t.Errorf("%s -> %#v", expression, result)
		}
	}
}
