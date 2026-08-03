package formula

import (
	"math"
	"strings"
	"time"
)

// FunctionDoc describes one supported function so the product can list what the
// engine understands instead of leaving people to discover #NAME? by trial.
type FunctionDoc struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Syntax   string `json:"syntax"`
	Summary  string `json:"summary"`
}

var catalog = []FunctionDoc{
	{"SUM", "수학", "SUM(값1, 값2, …)", "숫자와 범위의 합계를 구합니다."},
	{"AVERAGE", "수학", "AVERAGE(값1, 값2, …)", "숫자의 평균을 구합니다."},
	{"MIN", "수학", "MIN(값1, 값2, …)", "가장 작은 숫자를 반환합니다."},
	{"MAX", "수학", "MAX(값1, 값2, …)", "가장 큰 숫자를 반환합니다."},
	{"MEDIAN", "수학", "MEDIAN(값1, 값2, …)", "숫자의 중앙값을 구합니다."},
	{"PRODUCT", "수학", "PRODUCT(값1, 값2, …)", "숫자를 모두 곱합니다."},
	{"ROUND", "수학", "ROUND(숫자, [자릿수])", "지정한 자릿수로 반올림합니다."},
	{"ROUNDUP", "수학", "ROUNDUP(숫자, [자릿수])", "지정한 자릿수로 올림합니다."},
	{"ROUNDDOWN", "수학", "ROUNDDOWN(숫자, [자릿수])", "지정한 자릿수로 내림합니다."},
	{"ABS", "수학", "ABS(숫자)", "절댓값을 반환합니다."},
	{"INT", "수학", "INT(숫자)", "가장 가까운 작은 정수로 내립니다."},
	{"MOD", "수학", "MOD(숫자, 나눌 수)", "나눗셈의 나머지를 반환합니다."},
	{"POWER", "수학", "POWER(밑, 지수)", "거듭제곱을 계산합니다."},
	{"SQRT", "수학", "SQRT(숫자)", "제곱근을 반환합니다."},
	{"COUNT", "집계", "COUNT(값1, 값2, …)", "숫자가 들어 있는 셀의 개수를 셉니다."},
	{"COUNTA", "집계", "COUNTA(값1, 값2, …)", "비어 있지 않은 셀의 개수를 셉니다."},
	{"COUNTBLANK", "집계", "COUNTBLANK(범위)", "비어 있는 셀의 개수를 셉니다."},
	{"SUMIF", "집계", "SUMIF(범위, 조건, [합계 범위])", "조건을 만족하는 값의 합계를 구합니다."},
	{"SUMIFS", "집계", "SUMIFS(합계 범위, 범위1, 조건1, …)", "여러 조건을 만족하는 값의 합계를 구합니다."},
	{"COUNTIF", "집계", "COUNTIF(범위, 조건)", "조건을 만족하는 셀의 개수를 셉니다."},
	{"COUNTIFS", "집계", "COUNTIFS(범위1, 조건1, …)", "여러 조건을 만족하는 셀의 개수를 셉니다."},
	{"IF", "논리", "IF(조건, 참일 때, [거짓일 때])", "조건에 따라 다른 값을 반환합니다."},
	{"IFERROR", "논리", "IFERROR(값, 오류일 때)", "값이 오류이면 대체 값을 반환합니다."},
	{"AND", "논리", "AND(조건1, 조건2, …)", "모든 조건이 참이면 TRUE입니다."},
	{"OR", "논리", "OR(조건1, 조건2, …)", "조건 중 하나라도 참이면 TRUE입니다."},
	{"NOT", "논리", "NOT(조건)", "참과 거짓을 뒤집습니다."},
	{"CONCAT", "텍스트", "CONCAT(값1, 값2, …)", "텍스트를 이어 붙입니다."},
	{"TEXTJOIN", "텍스트", "TEXTJOIN(구분자, 빈 값 무시, 값1, …)", "구분자를 넣어 텍스트를 이어 붙입니다."},
	{"LEFT", "텍스트", "LEFT(텍스트, [개수])", "왼쪽에서 지정한 개수만큼 잘라냅니다."},
	{"RIGHT", "텍스트", "RIGHT(텍스트, [개수])", "오른쪽에서 지정한 개수만큼 잘라냅니다."},
	{"MID", "텍스트", "MID(텍스트, 시작 위치, 개수)", "가운데 일부를 잘라냅니다."},
	{"LEN", "텍스트", "LEN(텍스트)", "글자 수를 셉니다."},
	{"TRIM", "텍스트", "TRIM(텍스트)", "앞뒤 공백과 중복 공백을 제거합니다."},
	{"UPPER", "텍스트", "UPPER(텍스트)", "영문을 대문자로 바꿉니다."},
	{"LOWER", "텍스트", "LOWER(텍스트)", "영문을 소문자로 바꿉니다."},
	{"PROPER", "텍스트", "PROPER(텍스트)", "각 단어의 첫 글자를 대문자로 바꿉니다."},
	{"SUBSTITUTE", "텍스트", "SUBSTITUTE(텍스트, 찾을 값, 바꿀 값)", "텍스트의 일부를 바꿉니다."},
	{"FIND", "텍스트", "FIND(찾을 값, 텍스트)", "대소문자를 구분해 위치를 찾습니다."},
	{"SEARCH", "텍스트", "SEARCH(찾을 값, 텍스트)", "대소문자를 구분하지 않고 위치를 찾습니다."},
	{"REPT", "텍스트", "REPT(텍스트, 횟수)", "텍스트를 지정한 횟수만큼 반복합니다."},
	{"VALUE", "텍스트", "VALUE(텍스트)", "숫자 형태의 텍스트를 숫자로 바꿉니다."},
	{"HYPERLINK", "텍스트", "HYPERLINK(주소, [표시할 텍스트])", "링크 주소와 표시 텍스트를 만듭니다."},
	{"DATE", "날짜", "DATE(연, 월, 일)", "연·월·일로 날짜를 만듭니다."},
	{"TODAY", "날짜", "TODAY()", "오늘 날짜를 반환합니다."},
	{"NOW", "날짜", "NOW()", "현재 날짜와 시간을 반환합니다."},
	{"YEAR", "날짜", "YEAR(날짜)", "날짜에서 연도를 꺼냅니다."},
	{"MONTH", "날짜", "MONTH(날짜)", "날짜에서 월을 꺼냅니다."},
	{"DAY", "날짜", "DAY(날짜)", "날짜에서 일을 꺼냅니다."},
	{"WEEKDAY", "날짜", "WEEKDAY(날짜)", "요일 번호를 반환합니다. 일요일이 1입니다."},
	{"VLOOKUP", "조회", "VLOOKUP(찾을 값, 범위, 열 번호, [정렬 여부])", "세로 방향으로 값을 찾습니다."},
	{"HLOOKUP", "조회", "HLOOKUP(찾을 값, 범위, 행 번호, [정렬 여부])", "가로 방향으로 값을 찾습니다."},
	{"INDEX", "조회", "INDEX(범위, 행, [열])", "범위에서 위치로 값을 꺼냅니다."},
	{"MATCH", "조회", "MATCH(찾을 값, 범위, [유형])", "값이 있는 위치를 반환합니다."},
	{"FILTER", "배열", "FILTER(범위, 조건1, …)", "조건을 만족하는 행만 남깁니다."},
	{"SORT", "배열", "SORT(범위, [정렬 열], [오름차순])", "범위를 정렬한 결과를 반환합니다."},
}

// Catalog lists every function the evaluator understands, in menu order.
func Catalog() []FunctionDoc { return append([]FunctionDoc(nil), catalog...) }

var now = time.Now

// evaluateLibrary handles the functions whose arguments are ordinary flattened
// values. It reports false when the name belongs to no known function so the
// caller can raise #NAME? once, in one place.
func evaluateLibrary(name string, values []any) (any, bool, error) {
	switch name {
	case "COUNTA":
		count := 0
		for _, value := range values {
			if value != nil && display(value) != "" {
				count++
			}
		}
		return float64(count), true, nil
	case "COUNTBLANK":
		count := 0
		for _, value := range values {
			if value == nil || display(value) == "" {
				count++
			}
		}
		return float64(count), true, nil
	case "MEDIAN":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return nil, true, formulaError("#NUM!", "MEDIAN requires at least one number")
		}
		sorted := append([]float64(nil), numbers...)
		for index := 1; index < len(sorted); index++ {
			for position := index; position > 0 && sorted[position] < sorted[position-1]; position-- {
				sorted[position], sorted[position-1] = sorted[position-1], sorted[position]
			}
		}
		middle := len(sorted) / 2
		if len(sorted)%2 == 1 {
			return sorted[middle], true, nil
		}
		return (sorted[middle-1] + sorted[middle]) / 2, true, nil
	case "PRODUCT":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return float64(0), true, nil
		}
		result := 1.0
		for _, number := range numbers {
			result *= number
		}
		return result, true, nil
	case "ROUNDUP", "ROUNDDOWN":
		number, digits, err := roundingArguments(name, values)
		if err != nil {
			return nil, true, err
		}
		factor := math.Pow(10, digits)
		if name == "ROUNDUP" {
			return math.Ceil(math.Abs(number)*factor) / factor * sign(number), true, nil
		}
		return math.Floor(math.Abs(number)*factor) / factor * sign(number), true, nil
	case "ABS", "INT", "SQRT":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		switch name {
		case "ABS":
			return math.Abs(number), true, nil
		case "INT":
			return math.Floor(number), true, nil
		}
		if number < 0 {
			return nil, true, formulaError("#NUM!", "SQRT requires a value of zero or more")
		}
		return math.Sqrt(number), true, nil
	case "MOD", "POWER":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		left, leftOK := toNumber(values[0])
		right, rightOK := toNumber(values[1])
		if !leftOK || !rightOK {
			return nil, true, formulaError("#VALUE!", name+" requires numbers")
		}
		if name == "MOD" {
			if right == 0 {
				return nil, true, formulaError("#DIV/0!", "MOD cannot divide by zero")
			}
			return math.Mod(math.Mod(left, right)+right, right), true, nil
		}
		return math.Pow(left, right), true, nil
	case "NOT":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return !truthy(values[0]), true, nil
	case "LEN":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return float64(len([]rune(display(values[0])))), true, nil
	case "TRIM":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return strings.Join(strings.Fields(display(values[0])), " "), true, nil
	case "UPPER", "LOWER", "PROPER":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		text := display(values[0])
		switch name {
		case "UPPER":
			return strings.ToUpper(text), true, nil
		case "LOWER":
			return strings.ToLower(text), true, nil
		}
		return properCase(text), true, nil
	case "SUBSTITUTE":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		return strings.ReplaceAll(display(values[0]), display(values[1]), display(values[2])), true, nil
	case "FIND", "SEARCH":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		needle, haystack := display(values[0]), display(values[1])
		if name == "SEARCH" {
			needle, haystack = strings.ToLower(needle), strings.ToLower(haystack)
		}
		index := strings.Index(haystack, needle)
		if index < 0 {
			return nil, true, formulaError("#VALUE!", name+" did not find the text")
		}
		return float64(len([]rune(haystack[:index])) + 1), true, nil
	case "REPT":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		count, ok := toNumber(values[1])
		if !ok || count < 0 || count > 10_000 {
			return nil, true, formulaError("#VALUE!", "REPT requires a repeat count between 0 and 10000")
		}
		return strings.Repeat(display(values[0]), int(count)), true, nil
	case "TEXTJOIN":
		if len(values) < 3 {
			return nil, true, argError(name)
		}
		separator, skipEmpty := display(values[0]), truthy(values[1])
		parts := make([]string, 0, len(values)-2)
		for _, value := range values[2:] {
			text := display(value)
			if skipEmpty && text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, separator), true, nil
	case "VALUE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "VALUE requires a number in text form")
		}
		return number, true, nil
	case "HYPERLINK":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		if len(values) == 2 && display(values[1]) != "" {
			return display(values[1]), true, nil
		}
		return display(values[0]), true, nil
	case "TODAY":
		if len(values) != 0 {
			return nil, true, argError(name)
		}
		return now().Format("2006-01-02"), true, nil
	case "NOW":
		if len(values) != 0 {
			return nil, true, argError(name)
		}
		return now().Format("2006-01-02 15:04:05"), true, nil
	case "YEAR", "MONTH", "DAY", "WEEKDAY":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		moment, ok := parseDate(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a date")
		}
		switch name {
		case "YEAR":
			return float64(moment.Year()), true, nil
		case "MONTH":
			return float64(int(moment.Month())), true, nil
		case "DAY":
			return float64(moment.Day()), true, nil
		}
		return float64(int(moment.Weekday()) + 1), true, nil
	}
	return nil, false, nil
}

func sign(number float64) float64 {
	if number < 0 {
		return -1
	}
	return 1
}

func roundingArguments(name string, values []any) (float64, float64, error) {
	if len(values) < 1 || len(values) > 2 {
		return 0, 0, argError(name)
	}
	number, ok := toNumber(values[0])
	if !ok {
		return 0, 0, formulaError("#VALUE!", name+" requires a number")
	}
	digits := 0.0
	if len(values) == 2 {
		digits, _ = toNumber(values[1])
	}
	return number, digits, nil
}

func properCase(text string) string {
	var builder strings.Builder
	start := true
	for _, letter := range text {
		if start {
			builder.WriteString(strings.ToUpper(string(letter)))
		} else {
			builder.WriteString(strings.ToLower(string(letter)))
		}
		start = letter == ' ' || letter == '\t' || letter == '-' || letter == '_'
	}
	return builder.String()
}

// Dates are stored as the text DATE produces, so both the plain date and the
// date with a time component are accepted.
func parseDate(value any) (time.Time, bool) {
	text := strings.TrimSpace(display(value))
	if text == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339, "2006/01/02"} {
		if moment, err := time.Parse(layout, text); err == nil {
			return moment, true
		}
	}
	return time.Time{}, false
}
