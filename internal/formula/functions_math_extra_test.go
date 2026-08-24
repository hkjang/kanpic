package formula

import "testing"

// 삼각의 역수와 쌍곡선. 나누는 쪽이 0 이면 답이 없다고 말해야 한다 —
// 무한대를 내면 뒤의 셈이 조용히 망가진다.
func TestReciprocalAndHyperbolicFunctions(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{"=ROUND(SEC(0),6)", "1"},
		{"=ROUND(CSC(PI()/2),6)", "1"},
		{"=ROUND(COT(PI()/4),6)", "1"},
		{"=ROUND(SECH(0),6)", "1"},
		{"=ROUND(COTH(1),6)", "1.313035"},
		// ACOT 는 0 과 파이 사이의 값이다. atan(1/x) 를 쓰면 음수에서
		// 부호가 뒤집혀 -0.785398 이 된다.
		{"=ROUND(ACOT(1),6)", "0.785398"},
		{"=ROUND(ACOT(-1),6)", "2.356194"},
		{"=ROUND(ACOTH(2),6)", "0.549306"},
		{"=ROUND(ACOSH(1),6)", "0"},
		{"=ROUND(ACOSH(10),6)", "2.993223"},
		{"=ROUND(ASINH(1),6)", "0.881374"},
		{"=ROUND(ATANH(0.5),6)", "0.549306"},
		{"=ROUND(GAMMALN(4),6)", "1.791759"},
		{"=ROUND(SQRTPI(1),6)", "1.772454"},
		{"=ROUND(SQRTPI(2),6)", "2.506628"},
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
		{"=COT(0)", "#DIV/0!"}, {"=CSC(0)", "#DIV/0!"}, {"=CSCH(0)", "#DIV/0!"}, {"=COTH(0)", "#DIV/0!"},
		{"=ACOSH(0)", "#NUM!"}, {"=ATANH(1)", "#NUM!"}, {"=ACOTH(0.5)", "#NUM!"}, {"=GAMMALN(0)", "#NUM!"},
		{"=SQRTPI(-1)", "#NUM!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil || result.Error.Code != item.code {
			t.Errorf("%s -> %#v, 기대=%s", item.expression, result, item.code)
		}
	}
}

// 자리 올림의 갈래는 **음수** 에서 갈린다. 하나로 뭉뚱그리면 음수에서
// 조용히 다른 값을 낸다.
func TestPreciseRoundingSplitsOnNegativeNumbers(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{"=CEILING.MATH(6.7)", "7"},
		// 기본은 0 쪽으로 올린다.
		{"=CEILING.MATH(-5.5)", "-5"},
		// mode 를 주면 0 에서 멀어지는 쪽으로 간다.
		{"=CEILING.MATH(-5.5,1,1)", "-6"},
		{"=CEILING.MATH(-8.1,2)", "-8"},
		{"=FLOOR.MATH(6.7)", "6"},
		{"=FLOOR.MATH(-5.5)", "-6"},
		{"=FLOOR.MATH(-5.5,1,1)", "-5"},
		// PRECISE 갈래는 언제나 한 방향이고 기준의 부호를 무시한다.
		{"=CEILING.PRECISE(4.3)", "5"},
		{"=CEILING.PRECISE(-4.3)", "-4"},
		{"=CEILING.PRECISE(-4.3,-1)", "-4"},
		{"=ISO.CEILING(-4.3)", "-4"},
		{"=FLOOR.PRECISE(3.2)", "3"},
		{"=FLOOR.PRECISE(-3.2)", "-4"},
		{"=FLOOR.PRECISE(-3.2,-1)", "-4"},
		{"=CEILING.MATH(0)", "0"},
		{"=FLOOR.MATH(5,0)", "0"},
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
}

// 조합·제곱합·진법. 값은 손으로 셀 수 있는 것으로 고른다.
func TestCombinationsSumsAndBases(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		// 되풀이를 허용한 조합은 C(n+k-1, k) 다. COMBINA(4,3)=C(6,3)=20.
		{"=COMBINA(4,3)", "20"},
		{"=COMBINA(10,3)", "220"},
		{"=FACTDOUBLE(6)", "48"},
		{"=FACTDOUBLE(7)", "105"},
		{"=FACTDOUBLE(0)", "1"},
		{"=FACTDOUBLE(-1)", "1"},
		{"=MULTINOMIAL(2,3,4)", "1260"},
		// (4-36)+(9-25)+(81-121) = -88
		{"=SUMX2MY2({2,3,9},{6,5,11})", "-88"},
		{"=SUMX2PY2({2,3,9},{6,5,11})", "276"},
		{"=SUMXMY2({2,3,9},{6,5,11})", "24"},
		// 2^1 + 2^2 + 2^3 = 14
		{"=SERIESSUM(2,1,1,{1,1,1})", "14"},
		{"=BASE(15,2)", "1111"},
		{"=BASE(15,2,10)", "0000001111"},
		{"=BASE(255,16)", "FF"},
		{`=DECIMAL("FF",16)`, "255"},
		{`=DECIMAL("111",2)`, "7"},
		{`=DECIMAL("zz",36)`, "1295"},
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
	// 짝지어 세는 함수는 두 쪽 크기가 같아야 한다.
	if result := engine.Evaluate("=SUMXMY2({1,2},{1,2,3})", map[string]any{}); result.Error == nil || result.Error.Code != "#N/A" {
		t.Errorf("크기가 다른 SUMXMY2=%#v", result)
	}
	for _, expression := range []string{"=BASE(15,1)", "=BASE(-1,2)", `=DECIMAL("2",2)`, "=FACTDOUBLE(-2)"} {
		if result := engine.Evaluate(expression, map[string]any{}); result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", expression, result.Value)
		}
	}
}

// MUNIT 은 단위 행렬을, RANDARRAY 는 지정한 크기의 난수를 펼친다.
func TestArrayProducingMathFunctions(t *testing.T) {
	t.Parallel()
	engine := New()
	unit := engine.Evaluate("=MUNIT(3)", map[string]any{})
	if unit.Error != nil {
		t.Fatalf("MUNIT -> %#v", unit.Error)
	}
	matrix, err := toArray(unit.Value)
	if err != nil || matrix.rows != 3 || matrix.columns != 3 {
		t.Fatalf("MUNIT 모양=%#v %v", matrix, err)
	}
	for row := 0; row < 3; row++ {
		for column := 0; column < 3; column++ {
			expected := "0"
			if row == column {
				expected = "1"
			}
			if actual := display(matrix.values[row*3+column]); actual != expected {
				t.Errorf("MUNIT(3) 의 %d,%d = %s, 기대=%s", row+1, column+1, actual, expected)
			}
		}
	}
	random := engine.Evaluate("=RANDARRAY(2,3,1,10,TRUE)", map[string]any{})
	if random.Error != nil {
		t.Fatalf("RANDARRAY -> %#v", random.Error)
	}
	cells, err := toArray(random.Value)
	if err != nil || cells.rows != 2 || cells.columns != 3 {
		t.Fatalf("RANDARRAY 모양=%#v %v", cells, err)
	}
	for _, cell := range cells.values {
		number, ok := toNumber(cell)
		if !ok || number < 1 || number > 10 || number != float64(int(number)) {
			t.Errorf("RANDARRAY 가 범위 밖이거나 정수가 아닌 값을 냈다: %v", cell)
		}
	}
	// 값이 매번 달라지므로 워크북은 이것을 쓰는 수식을 다시 계산해야 한다.
	if !IsVolatile("=RANDARRAY(2,2)") {
		t.Error("RANDARRAY 가 다시 계산되는 함수로 표시되지 않는다")
	}
}
