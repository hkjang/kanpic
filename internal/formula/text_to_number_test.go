package formula

import (
	"fmt"
	"math"
	"testing"
)

// 가져온 표의 금액 칸은 대개 글자다. "1,000" 이나 "$1,000" 이 든 열을 숫자로
// 바꾸려고 부르는 함수가 VALUE 인데, 예전에는 그런 글자를 하나도 읽지 못해
// 자릿점이 붙은 열은 손으로 고치는 수밖에 없었다. 셈에 끼어드는 칸의 값을
// 넓히는 것과는 다른 이야기다 — 그쪽(numberFromText)은 좁은 채로 둔다.
func TestValueReadsTheShapesPeopleTypeIntoCells(t *testing.T) {
	t.Parallel()
	cases := []struct {
		text string
		want float64
	}{
		{"1234", 1234},
		{" 1234 ", 1234},
		{"-1234.5", -1234.5},
		{"1,000", 1000},
		{"1,234,567", 1234567},
		{"1,234.50", 1234.5},
		{"$1,000", 1000},
		{"1000$", 1000},
		{"₩12,500", 12500},
		{"-$1,234.50", -1234.5},
		{"$-1,234.50", -1234.5},
		{"10%", 0.1},
		{"12.5%", 0.125},
		{"-10%", -0.1},
		{"$1,000.50", 1000.5},
		// 회계 서식은 음수를 괄호로 적는다. 내보낸 CSV 가 그 꼴이다.
		{"(1,000)", -1000},
		{"(100)", -100},
		// 시각과 날짜는 TIMEVALUE·DATEVALUE 와 같은 자로 읽는다.
		{"12:00", 0.5},
		{"16:48:00", 0.7},
		{"2026-01-05", 46027},
		{"2026-01-05 12:00:00", 46027.5},
	}
	for _, item := range cases {
		formula := fmt.Sprintf("=VALUE(%q)", item.text)
		result := New().Evaluate(formula, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		number, ok := result.Value.(float64)
		if !ok || math.Abs(number-item.want) > 1e-9 {
			t.Errorf("%s = %#v, %v 여야 한다", formula, result.Value, item.want)
		}
	}
}

// 넓게 읽더라도 수가 아닌 것은 수가 아니라고 답해야 한다. 자릿점인지 소수점인지
// 알 수 없는 "1,00" 을 골라 읽으면 열 하나가 통째로 어긋난다.
func TestValueStillRefusesWhatIsNotANumber(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "   ", "abc", "1,00", "1,2345", "12,34", "--5", "1.2.3", "$", "()", "(abc)", "%", "25:00", "2026-13-45"} {
		formula := fmt.Sprintf("=VALUE(%q)", text)
		result := New().Evaluate(formula, map[string]any{})
		if result.Error == nil || result.Error.Code != "#VALUE!" {
			t.Errorf("%s = %#v (오류 %v), #VALUE! 여야 한다", formula, result.Value, result.Error)
		}
	}
}

// 같은 글자를 두 함수가 다르게 읽으면 안 된다. VALUE 가 시각을 따로 읽으면
// =VALUE("12:00") 과 =TIMEVALUE("12:00") 이 갈린다.
func TestValueAgreesWithTimeValueAndDateValue(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, text := range []string{"00:00", "09:30", "12:00", "23:59:59"} {
		value := engine.Evaluate(fmt.Sprintf("=VALUE(%q)", text), map[string]any{})
		clock := engine.Evaluate(fmt.Sprintf("=TIMEVALUE(%q)", text), map[string]any{})
		if value.Error != nil || clock.Error != nil {
			t.Fatalf("%q: VALUE %v, TIMEVALUE %v", text, value.Error, clock.Error)
		}
		if value.Value != clock.Value {
			t.Errorf("%q: VALUE %#v, TIMEVALUE %#v", text, value.Value, clock.Value)
		}
	}
	for _, text := range []string{"1900-01-01", "1900-03-01", "2026-01-05", "9999-12-31"} {
		result := engine.Evaluate(fmt.Sprintf("=VALUE(%q)", text), map[string]any{})
		if result.Error != nil {
			t.Fatalf("%q: %v", text, result.Error)
		}
		serial, ok := result.Value.(float64)
		if !ok {
			t.Fatalf("%q: %#v", text, result.Value)
		}
		moment, ok := serialDate(serial)
		if !ok || moment.Format(dateLayout) != text {
			t.Errorf("%q 를 %v 로 읽었고 되돌리면 %v 다", text, serial, moment.Format(dateLayout))
		}
	}
}

// NUMBERVALUE 는 구분 기호를 부르는 쪽이 정하는 유일한 함수다. 유럽식으로
// 적힌 수(1.234,5)를 읽을 다른 방법이 없어서, 여태 그런 자료는 손으로 고쳤다.
func TestNumberValueFollowsTheSeparatorsItIsGiven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		formula string
		want    float64
	}{
		{`=NUMBERVALUE("1.234,5",",",".")`, 1234.5},
		{`=NUMBERVALUE("-1.234.567,25",",",".")`, -1234567.25},
		{`=NUMBERVALUE("1,234.5")`, 1234.5},
		{`=NUMBERVALUE("2.500")`, 2.5},
		{`=NUMBERVALUE("3.5")`, 3.5},
		// 빈칸은 가운데 있어도 무시한다. 자릿수를 빈칸으로 나눈 표기가 있다.
		{`=NUMBERVALUE(" 3 000 ")`, 3000},
		{`=NUMBERVALUE("2 500,5",",",".")`, 2500.5},
		// % 는 하나마다 100 으로 나눈다. 엑셀의 =9%% 와 같은 값이다.
		{`=NUMBERVALUE("9%")`, 0.09},
		{`=NUMBERVALUE("9%%")`, 0.0009},
		// 빈 글자는 0 이다. 오류가 아니다.
		{`=NUMBERVALUE("")`, 0},
		// 자릿수 구분 기호는 소수 구분 기호보다 앞에 있으면 그냥 버린다.
		{`=NUMBERVALUE("1,5.5")`, 15.5},
		// 여러 글자를 적어도 첫 글자만 쓴다.
		{`=NUMBERVALUE("1.234,5",",;",".;")`, 1234.5},
	}
	for _, item := range cases {
		result := New().Evaluate(item.formula, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s: %v", item.formula, result.Error)
			continue
		}
		number, ok := result.Value.(float64)
		if !ok || math.Abs(number-item.want) > 1e-9 {
			t.Errorf("%s = %#v, %v 여야 한다", item.formula, result.Value, item.want)
		}
	}
}

// 엑셀이 오류로 정한 자리들이다. 읽을 수 없는 것을 골라 읽으면 자릿점을
// 소수점으로 잘못 본 값이 조용히 표에 들어간다.
func TestNumberValueRefusesAmbiguousText(t *testing.T) {
	t.Parallel()
	for _, formula := range []string{
		`=NUMBERVALUE("3.5.1")`,             // 소수 구분 기호가 두 번
		`=NUMBERVALUE("1.234,5.6",",",".")`, // 소수 구분 기호 뒤의 자릿수 구분 기호
		`=NUMBERVALUE("1.5",".",".")`,       // 두 기호가 같다
		`=NUMBERVALUE("1.5","",",")`,        // 기호가 비어 있다
		`=NUMBERVALUE("abc")`,
		`=NUMBERVALUE("--5")`,
		`=NUMBERVALUE("$1,000")`, // 통화 기호까지 읽는 것은 VALUE 의 일이다
		`=NUMBERVALUE()`,
		`=NUMBERVALUE("1","2","3","4")`,
	} {
		result := New().Evaluate(formula, map[string]any{})
		if result.Error == nil {
			t.Errorf("%s = %#v, 오류여야 한다", formula, result.Value)
		}
	}
}

// 칸에 든 글자를 그대로 넣는 것이 실제로 쓰는 꼴이다. VALUE 는 칸마다 따로
// 셈하는 함수라 범위를 주면 같은 모양의 배열이 나와야 한다.
func TestValueOverAColumnOfImportedAmounts(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": "1,200", "A2": "$3,400.50", "A3": "(500)", "A4": "10%"}
	result := New().Evaluate("=SUM(VALUE(A1:A4))", cells)
	if result.Error != nil {
		t.Fatalf("%v", result.Error)
	}
	number, ok := result.Value.(float64)
	if !ok || math.Abs(number-4100.6) > 1e-9 {
		t.Fatalf("= %#v, 4100.6 이어야 한다", result.Value)
	}
}
