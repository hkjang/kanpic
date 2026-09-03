package formula

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// 글자로 담긴 수를 읽는 자리는 두 곳이고, 둘은 서로 다른 일을 한다.
//
// 하나는 셈에 끼어드는 칸의 값이다(numberFromText). 그쪽은 좁아야 한다 —
// "1,000" 이라고 적힌 칸을 =SUM 이 제멋대로 1000 으로 세면, 자릿점을 쓰는
// 나라에서는 맞고 소수점으로 쓰는 나라에서는 틀린 값이 조용히 합계에 들어간다.
//
// 다른 하나가 여기다. VALUE 와 NUMBERVALUE 는 **읽어 달라고 부르는** 함수라
// 넓어야 한다. 사람이 "이 글자를 수로 바꿔라" 라고 적었는데 #VALUE! 이 나오면
// 달리 쓸 함수가 없다. 엑셀·시트도 이 둘만 넓게 읽는다.

// currencySigns 는 값의 앞이나 뒤에 붙어 있어도 수로 읽는 기호다. 가져온 표의
// 금액 칸이 대개 이 꼴이다. 확실한 것만 적는다 — 여기 없는 기호가 붙은 글자는
// 지금까지와 같이 #VALUE! 일 뿐이다.
var currencySigns = []string{"$", "₩", "€", "£", "¥", "¢", "₹", "₽"}

// groupedNumberText 는 자릿점이 든 수의 모양이다. 자릿점 뒤는 반드시 세 자리라
// "1,00" 이나 "1,2345" 는 수로 보지 않는다. 자릿점인지 소수점인지 알 수 없는
// 글자를 골라 읽으면 열 전체가 1000배 어긋나므로, 모양이 확실할 때만 센다.
var groupedNumberText = regexp.MustCompile(`^\d{1,3}(,\d{3})+(\.\d*)?$`)

// valueOfText 는 VALUE 가 읽는 범위다. 수·백분율·통화·자릿점·시각·날짜를
// 모두 받는다. 시각과 날짜는 TIMEVALUE·DATEVALUE 와 **같은 자**로 읽는다 —
// 따로 읽으면 =VALUE("12:00") 과 =TIMEVALUE("12:00") 이 갈린다.
func valueOfText(value any) (float64, bool) {
	if number, ok := toNumber(value); ok {
		return number, true
	}
	text := strings.TrimSpace(display(value))
	if text == "" {
		return 0, false
	}
	if number, ok := textValueNumber(text); ok {
		return number, true
	}
	// 날짜를 먼저 본다. "2026-01-05 15:04:05" 는 시각처럼 보이기도 하지만
	// 날짜다. 엑셀과 마찬가지로 날 수를 낸다 — 하루 안의 자리는 소수다.
	if serial, ok := DateSerial(text); ok {
		return serial, true
	}
	if moment, ok := parseTime(text); ok && strings.Contains(text, ":") {
		return float64(moment.Hour()*3600+moment.Minute()*60+moment.Second()) / 86400, true
	}
	return 0, false
}

// textValueNumber 는 사람이 칸에 적는 수의 겉모양을 하나씩 벗겨 낸다.
//
//	(1,000)   회계 서식의 괄호는 음수다
//	-$1,000   부호는 통화 기호의 앞에도 뒤에도 온다
//	12%       뒤에 붙은 % 하나마다 100 으로 나눈다
//
// 벗겨 낸 뒤에 남은 것이 수의 모양이 아니면 읽지 않는다.
func textValueNumber(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	negative := false
	if strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		negative, trimmed = true, strings.TrimSpace(trimmed[1:len(trimmed)-1])
	}
	scale := 1.0
	for strings.HasSuffix(trimmed, "%") {
		scale *= 100
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "%"))
	}
	// 부호는 한 번만 받는다. "--5" 는 수가 아니다.
	signed := false
	trimmed, negative, signed = takeSign(trimmed, negative, signed)
	for _, symbol := range currencySigns {
		if after, found := strings.CutPrefix(trimmed, symbol); found {
			trimmed = strings.TrimSpace(after)
			break
		}
	}
	trimmed, negative, signed = takeSign(trimmed, negative, signed)
	for _, symbol := range currencySigns {
		if before, found := strings.CutSuffix(trimmed, symbol); found {
			trimmed = strings.TrimSpace(before)
			break
		}
	}
	if trimmed == "" {
		return 0, false
	}
	// 부호를 이미 뗐는데 또 남아 있으면 수가 아니다. ParseFloat 은 "-5" 를
	// 받으므로, 걸러 내지 않으면 "--5" 가 5 가 된다.
	if signed && (trimmed[0] == '-' || trimmed[0] == '+') {
		return 0, false
	}
	if groupedNumberText.MatchString(trimmed) {
		trimmed = strings.ReplaceAll(trimmed, ",", "")
	} else if !decimalText.MatchString(trimmed) {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.TrimPrefix(trimmed, "+"), 64)
	if err != nil {
		return 0, false
	}
	if negative {
		number = -number
	}
	return number / scale, true
}

// takeSign 은 맨 앞의 부호를 한 번만 떼어 낸다. 이미 뗀 뒤라면 그대로 둔다.
func takeSign(text string, negative, taken bool) (string, bool, bool) {
	if taken || text == "" {
		return text, negative, taken
	}
	switch text[0] {
	case '-':
		return strings.TrimSpace(text[1:]), !negative, true
	case '+':
		return strings.TrimSpace(text[1:]), negative, true
	}
	return text, negative, taken
}

// numberValueOfText 는 NUMBERVALUE 다. 구분 기호를 부르는 쪽이 정하므로,
// 나라마다 다른 꼴로 적힌 수를 읽을 수 있는 유일한 함수다.
//
//	=NUMBERVALUE("1.234,5", ",", ".")   1234.5  (독일식)
//	=NUMBERVALUE("2 500")               2500    (가운데 빈칸도 무시한다)
//	=NUMBERVALUE("9%%")                 0.0009  (% 하나마다 100 으로 나눈다)
//
// 엑셀 도움말이 정한 규칙을 그대로 따른다 — 소수 구분 기호가 두 번 나오면
// 오류, 자릿수 구분 기호가 소수 구분 기호보다 뒤에 나오면 오류, 앞에 나오면
// 그냥 버린다. 두 기호가 같아도 오류다.
func numberValueOfText(text, decimal, group string) (float64, bool) {
	if decimal == "" || group == "" || decimal == group {
		return 0, false
	}
	// 빈칸은 가운데 있어도 무시한다. 엑셀이 그렇게 정했고, 자릿수를 빈칸으로
	// 나눈 유럽식 표기가 실제로 그 꼴로 들어온다.
	packed := strings.Map(func(letter rune) rune {
		if unicode.IsSpace(letter) {
			return -1
		}
		return letter
	}, text)
	if packed == "" {
		return 0, true
	}
	scale := 1.0
	for strings.HasSuffix(packed, "%") {
		scale *= 100
		packed = strings.TrimSuffix(packed, "%")
	}
	whole, fraction, split := strings.Cut(packed, decimal)
	if split && strings.Contains(fraction, decimal) {
		return 0, false
	}
	if strings.Contains(fraction, group) {
		return 0, false
	}
	whole = strings.ReplaceAll(whole, group, "")
	if split {
		whole += "." + fraction
	}
	if !decimalText.MatchString(whole) {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.TrimPrefix(whole, "+"), 64)
	if err != nil {
		return 0, false
	}
	return number / scale, true
}

// separatorArgument 는 구분 기호 인수를 읽는다. 엑셀은 여러 글자를 적어도 첫
// 글자만 쓴다.
func separatorArgument(value any, fallback string) string {
	if omitted(value) {
		return fallback
	}
	text := display(value)
	if text == "" {
		return ""
	}
	return string([]rune(text)[0])
}
