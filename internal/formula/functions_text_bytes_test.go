package formula

import "testing"

// 한글과 한자는 엑셀이 두 바이트로 센다. 그래서 LEN 과 LENB 의 답이 다르고,
// 자리를 바이트로 세는 함수들이 따로 있다.
func TestByteLengthCountsWideCharactersAsTwo(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{`=LEN("한글")`, "2"},
		{`=LENB("한글")`, "4"},
		{`=LENB("한a글")`, "5"},
		{`=LENB("abc")`, "3"},
		{`=LENB("")`, "0"},
		// 두 바이트짜리 글자의 가운데가 잘리면 그 반쪽을 공백 하나로 바꾼다.
		// 반쪽 글자를 낼 수는 없기 때문이다.
		{`=LEFTB("한글",1)`, " "},
		{`=LEFTB("한글",2)`, "한"},
		{`=LEFTB("한글",3)`, "한 "},
		{`=LEFTB("한글",4)`, "한글"},
		{`=RIGHTB("한글",1)`, " "},
		{`=RIGHTB("한글",2)`, "글"},
		{`=RIGHTB("한글",3)`, " 글"},
		{`=MIDB("한국어",3,2)`, "국"},
		// 양쪽 반쪽만 걸리면 공백 둘이다.
		{`=MIDB("한국어",2,2)`, "  "},
		{`=MIDB("a한b",2,2)`, "한"},
		// 찾은 자리도 바이트로 센다. a=1, 한=2·3, b=4 다.
		{`=FINDB("글","한글")`, "3"},
		{`=FINDB("b","a한b")`, "4"},
		{`=SEARCHB("B","a한b")`, "4"},
		{`=REPLACEB("한국어",3,2,"民")`, "한民어"},
		{`=REPLACEB("abcdef",3,2,"XY")`, "abXYef"},
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
	if result := engine.Evaluate(`=FINDB("없음","한글")`, map[string]any{}); result.Error == nil {
		t.Errorf("없는 글자를 찾았다: %v", result.Value)
	}
}

// 붙여넣은 글에 전각 숫자가 섞이면 숫자로 읽히지 않는다. 고칠 자리가 필요하다.
func TestFullWidthConversion(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{`=ASC("ＡＢＣ１２３")`, "ABC123"},
		{`=ASC("ａ　ｂ")`, "a b"},
		{`=ASC("한글")`, "한글"},
		{`=JIS("ABC")`, "ＡＢＣ"},
		{`=LEN(JIS("ABC"))`, "3"},
		// 전각은 두 바이트로 센다.
		{`=LENB(JIS("ABC"))`, "6"},
		{`=ASC(JIS("Hello 123"))`, "Hello 123"},
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
}

// 로마 숫자는 얼마나 짧게 적을지 고를 수 있다. 499 의 다섯 가지 꼴은
// 엑셀 문서에 그대로 적혀 있어 정의를 확인하기 좋다.
func TestRomanNumeralForms(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		{`=ROMAN(499)`, "CDXCIX"},
		{`=ROMAN(499,0)`, "CDXCIX"},
		{`=ROMAN(499,1)`, "LDVLIV"},
		{`=ROMAN(499,2)`, "XDIX"},
		{`=ROMAN(499,3)`, "VDIV"},
		{`=ROMAN(499,4)`, "ID"},
		{`=ROMAN(1)`, "I"},
		{`=ROMAN(4)`, "IV"},
		{`=ROMAN(9)`, "IX"},
		{`=ROMAN(1999)`, "MCMXCIX"},
		{`=ROMAN(2024)`, "MMXXIV"},
		{`=ROMAN(3999)`, "MMMCMXCIX"},
		{`=ROMAN(0)`, ""},
		{`=ARABIC("MCMXCIX")`, "1999"},
		{`=ARABIC("IV")`, "4"},
		// 짧게 적은 꼴도 되읽는다.
		{`=ARABIC("ID")`, "499"},
		{`=ARABIC("-IV")`, "-4"},
		{`=ARABIC(ROMAN(2024))`, "2024"},
		{`=ARABIC("")`, "0"},
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
	for _, expression := range []string{`=ROMAN(4000)`, `=ROMAN(-1)`, `=ARABIC("한글")`, `=ROMAN(10,5)`} {
		if result := engine.Evaluate(expression, map[string]any{}); result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", expression, result.Value)
		}
	}
}

// 주말이 토·일이 아닌 곳도 있다. 숫자로 고르거나 월요일부터 일곱 글자로
// 적는다.
func TestInternationalWorkdays(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, expected string }{
		// 2018년 1월은 토요일 넷, 일요일 넷이라 평일이 23일이다.
		{`=NETWORKDAYS.INTL("2018-01-01","2018-01-31")`, "23"},
		{`=NETWORKDAYS.INTL("2018-01-01","2018-01-31",11)`, "27"},
		{`=NETWORKDAYS.INTL("2018-01-01","2018-01-31","0000011")`, "23"},
		{`=NETWORKDAYS.INTL("2018-01-01","2018-01-31",1,{"2018-01-15"})`, "22"},
		{`=NETWORKDAYS.INTL("2018-01-31","2018-01-01")`, "-23"},
		{`=WORKDAY.INTL("2018-01-01",5)`, "2018-01-08"},
		{`=WORKDAY.INTL("2018-01-01",5,11)`, "2018-01-06"},
		{`=WORKDAY.INTL("2018-01-08",-5)`, "2018-01-01"},
		{`=EPOCHTODATE(1517299200)`, "2018-01-30 08:00:00"},
		{`=EPOCHTODATE(1517299200000,2)`, "2018-01-30 08:00:00"},
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
	for _, expression := range []string{
		`=NETWORKDAYS.INTL("2018-01-01","2018-01-31","1111111")`,
		`=NETWORKDAYS.INTL("2018-01-01","2018-01-31",8)`,
		`=NETWORKDAYS.INTL("2018-01-01","2018-01-31","000011")`,
		`=EPOCHTODATE(1517299200,4)`,
	} {
		if result := engine.Evaluate(expression, map[string]any{}); result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", expression, result.Value)
		}
	}
}
