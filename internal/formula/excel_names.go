package formula

import (
	"strings"
	"unicode"
)

// 엑셀은 2007 이후에 생긴 함수를 파일 안에 접두사와 함께 적는다. 옛 엑셀이
// 열었을 때 모르는 이름을 사용자 함수로 오해하지 않게 하려는 표시다. 화면에
// 보이는 이름에는 없다.
//
// 읽을 때는 떼고(canonicalFunctionName), 쓸 때는 붙인다. 붙이지 않고 내보낸
// 파일은 엑셀에서 열면 IFS·XLOOKUP 같은 함수가 모두 #NAME? 이 된다.
const (
	excelModernPrefix    = "_xlfn."
	excelWorksheetPrefix = "_xlfn._xlws."
	// excelParameterPrefix 는 LAMBDA 의 매개변수와 LET 의 변수에 붙는다.
	// 까닭은 함수 쪽과 같다 — 파일 안에서 그 이름이 정의된 이름과 부딪히지
	// 않게 하는 표시다. 화면에 보이는 이름에는 없다.
	excelParameterPrefix = "_xlpm."
)

// ForExcelParameter 는 LAMBDA 매개변수 이름을 파일 안의 꼴로 바꾼다.
func ForExcelParameter(name string) string {
	if name == "" || strings.HasPrefix(strings.ToLower(name), excelParameterPrefix) {
		return name
	}
	return excelParameterPrefix + name
}

// FromExcelParameter 는 그 접두사를 뗀다. 붙은 채로 담으면 사람이 만든 적
// 없는 _xlpm.매출 이 매개변수 이름으로 화면에 보인다.
func FromExcelParameter(name string) string {
	if len(name) > len(excelParameterPrefix) && strings.EqualFold(name[:len(excelParameterPrefix)], excelParameterPrefix) {
		return name[len(excelParameterPrefix):]
	}
	return name
}

// RewriteBareNames 는 수식에서 함수가 아닌 이름 자리만 찾아 바꾼다. 함수
// 이름을 바꾸는 것과 같은 훑기를 쓴다 — 문자열을 건너뛰는 규칙을 두 번 적으면
// 한쪽만 고치게 된다. LAMBDA 본문 안의 매개변수가 이 자리다.
func RewriteBareNames(text string, rename func(string) string) string {
	if text == "" {
		return text
	}
	return rewriteNames(text, func(word string, isCall bool) string {
		if isCall {
			return word
		}
		return rename(word)
	})
}

// excelPrefixedFunctions 는 접두사가 필요한 함수와 그 접두사다. 동적 배열
// 함수 중 워크시트 전용으로 분류된 둘은 접두사가 더 길다.
//
// 여기에 없는 이름은 그대로 나간다. 잘못 넣으면 멀쩡히 돌던 수식이 엑셀에서
// 깨지므로, 확실한 것만 적는다. 빠뜨린 것은 지금까지와 같을 뿐이다.
var excelPrefixedFunctions = map[string]string{
	"STDEV.P": excelModernPrefix, "STDEV.S": excelModernPrefix,
	"VAR.P": excelModernPrefix, "VAR.S": excelModernPrefix,
	"MODE.SNGL": excelModernPrefix, "RANK.EQ": excelModernPrefix, "RANK.AVG": excelModernPrefix,
	"PERCENTILE.INC": excelModernPrefix, "PERCENTILE.EXC": excelModernPrefix,
	"QUARTILE.INC": excelModernPrefix, "QUARTILE.EXC": excelModernPrefix,
	"COVARIANCE.P": excelModernPrefix, "FORECAST.LINEAR": excelModernPrefix,
	"IFS": excelModernPrefix, "SWITCH": excelModernPrefix, "IFNA": excelModernPrefix,
	"TEXTJOIN": excelModernPrefix, "CONCAT": excelModernPrefix,
	"MAXIFS": excelModernPrefix, "MINIFS": excelModernPrefix,
	"XLOOKUP": excelModernPrefix, "XMATCH": excelModernPrefix,
	"UNIQUE": excelModernPrefix, "SEQUENCE": excelModernPrefix, "SORTBY": excelModernPrefix,
	"LET": excelModernPrefix, "LAMBDA": excelModernPrefix, "ISOMITTED": excelModernPrefix,
	"BYROW": excelModernPrefix, "BYCOL": excelModernPrefix, "MAP": excelModernPrefix,
	"REDUCE": excelModernPrefix, "SCAN": excelModernPrefix,
	"TEXTBEFORE": excelModernPrefix, "TEXTAFTER": excelModernPrefix, "TEXTSPLIT": excelModernPrefix,
	"VSTACK": excelModernPrefix, "HSTACK": excelModernPrefix,
	"TOROW": excelModernPrefix, "TOCOL": excelModernPrefix,
	"CHOOSEROWS": excelModernPrefix, "CHOOSECOLS": excelModernPrefix,
	"WRAPROWS": excelModernPrefix, "WRAPCOLS": excelModernPrefix, "EXPAND": excelModernPrefix,
	"ISFORMULA": excelModernPrefix, "DAYS": excelModernPrefix, "ISOWEEKNUM": excelModernPrefix,
	"UNICHAR": excelModernPrefix, "UNICODE": excelModernPrefix,
	"PDURATION": excelModernPrefix, "RRI": excelModernPrefix,
	"FILTER": excelWorksheetPrefix, "SORT": excelWorksheetPrefix,
}

// ForExcel 은 수식을 엑셀이 파일에서 기대하는 모양으로 바꾼다. 문자열 안은
// 건드리지 않는다 — "IFS(" 라고 적힌 글자까지 함수로 보면 사람이 쓴 글이
// 망가진다.
func ForExcel(text string) string {
	if text == "" {
		return text
	}
	return rewriteFunctionNames(text, excelFunctionName)
}

// rewriteFunctionNames 는 수식에서 함수 이름 자리만 찾아 바꾼다. 두 방향이
// 같은 훑기를 쓴다 — 문자열을 건너뛰는 규칙을 두 번 적으면 한쪽만 고치게 된다.
func rewriteFunctionNames(text string, rename func(string) string) string {
	return rewriteNames(text, func(word string, isCall bool) string {
		if !isCall {
			return word
		}
		return rename(word)
	})
}

// rewriteNames 는 그 훑기 자체다. 이름을 만나면 뒤에 여는 괄호가 오는지를
// 함께 넘겨준다 — 함수 이름인지 그냥 이름인지는 그것으로 갈린다.
func rewriteNames(text string, rename func(word string, isCall bool) string) string {
	var out strings.Builder
	out.Grow(len(text) + 16)
	runes := []rune(text)
	for index := 0; index < len(runes); {
		switch {
		case runes[index] == '\'':
			// 작은따옴표 안은 시트 이름이다. 매개변수와 같은 이름의 시트가
			// 있다고 그 자리를 바꾸면 가리키는 곳이 달라진다.
			out.WriteRune(runes[index])
			index++
			for index < len(runes) {
				out.WriteRune(runes[index])
				index++
				if runes[index-1] == '\'' {
					break
				}
			}
		case runes[index] == '"':
			// 엑셀 수식에서 따옴표 안의 따옴표는 두 번 적는다.
			out.WriteRune(runes[index])
			index++
			for index < len(runes) {
				out.WriteRune(runes[index])
				if runes[index] == '"' {
					index++
					if index < len(runes) && runes[index] == '"' {
						out.WriteRune(runes[index])
						index++
						continue
					}
					break
				}
				index++
			}
		case isNameRune(runes[index]):
			start := index
			for index < len(runes) && isNameRune(runes[index]) {
				index++
			}
			word := string(runes[start:index])
			// 이름 바로 뒤에 여는 괄호가 와야 함수다. 사이의 공백은 엑셀도
			// 허용한다.
			lookahead := index
			for lookahead < len(runes) && (runes[lookahead] == ' ' || runes[lookahead] == '\t') {
				lookahead++
			}
			out.WriteString(rename(word, lookahead < len(runes) && runes[lookahead] == '('))
		default:
			out.WriteRune(runes[index])
			index++
		}
	}
	return out.String()
}

// FromExcel 은 파일에서 읽은 수식을 사람이 보는 이름으로 되돌린다. 엔진은
// 접두사가 붙어 있어도 셀 줄 알지만, 저장된 글자에 남아 있으면 수식 입력줄에
// `=_xlfn.IFS(...)` 라고 보인다. 사람이 쓴 이름이 아니다.
func FromExcel(text string) string {
	if text == "" {
		return text
	}
	return rewriteFunctionNames(text, plainFunctionName)
}

func plainFunctionName(word string) string {
	upper := strings.ToUpper(word)
	for _, prefix := range xlsxFunctionPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return word[len(prefix):]
		}
	}
	return word
}

func excelFunctionName(word string) string {
	upper := strings.ToUpper(word)
	for _, prefix := range xlsxFunctionPrefixes {
		if strings.HasPrefix(upper, prefix) {
			// 이미 붙어 있으면 그대로 둔다.
			return word
		}
	}
	if prefix, needed := excelPrefixedFunctions[upper]; needed {
		return prefix + word
	}
	return word
}

// isNameRune 은 이름을 이루는 글자인지 본다. 한글도 이름이다 — 함수 이름은
// 모두 영문이라 여태 문제가 없었지만, LAMBDA 의 매개변수는 사람이 짓는
// 이름이라 이 나라에서는 대개 한글이다. 매출 을 이름으로 못 읽으면 본문에서
// 그 자리를 찾지 못해, 선언만 바뀌고 부르는 자리는 그대로 남는다.
func isNameRune(value rune) bool {
	return value == '.' || value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

// ReferencesName 은 수식이 그 이름을 부르는지 본다. 매출표 와 매출표[금액]
// 둘 다 부르는 것으로 센다.
//
// 함수 이름을 바꾸는 것과 같은 훑기를 쓴다. 문자열 안에 적힌 글자는 이름이
// 아니다 — "매출표는 늘었다" 라고 적은 메모까지 표를 부르는 것으로 세면,
// 사람이 적은 글 때문에 표가 자라지 않게 된다.
func ReferencesName(text, name string) bool {
	if text == "" || name == "" {
		return false
	}
	target := strings.ToUpper(strings.TrimSpace(name))
	found := false
	rewriteNames(text, func(word string, _ bool) string {
		upper := strings.ToUpper(word)
		if upper == target || strings.HasPrefix(upper, target+"[") {
			found = true
		}
		return word
	})
	return found
}
