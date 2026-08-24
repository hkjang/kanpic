package formula

import "testing"

// 엑셀은 복소수를 "3+4i" 같은 글자로 다룬다. 셈할 때마다 읽고 다시 적으므로
// 읽는 쪽과 적는 쪽이 서로를 되돌려야 한다. 기댓값은 파이썬 cmath 로 따로
// 계산해 맞췄다.
func TestComplexArithmetic(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{`=COMPLEX(3,4)`, "3+4i"},
		{`=COMPLEX(3,4,"j")`, "3+4j"},
		{`=COMPLEX(3,-4)`, "3-4i"},
		// 1 과 -1 은 숫자를 적지 않고, 한쪽이 0 이면 그쪽을 적지 않는다.
		{`=COMPLEX(0,1)`, "i"},
		{`=COMPLEX(0,-1)`, "-i"},
		{`=COMPLEX(1,0)`, "1"},
		{`=COMPLEX(0,0)`, "0"},
		{`=IMREAL("3+4i")`, "3"},
		{`=IMAGINARY("3+4i")`, "4"},
		{`=IMABS("3+4i")`, "5"},
		{`=ROUND(IMARGUMENT("3+4i"),9)`, "0.927295218"},
		{`=IMCONJUGATE("3+4i")`, "3-4i"},
		{`=IMSUM("3+4i","1-2i")`, "4+2i"},
		{`=IMSUB("3+4i","1-2i")`, "2+6i"},
		{`=IMPRODUCT("3+4i","1-2i")`, "11-2i"},
		{`=IMDIV("3+4i","1-2i")`, "-1+2i"},
		{`=IMPOWER("3+4i",2)`, "-7+24i"},
		{`=IMSQRT("3+4i")`, "2+i"},
		{`=IMEXP("1+1i")`, "1.46869393991589+2.28735528717884i"},
		{`=IMLN("3+4i")`, "1.6094379124341+0.927295218001612i"},
		{`=IMLOG10("3+4i")`, "0.698970004336019+0.402719196273373i"},
		{`=IMLOG2("3+4i")`, "2.32192809488736+1.33780421245098i"},
		// 허수 단위만 적힌 것도 읽는다.
		{`=IMREAL("i")`, "0"},
		{`=IMAGINARY("i")`, "1"},
		{`=IMAGINARY("-i")`, "-1"},
		{`=IMREAL("5")`, "5"},
		{`=IMAGINARY("5")`, "0"},
		// 지수 표기의 부호는 실수부와 허수부가 갈리는 자리가 아니다.
		{`=COMPLEX(IMREAL("2.5e3-4i"),IMAGINARY("2.5e3-4i"))`, "2500-4i"},
		// 곱하고 나누면 제자리로 온다.
		{`=IMDIV(IMPRODUCT("3+4i","1-2i"),"1-2i")`, "3+4i"},
		{`=IMSUB(IMSUM("3+4i","1-2i"),"1-2i")`, "3+4i"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s -> %s %s", item.expression, result.Error.Code, result.Error.Message)
			continue
		}
		if actual := display(result.Value); actual != item.expected {
			t.Errorf("%s=%q, 기대=%q", item.expression, actual, item.expected)
		}
	}
	for _, item := range []struct{ expression, code string }{
		// 한 식 안에서 i 와 j 를 섞으면 어느 쪽으로 적을지 정할 수 없다.
		{`=IMSUM("3+4i","1+2j")`, "#VALUE!"},
		{`=IMDIV("3+4i","0")`, "#NUM!"},
		{`=IMREAL("한글")`, "#NUM!"},
		{`=IMLN("0")`, "#NUM!"},
		{`=IMARGUMENT("0")`, "#DIV/0!"},
		{`=COMPLEX(3,4,"k")`, "#VALUE!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
	// j 로 적은 것은 j 로 답한다. 사람이 고른 표기를 바꾸지 않는다.
	if result := engine.Evaluate(`=IMSUM("3+4j","1-2j")`, map[string]any{}); result.Error != nil || display(result.Value) != "4+2j" {
		t.Errorf("j 표기=%#v", result)
	}
}
