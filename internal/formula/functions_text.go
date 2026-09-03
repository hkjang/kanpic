package formula

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// evaluateText covers the string functions whose arguments are plain values.
func evaluateText(name string, values []any) (any, bool, error) {
	switch name {
	case "TEXT":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		return formatValue(values[0], display(values[1])), true, nil
	case "NUMBERVALUE":
		if len(values) < 1 || len(values) > 3 {
			return nil, true, argError(name)
		}
		decimal, group := ".", ","
		if len(values) >= 2 {
			decimal = separatorArgument(values[1], decimal)
		}
		if len(values) >= 3 {
			group = separatorArgument(values[2], group)
		}
		number, ok := numberValueOfText(display(values[0]), decimal, group)
		if !ok {
			return nil, true, formulaError("#VALUE!", "NUMBERVALUE requires a number in text form")
		}
		return number, true, nil
	case "TO_TEXT", "T":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		if name == "T" {
			if _, isText := values[0].(string); !isText {
				return "", true, nil
			}
		}
		return display(values[0]), true, nil
	case "REPLACE":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		text := []rune(display(values[0]))
		start, err := integerValue(values[1], name)
		if err != nil {
			return nil, true, err
		}
		count, err := integerValue(values[2], name)
		if err != nil {
			return nil, true, err
		}
		if start < 1 || count < 0 {
			return nil, true, formulaError("#VALUE!", "REPLACE position must be positive")
		}
		from := min(start-1, len(text))
		to := min(from+count, len(text))
		return string(text[:from]) + display(values[3]) + string(text[to:]), true, nil
	case "EXACT":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		return display(values[0]) == display(values[1]), true, nil
	case "CHAR", "UNICHAR":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		code, err := integerValue(values[0], name)
		if err != nil {
			return nil, true, err
		}
		if code < 1 || code > 1114111 {
			return nil, true, formulaError("#NUM!", name+" needs a character code")
		}
		return string(rune(code)), true, nil
	case "CODE", "UNICODE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		text := []rune(display(values[0]))
		if len(text) == 0 {
			return nil, true, formulaError("#VALUE!", name+" needs text")
		}
		return float64(text[0]), true, nil
	case "CLEAN":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return strings.Map(func(character rune) rune {
			if character < 32 {
				return -1
			}
			return character
		}, display(values[0])), true, nil
	case "DOLLAR", "FIXED":
		if len(values) < 1 || len(values) > 3 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		digits := 2
		if len(values) >= 2 && !omitted(values[1]) {
			supplied, err := integerValue(values[1], name)
			if err != nil {
				return nil, true, err
			}
			digits = supplied
		}
		grouped := true
		if name == "FIXED" && len(values) == 3 {
			suppress, err := booleanValue(values[2], name)
			if err != nil {
				return nil, true, err
			}
			grouped = !suppress
		}
		text := formatNumber(number, digits, grouped)
		if name == "DOLLAR" {
			return "₩" + text, true, nil
		}
		return text, true, nil
	case "REGEXMATCH", "REGEXEXTRACT", "REGEXREPLACE":
		return evaluateRegex(name, values)
	case "JOIN":
		if len(values) < 2 {
			return nil, true, argError(name)
		}
		separator := display(values[0])
		parts := make([]string, 0, len(values)-1)
		for _, value := range values[1:] {
			parts = append(parts, display(value))
		}
		return strings.Join(parts, separator), true, nil
	}
	return nil, false, nil
}

func evaluateRegex(name string, values []any) (any, bool, error) {
	if (name == "REGEXREPLACE" && len(values) != 3) || (name != "REGEXREPLACE" && len(values) != 2) {
		return nil, true, argError(name)
	}
	pattern, err := regexp.Compile(display(values[1]))
	if err != nil {
		return nil, true, formulaError("#VALUE!", name+" received an invalid expression")
	}
	text := display(values[0])
	switch name {
	case "REGEXMATCH":
		return pattern.MatchString(text), true, nil
	case "REGEXEXTRACT":
		match := pattern.FindStringSubmatch(text)
		if match == nil {
			return nil, true, formulaError("#N/A", "REGEXEXTRACT found no match")
		}
		if len(match) > 1 {
			return match[1], true, nil
		}
		return match[0], true, nil
	}
	// Go writes capture groups as $1; spreadsheets write them as $1 too, so the
	// replacement passes through unchanged.
	return pattern.ReplaceAllString(text, display(values[2])), true, nil
}

// formatValue implements the subset of number formats people actually type
// into TEXT: digit and thousands patterns, percentages, and date patterns.
func formatValue(value any, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	// "General" 은 "있는 그대로", "@" 는 "글자로" 라는 뜻이다. 둘 다 값을
	// 손대지 않는다. 예전에는 이것을 숫자 서식으로 읽어 =TEXT(0.5,"General")
	// 이 "1" 이 되었다. 격자는 있는 그대로 보여주고 있었다.
	if pattern == "" || strings.EqualFold(pattern, "general") || pattern == "@" {
		return display(value)
	}
	if moment, ok := parseDate(value); ok && isDatePattern(pattern) {
		return renderDatePattern(moment, pattern)
	}
	// 서식은 값의 부호마다 다른 구역을 담는다. ";" 로 나눈다.
	sections := formatSections(pattern)
	number, ok := toNumber(value)
	if !ok {
		// 넷째 구역은 글자 값의 서식이다. 구역이 넷이 안 되면 엑셀은 글자를
		// 손대지 않는다.
		if section, isText := textSection(sections); isText {
			return renderTextSection(section, display(value))
		}
		return display(value)
	}
	section, flipped := sectionForNumber(sections, number)
	if flipped {
		number = -number
	}
	// 비워 둔 구역은 "그 값은 적지 말라" 는 뜻이다. "0;;" 은 음수와 0 을 감춘다.
	if section == "" {
		return ""
	}
	// @ 는 값을 있는 그대로 적으라는 자리다. 수에도 쓴다 — 5 에 @"원" 은
	// "5원" 이다. 예전에는 자리 기호가 아니라고 보아 "@원" 이라고 적었다.
	if hasTextPlaceholder(section) {
		return renderTextSection(section, display(value))
	}
	// 0.00E+00 처럼 지수로 적는 서식. E 뒤의 0 개수만큼 지수 자리를 채운다.
	if match := scientificPattern.FindStringSubmatch(section); match != nil {
		return renderScientific(number, len(match[1]), len(match[3]), match[2])
	}
	// # ?/? 처럼 빗금 양쪽에 자리 기호가 붙은 서식은 분수로 적는다.
	if spec, ok := parseFractionFormat(section); ok && math.Abs(number) < maxFractionValue {
		return renderFraction(number, spec)
	}
	prefix, digits, suffix := splitFormatSection(section)
	// 자리 기호가 하나도 없는 구역은 수를 적지 않고 적어 둔 글자만 적는다.
	// 회계 서식의 0 구역이 그 꼴이다 — #,##0;(#,##0);"-" 의 0 은 "-" 이다.
	if !strings.ContainsAny(digits, "0#?") && !isDatePattern(section) {
		return prefix + suffix
	}
	// 날짜 서식에 날짜가 될 수 없는 수가 들어오면 여기까지 흘러오는데, 그때
	// y 나 m 을 글자로 적으면 "-yyyy-mm-dd1" 이 된다. 격자는 "-1" 을 그린다.
	if !strings.ContainsAny(digits, "0#") {
		prefix, suffix = "", ""
	}
	if strings.Contains(digits, "%") {
		number *= 100
	}
	decimals := 0
	if index := strings.Index(digits, "."); index >= 0 {
		// 뒤에 붙은 쉼표는 소수 자리가 아니라 천 단위 축약이다. 세면
		// 0.0,, 이 소수 세 자리가 되어 1.2 대신 1.235 가 나온다.
		decimals = len(strings.TrimRight(digits[index+1:], "%,"))
	}
	scaled, grouped := thousandsScale(digits)
	if scaled > 0 {
		number /= math.Pow(1000, float64(scaled))
	}
	// 백분율 기호는 수 바로 뒤에 온다. 자리 기호와 함께 걷어 두었으므로
	// 여기서 다시 붙인다. 빠뜨리면 500% 가 500 으로 보인다.
	percentMark := ""
	if strings.Contains(digits, "%") {
		percentMark = "%"
	}
	body := formatNumber(math.Abs(number), decimals, grouped)
	rendered := prefix + body + percentMark + suffix
	// 빼기 부호는 기호 앞에 온다. 엑셀은 -₩5 로 적지 ₩-5 로 적지 않는다.
	// 음수 구역을 쓴 경우에는 number 를 이미 뒤집었으므로 붙지 않는다.
	// -0.2 를 정수 자리로 반올림하면 0 이다. "-0" 은 잘못 그린 것처럼 읽힌다.
	if number < 0 && strings.Trim(body, "0.,") != "" && !strings.HasPrefix(rendered, "-") {
		rendered = "-" + rendered
	}
	return rendered
}

// formatSections 는 서식을 구역으로 나눈다. 따옴표 안과 대괄호 안의 ";" 는
// 구역을 가르지 않는다 — #,##0;"내려감;" 처럼 글자로 적은 쌍반점이 있다.
//
// web/src/lib/cellFormat.ts 의 formatSections 와 같은 규칙이다.
func formatSections(pattern string) []string {
	sections := make([]string, 0, 4)
	var current strings.Builder
	runes := []rune(pattern)
	for index := 0; index < len(runes); index++ {
		switch character := runes[index]; character {
		case '\\':
			current.WriteRune(character)
			if index+1 < len(runes) {
				index++
				current.WriteRune(runes[index])
			}
		case '"', '[':
			closer := '"'
			if character == '[' {
				closer = ']'
			}
			current.WriteRune(character)
			for index++; index < len(runes); index++ {
				current.WriteRune(runes[index])
				if runes[index] == closer {
					break
				}
			}
		case ';':
			sections = append(sections, current.String())
			current.Reset()
		default:
			current.WriteRune(character)
		}
	}
	return append(sections, current.String())
}

// sectionForNumber 는 값의 부호에 맞는 구역을 고른다. 엑셀의 규칙이다.
//
//	한 구역    모든 값
//	두 구역    양수와 0 ; 음수
//	세 구역    양수 ; 음수 ; 0
//
// 음수 구역을 골랐으면 그 구역이 부호를 스스로 적으므로(대개 괄호다) 값을
// 뒤집어 돌려준다. 예전에는 구역이 둘뿐인 줄 알고 0 을 늘 양수 구역으로
// 보냈다. 회계 서식의 0 은 "-" 인데 "0" 이라고 적혀 있었다.
func sectionForNumber(sections []string, number float64) (string, bool) {
	switch {
	case number < 0 && len(sections) > 1:
		return sections[1], true
	case number == 0 && len(sections) > 2:
		return sections[2], false
	}
	return sections[0], false
}

// textSection 은 글자 값을 그릴 구역을 고른다.
//
// 엑셀 서식의 넷째 구역은 글자 값의 서식이다 — 회계 서식의 마지막 토막
// `_-@_-` 와 `#,##0;(#,##0);"-";"["@"]"` 의 `"["@"]"` 가 그것이다. 구역이
// 넷이 안 되면 엑셀은 글자를 손대지 않는다. 다만 구역이 하나뿐인데 그 안에
// @ 가 있으면 그 구역이 곧 글자 구역이다 — `@" 님"` 처럼 이름 뒤에 말을
// 붙이는 데 흔히 쓴다.
//
// 예전에는 넷째 구역을 아예 읽지 않아 글자 칸이 서식 없이 그대로 나왔고,
// `@" 님"` 은 "@ 님" 이라고 적혔다.
//
// web/src/lib/cellFormat.ts 의 textSection 과 같은 규칙이다.
func textSection(sections []string) (string, bool) {
	if len(sections) > 3 {
		return sections[3], true
	}
	if len(sections) == 1 && hasTextPlaceholder(sections[0]) {
		return sections[0], true
	}
	return "", false
}

// hasTextPlaceholder 는 구역에 값 자리(@)가 있는지 본다. 따옴표 안과 대괄호
// 안의 @ 는 그냥 글자다 — [$@-409] 같은 나라 코드가 있다.
func hasTextPlaceholder(section string) bool {
	found := false
	scanFormatSection(section, func(character rune, _ string) {
		if character == '@' {
			found = true
		}
	})
	return found
}

// renderTextSection 은 글자 구역을 그린다. @ 자리에 값이 들어가고, 나머지는
// splitFormatSection 과 같은 규칙으로 읽는다 — 따옴표 안은 그대로 적고,
// 대괄호 안의 색·나라 코드는 그리지 않으며(통화 기호만 꺼낸다), _ 와 * 는
// 자리 맞추기라 그리지 않는다.
func renderTextSection(section, text string) string {
	var out strings.Builder
	scanFormatSection(section, func(character rune, literal string) {
		switch character {
		case '@':
			out.WriteString(text)
		case 0:
			out.WriteString(literal)
		default:
			out.WriteRune(character)
		}
	})
	return out.String()
}

// scanFormatSection 은 구역을 한 글자씩 읽어 넘긴다. 따옴표·대괄호·역빗금으로
// 묶인 것은 글자(literal)로, 나머지는 글자 하나(character)로 넘어온다. _ 와 *
// 는 자리 맞추기라 그 뒤 한 글자와 함께 건너뛴다.
func scanFormatSection(section string, emit func(character rune, literal string)) {
	runes := []rune(section)
	closing := func(start int, mark rune) int {
		for index := start; index < len(runes); index++ {
			if runes[index] == mark {
				return index
			}
		}
		return len(runes)
	}
	for index := 0; index < len(runes); index++ {
		switch character := runes[index]; character {
		case '"':
			end := closing(index+1, '"')
			emit(0, string(runes[index+1:end]))
			index = end
		case '[':
			end := closing(index+1, ']')
			inside := string(runes[index+1 : end])
			if strings.HasPrefix(inside, "$") {
				if symbol := strings.SplitN(inside[1:], "-", 2)[0]; symbol != "" {
					emit(0, symbol)
				}
			}
			index = end
		case '_', '*':
			index++
		case '\\':
			if index+1 < len(runes) {
				emit(0, string(runes[index+1]))
				index++
			}
		default:
			emit(character, "")
		}
	}
}

// thousandsScale 은 자리 기호 **뒤** 에 붙은 쉼표를 센다. 그 쉼표는 자릿점이
// 아니라 "천 단위로 줄여 적으라" 는 뜻이다 — #,##0, 은 천 단위, #,##0,, 은
// 백만 단위다. 자리 기호 사이의 쉼표만 자릿점이므로 함께 가려낸다.
//
// 예전에는 쉼표가 있기만 하면 자릿점으로 보아 #,##0,, 이 1,234,567 을
// 그대로 적었다. 재무 자료의 "백만 원 단위" 표가 통째로 백만 배로 보였다.
func thousandsScale(digits string) (int, bool) {
	last := strings.LastIndexAny(digits, "0#?")
	if last < 0 {
		return 0, strings.Contains(digits, ",")
	}
	return strings.Count(digits[last+1:], ","), strings.Contains(digits[:last], ",")
}

// splitFormatSection 은 서식 한 구역을 앞 글자·숫자 자리·뒤 글자로 나눈다.
//
// 엑셀에서 온 파일은 자리 기호만 적혀 있지 않다.
//
//	[$-409]#,##0     나라 코드다. 409 의 0 을 자릿수로 세면 값이 달라 보인다.
//	[$₩-412]#,##0    통화 기호는 대괄호 안의 $ 와 - 사이에 있다.
//	#,##0"원"        따옴표 안은 그대로 적는 글자다. 따옴표는 적지 않는다.
//	[red](#,##0)     색 이름이다. 값이 아니므로 그리지 않는다.
//	_-* #,##0_-      _ 는 자리 비우기, * 는 채우기다. 그리지 않는다.
//
// 예전에는 처음 만난 0 이나 # 앞을 통째로 앞 글자로 삼았다. [$-409]#,##0 은
// 409 의 0 에서 갈려 "[$-4" 가 값 앞에 붙어 나왔다.
//
// web/src/lib/cellFormat.ts 의 parseFormatSection 과 같은 규칙이다.
// testdata/cell-formats.json 이 둘을 함께 붙잡는다.
func splitFormatSection(section string) (string, string, string) {
	var head, body, tail strings.Builder
	put := func(text string) {
		if body.Len() == 0 {
			head.WriteString(text)
			return
		}
		tail.WriteString(text)
	}
	runes := []rune(section)
	closing := func(start int, mark rune) int {
		for index := start; index < len(runes); index++ {
			if runes[index] == mark {
				return index
			}
		}
		return len(runes)
	}
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		switch {
		case character == '"':
			end := closing(index+1, '"')
			put(string(runes[index+1 : end]))
			index = end
		case character == '[':
			end := closing(index+1, ']')
			inside := string(runes[index+1 : end])
			// [$기호-나라코드] 에서 기호만 꺼낸다. [red] 나 [$-409] 는 그릴 것이 없다.
			if strings.HasPrefix(inside, "$") {
				if symbol := strings.SplitN(inside[1:], "-", 2)[0]; symbol != "" {
					put(symbol)
				}
			}
			index = end
		case character == '_' || character == '*':
			index++
		case character == '\\':
			if index+1 < len(runes) {
				put(string(runes[index+1]))
				index++
			}
		case strings.ContainsRune("0#?,.%", character):
			body.WriteRune(character)
		// 빈칸은 음수 구역의 괄호와 자리를 맞추려고 넣는 것이다. 그리지 않는다.
		case character == ' ':
		default:
			put(string(character))
		}
	}
	return head.String(), body.String(), tail.String()
}

// isDatePattern 은 서식이 날짜·시각을 적는 것인지 가린다.
//
// 따옴표로 묶은 글자와 벗어난 글자는 그냥 글자이므로 먼저 걷어낸다.
// 대괄호도 걷어내되 [h] [m] [s] 는 남긴다 — 흘러간 시간을 적는 기호다.
//
// 남은 것에 y, d, h, s 가 있으면 날짜 서식이다. m 하나만으로는 가르지
// 않는다. "0.0 m" 처럼 단위로 적은 m 을 달로 읽으면 안 되기 때문이다.
// web/src/lib/cellFormat.ts 의 isDateFormat 이 같은 기준을 쓴다.
func isDatePattern(pattern string) bool {
	cleaned := quotedLiteral.ReplaceAllString(pattern, "")
	cleaned = escapedLetter.ReplaceAllString(cleaned, "")
	// Go 의 정규식에는 부정 전방탐색이 없으므로 하나씩 보고 남길지 정한다.
	cleaned = bracketSection.ReplaceAllStringFunc(cleaned, func(match string) string {
		if elapsedSection.MatchString(match) {
			return match
		}
		return ""
	})
	lowered := strings.ToLower(cleaned)
	// mmm 과 mmmm 은 달 이름이므로 단위로 오해할 일이 없다.
	return strings.ContainsAny(lowered, "ydhs") || strings.Contains(lowered, "mmm")
}

var (
	quotedLiteral  = regexp.MustCompile(`"[^"]*"`)
	escapedLetter  = regexp.MustCompile(`\\.`)
	bracketSection = regexp.MustCompile(`\[[^\]]*\]`)
	// [h] [mm] [ss] 는 흘러간 시간을 적는 기호이므로 남긴다.
	elapsedSection = regexp.MustCompile(`(?i)^\[[hms]+\]$`)
)

// renderDatePattern 은 표 서식 기호를 하나씩 읽어 그대로 적는다.
//
// 예전에는 기호를 Go 의 시각 layout 으로 바꿔치기했는데, 그 방식으로는
// m 을 가려낼 수 없다. 표 서식에서 m 은 **앞뒤를 봐야** 뜻이 정해진다.
//
//	"mm/dd/yyyy"  달
//	"hh:mm"       분 — 시 뒤에 오면 분이다
//	"h:mm:ss"     분 — 초 앞에 와도 분이다
//
// 바꿔치기하던 시절에는 mm 이 늘 달이 되어 =TEXT(0.5,"hh:mm") 이 12:00 이
// 아니라 12:12 였다. 격자는 web/src/lib/cellFormat.ts 에서 제대로 그리고
// 있었으므로, 같은 값을 화면과 TEXT 가 다르게 보여주고 있었다.
func renderDatePattern(moment time.Time, pattern string) string {
	// 대괄호는 [h] [mm] [ss] 만 남긴다. 흘러간 시간을 적는 기호이기 때문이다.
	// [$-412] 같은 나라 코드를 그대로 두면 날짜 앞에 붙어 나온다.
	// web/src/lib/cellFormat.ts 의 formatDate 가 같은 것을 한다.
	pattern = bracketSection.ReplaceAllStringFunc(pattern, func(match string) string {
		if elapsedSection.MatchString(match) {
			return match[1 : len(match)-1]
		}
		return ""
	})
	letters := []rune(pattern)
	twelveHour := hasMeridiem(pattern)
	var builder strings.Builder
	previousWasHour := false
	for index := 0; index < len(letters); {
		// 따옴표 안은 그대로 적는 글자다. 미리 걷어내면 "day" 의 d 가 날짜
		// 기호로 읽히므로 여기서 통째로 옮긴다. 한국 파일의 yyyy"년" 이
		// 2023"년" 으로 나오던 것이 이것 때문이다.
		if letters[index] == '"' {
			end := index + 1
			for end < len(letters) && letters[end] != '"' {
				end++
			}
			builder.WriteString(string(letters[index+1 : end]))
			index = end + 1
			continue
		}
		if letters[index] == '\\' {
			if index+1 < len(letters) {
				builder.WriteRune(letters[index+1])
			}
			index += 2
			continue
		}
		token, size := nextDateToken(letters, index)
		switch token {
		case "":
			builder.WriteRune(letters[index])
			index++
			continue
		case "am/pm", "a/p":
			marker := "AM"
			if moment.Hour() >= 12 {
				marker = "PM"
			}
			if token == "a/p" {
				marker = marker[:1]
			}
			builder.WriteString(marker)
		case "yyyy":
			builder.WriteString(fmt.Sprintf("%04d", moment.Year()))
		case "yy":
			builder.WriteString(fmt.Sprintf("%02d", moment.Year()%100))
		case "mmmm":
			builder.WriteString(moment.Format("January"))
		case "mmm":
			builder.WriteString(moment.Format("Jan"))
		case "dddd":
			builder.WriteString(moment.Format("Monday"))
		case "ddd":
			builder.WriteString(moment.Format("Mon"))
		case "dd":
			builder.WriteString(fmt.Sprintf("%02d", moment.Day()))
		case "d":
			builder.WriteString(strconv.Itoa(moment.Day()))
		case "hh", "h":
			hour := moment.Hour()
			if twelveHour {
				if hour %= 12; hour == 0 {
					hour = 12
				}
			}
			builder.WriteString(padded(hour, token == "hh"))
		case "ss", "s":
			builder.WriteString(padded(moment.Second(), token == "ss"))
		case "mm", "m":
			// 시 뒤에 오거나 초 앞에 오면 분이다. 그 밖에는 달이다.
			if previousWasHour || secondFollows(letters, index+size) {
				builder.WriteString(padded(moment.Minute(), token == "mm"))
			} else {
				builder.WriteString(padded(int(moment.Month()), token == "mm"))
			}
		}
		previousWasHour = token == "hh" || token == "h"
		index += size
	}
	return builder.String()
}

func padded(value int, twoDigits bool) string {
	if twoDigits {
		return fmt.Sprintf("%02d", value)
	}
	return strconv.Itoa(value)
}

// nextDateToken 은 자리에서 가장 긴 서식 기호를 집는다. 기호가 아니면 빈
// 글자와 0 을 돌려주어 부르는 쪽이 글자를 그대로 적게 한다.
func nextDateToken(letters []rune, index int) (string, int) {
	rest := strings.ToLower(string(letters[index:]))
	for _, token := range []string{"am/pm", "a/p", "yyyy", "yy", "mmmm", "mmm", "mm", "m", "dddd", "ddd", "dd", "d", "hh", "h", "ss", "s"} {
		if strings.HasPrefix(rest, token) {
			return token, len([]rune(token))
		}
	}
	return "", 0
}

func hasMeridiem(pattern string) bool {
	lowered := strings.ToLower(pattern)
	return strings.Contains(lowered, "am/pm") || strings.Contains(lowered, "a/p")
}

// secondFollows 는 구분 기호를 건너뛰고 다음 기호가 초인지 본다.
func secondFollows(letters []rune, index int) bool {
	for index < len(letters) {
		token, _ := nextDateToken(letters, index)
		if token == "" {
			index++
			continue
		}
		return token == "ss" || token == "s"
	}
	return false
}

// scientificPattern 은 0.00E+00 꼴을 집는다. 소수 자리와 지수 자리 수를
// 따로 세어야 하므로 묶음을 나눠 둔다.
var scientificPattern = regexp.MustCompile(`(?i)^0(?:\.(0+))?E([+-])(0+)$`)

// renderScientific 은 엑셀·시트가 지수를 적는 방식을 따른다.
//
//	0.00E+00  에 0.5   →  5.00E-01
//	0.00E+00  에 1234  →  1.23E+03
//
// 지수 자리는 서식에 적은 0 개수만큼 채운다. 예전에는 서버가 이 꼴을
// 아예 못 알아보고 "0.500000" 처럼 적었고, 격자는 자리를 채우지 않아
// "5.00E-1" 이었다. 둘 다 틀렸고 틀린 모양도 달랐다.
func renderScientific(number float64, mantissaDecimals, exponentDigits int, sign string) string {
	exponent := 0
	mantissa := number
	if number != 0 {
		exponent = int(math.Floor(math.Log10(math.Abs(number))))
		mantissa = number / math.Pow(10, float64(exponent))
		// 반올림하다 10 이 되면 자리를 하나 올린다. 9.99 가 10.0 이 되는 경우다.
		mantissa = decimalRound(mantissa, mantissaDecimals, roundHalfAway)
		if math.Abs(mantissa) >= 10 {
			mantissa /= 10
			exponent++
		}
	}
	marker := "+"
	if exponent < 0 {
		marker = "-"
	} else if sign == "-" {
		// "0.00E-00" 은 양의 지수에 부호를 적지 않는다.
		marker = ""
	}
	magnitude := exponent
	if magnitude < 0 {
		magnitude = -magnitude
	}
	return fmt.Sprintf("%.*fE%s%0*d", mantissaDecimals, mantissa, marker, exponentDigits, magnitude)
}

func formatNumber(number float64, decimals int, grouped bool) string {
	// 화면에 보이는 자릿수도 사람이 적은 십진수를 따라야 한다. 그냥
	// FormatFloat 에 맡기면 이진 실수를 그대로 반올림하므로 1.005 가
	// "1.00" 이 된다. 브라우저의 Intl.NumberFormat 은 "1.01" 을 내므로,
	// 그대로 두면 화면과 TEXT 의 답이 서로 달라진다.
	rounded := decimalRound(number, decimals, roundHalfAway)
	// 자릿수가 음수면 소수점 왼쪽에서 반올림한다 — ROUND 와 같은 규칙이다.
	// 예전에는 여기서 0 으로 깎아 버려 FIXED(1234.567,-2) 가 "1,200" 이
	// 아니라 "1,235" 였다. 소수점 뒤에 적을 자리는 없으므로 0 으로 적는다.
	if decimals < 0 {
		decimals = 0
	}
	// FIXED(1,999999999) 처럼 자릿수가 터무니없이 크면 그만큼의 0 을 이어
	// 붙이느라 글자가 기가바이트로 자란다. float64 가 담는 십진수는
	// maxDecimalPlaces 자리 안에서 끝나므로 그 뒤는 어차피 0 뿐이다.
	if decimals > maxDecimalPlaces {
		decimals = maxDecimalPlaces
	}
	text := strconv.FormatFloat(rounded, 'f', decimals, 64)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	whole, fraction := text, ""
	if index := strings.Index(text, "."); index >= 0 {
		whole, fraction = text[:index], text[index:]
	}
	if grouped {
		whole = groupDigits(whole)
	}
	if negative {
		return "-" + whole + fraction
	}
	return whole + fraction
}

func groupDigits(whole string) string {
	if len(whole) <= 3 {
		return whole
	}
	var builder strings.Builder
	lead := len(whole) % 3
	if lead > 0 {
		builder.WriteString(whole[:lead])
	}
	for index := lead; index < len(whole); index += 3 {
		if builder.Len() > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(whole[index : index+3])
	}
	return builder.String()
}

// evaluateTextArray covers the text functions that produce or consume a range.
func evaluateTextArray(name string, arguments []any) (any, bool, error) {
	switch name {
	case "TEXTBEFORE", "TEXTAFTER":
		if len(arguments) < 2 || len(arguments) > 6 {
			return nil, true, argError(name)
		}
		text := display(scalarOrFirst(arguments[0]))
		delimiters := textDelimiters(arguments[1])
		if len(delimiters) == 0 {
			return nil, true, formulaError("#VALUE!", name+" needs a delimiter")
		}
		instance := 1
		if len(arguments) >= 3 && !omitted(arguments[2]) {
			supplied, err := integerValue(scalarOrFirst(arguments[2]), name)
			if err != nil {
				return nil, true, err
			}
			instance = supplied
		}
		if instance == 0 {
			return nil, true, formulaError("#VALUE!", name+" instance cannot be zero")
		}
		insensitive := false
		if len(arguments) >= 4 && !omitted(arguments[3]) {
			mode, err := integerValue(scalarOrFirst(arguments[3]), name)
			if err != nil {
				return nil, true, err
			}
			insensitive = mode == 1
		}
		matchEnd := false
		if len(arguments) >= 5 && !omitted(arguments[4]) {
			mode, err := integerValue(scalarOrFirst(arguments[4]), name)
			if err != nil {
				return nil, true, err
			}
			matchEnd = mode == 1
		}
		found := delimiterPositions(text, delimiters, insensitive)
		if matchEnd {
			// The end of the text counts as one more delimiter, which is how
			// `TEXTAFTER(text, ",", -1, , 1)` returns an empty tail instead of
			// an error when the text ends on a separator.
			found = append(found, [2]int{len(text), 0})
		}
		position := instance - 1
		if instance < 0 {
			position = len(found) + instance
		}
		if position < 0 || position >= len(found) {
			if len(arguments) == 6 && !omitted(arguments[5]) {
				return scalarOrFirst(arguments[5]), true, nil
			}
			return nil, true, formulaError("#N/A", name+" did not find the delimiter")
		}
		if name == "TEXTBEFORE" {
			return text[:found[position][0]], true, nil
		}
		return text[found[position][0]+found[position][1]:], true, nil
	case "TEXTSPLIT":
		if len(arguments) < 2 || len(arguments) > 6 {
			return nil, true, argError(name)
		}
		text := display(scalarOrFirst(arguments[0]))
		columnDelimiters := textDelimiters(arguments[1])
		rowDelimiters := []string{}
		if len(arguments) >= 3 && !omitted(arguments[2]) {
			rowDelimiters = textDelimiters(arguments[2])
		}
		if len(columnDelimiters) == 0 && len(rowDelimiters) == 0 {
			return nil, true, formulaError("#VALUE!", "TEXTSPLIT needs a delimiter")
		}
		ignoreEmpty := false
		if len(arguments) >= 4 && !omitted(arguments[3]) {
			flag, err := booleanValue(scalarOrFirst(arguments[3]), name)
			if err != nil {
				return nil, true, err
			}
			ignoreEmpty = flag
		}
		insensitive := false
		if len(arguments) >= 5 && !omitted(arguments[4]) {
			mode, err := integerValue(scalarOrFirst(arguments[4]), name)
			if err != nil {
				return nil, true, err
			}
			insensitive = mode == 1
		}
		var padding any
		if len(arguments) == 6 && !omitted(arguments[5]) {
			padding = scalarOrFirst(arguments[5])
		}
		lines := []string{text}
		if len(rowDelimiters) > 0 {
			lines = splitOnDelimiters(text, rowDelimiters, insensitive)
		}
		rows := make([][]any, 0, len(lines))
		width := 0
		for _, line := range lines {
			parts := []string{line}
			if len(columnDelimiters) > 0 {
				parts = splitOnDelimiters(line, columnDelimiters, insensitive)
			}
			cells := make([]any, 0, len(parts))
			for _, part := range parts {
				if ignoreEmpty && part == "" {
					continue
				}
				cells = append(cells, part)
			}
			if ignoreEmpty && len(cells) == 0 {
				continue
			}
			width = max(width, len(cells))
			rows = append(rows, cells)
		}
		if len(rows) == 0 || width == 0 {
			return nil, true, formulaError("#N/A", "TEXTSPLIT found no values")
		}
		result := arrayValue{rows: len(rows), columns: width, values: make([]any, 0, len(rows)*width)}
		for _, row := range rows {
			for column := 0; column < width; column++ {
				if column < len(row) {
					result.values = append(result.values, row[column])
					continue
				}
				result.values = append(result.values, padding)
			}
		}
		return result, true, nil
	case "SPLIT":
		if len(arguments) < 2 || len(arguments) > 4 {
			return nil, true, argError(name)
		}
		text := display(scalarOrFirst(arguments[0]))
		separators := display(scalarOrFirst(arguments[1]))
		eachCharacter := true
		if len(arguments) >= 3 && !omitted(arguments[2]) {
			flag, err := booleanValue(scalarOrFirst(arguments[2]), name)
			if err != nil {
				return nil, true, err
			}
			eachCharacter = flag
		}
		removeEmpty := true
		if len(arguments) == 4 && !omitted(arguments[3]) {
			flag, err := booleanValue(scalarOrFirst(arguments[3]), name)
			if err != nil {
				return nil, true, err
			}
			removeEmpty = flag
		}
		var parts []string
		if eachCharacter {
			parts = strings.FieldsFunc(text, func(character rune) bool {
				return strings.ContainsRune(separators, character)
			})
			if !removeEmpty {
				parts = splitByAny(text, separators)
			}
		} else {
			parts = strings.Split(text, separators)
		}
		if removeEmpty {
			kept := parts[:0]
			for _, part := range parts {
				if part != "" {
					kept = append(kept, part)
				}
			}
			parts = kept
		}
		values := make([]any, len(parts))
		for index, part := range parts {
			values[index] = part
		}
		if len(values) == 0 {
			return "", true, nil
		}
		return arrayValue{rows: 1, columns: len(values), values: values}, true, nil
	case "TEXTJOIN":
		// TEXTJOIN(separator, ignore_empty, …) needs the flag kept apart from
		// the values, which flattening would lose.
		if len(arguments) < 3 {
			return nil, true, argError(name)
		}
		separator := display(scalarOrFirst(arguments[0]))
		ignoreEmpty, err := booleanValue(scalarOrFirst(arguments[1]), name)
		if err != nil {
			return nil, true, err
		}
		parts := make([]string, 0)
		for _, argument := range arguments[2:] {
			for _, value := range flatten(argument) {
				text := display(value)
				if ignoreEmpty && text == "" {
					continue
				}
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, separator), true, nil
	}
	return nil, false, nil
}

func splitByAny(text, separators string) []string {
	parts := []string{""}
	for _, character := range text {
		if strings.ContainsRune(separators, character) {
			parts = append(parts, "")
			continue
		}
		parts[len(parts)-1] += string(character)
	}
	return parts
}

// textDelimiters reads the separator argument, which may name several
// separators at once so one call can split on ", " and " and " together.
func textDelimiters(argument any) []string {
	items := make([]string, 0)
	for _, value := range flatten(argument) {
		if value == nil {
			continue
		}
		if text := display(value); text != "" {
			items = append(items, text)
		}
	}
	return items
}

// delimiterPositions lists where each separator starts and how long it is,
// scanning left to right so two separators never claim the same characters.
// The longest separator wins a tie, which is what makes ", " beat "," .
func delimiterPositions(text string, delimiters []string, insensitive bool) [][2]int {
	found := make([][2]int, 0)
	for index := 0; index < len(text); {
		matched := 0
		for _, delimiter := range delimiters {
			if len(delimiter) <= matched || index+len(delimiter) > len(text) {
				continue
			}
			candidate := text[index : index+len(delimiter)]
			if candidate == delimiter || (insensitive && strings.EqualFold(candidate, delimiter)) {
				matched = len(delimiter)
			}
		}
		if matched == 0 {
			index++
			continue
		}
		found = append(found, [2]int{index, matched})
		index += matched
	}
	return found
}

func splitOnDelimiters(text string, delimiters []string, insensitive bool) []string {
	found := delimiterPositions(text, delimiters, insensitive)
	parts := make([]string, 0, len(found)+1)
	start := 0
	for _, position := range found {
		parts = append(parts, text[start:position[0]])
		start = position[0] + position[1]
	}
	return append(parts, text[start:])
}
