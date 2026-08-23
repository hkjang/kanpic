package formula

import (
	"fmt"
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
	if pattern == "" {
		return display(value)
	}
	if moment, ok := parseDate(value); ok && isDatePattern(pattern) {
		return renderDatePattern(moment, pattern)
	}
	number, ok := toNumber(value)
	if !ok {
		return display(value)
	}
	// A format can carry a positive and a negative section separated by ";".
	sections := strings.Split(pattern, ";")
	section := sections[0]
	if number < 0 && len(sections) > 1 {
		section, number = sections[1], -number
	}
	prefix, suffix, digits := "", "", section
	if index := strings.IndexAny(section, "0#"); index > 0 {
		prefix, digits = section[:index], section[index:]
	}
	if index := strings.LastIndexAny(digits, "0#"); index >= 0 && index+1 < len(digits) {
		suffix, digits = digits[index+1:], digits[:index+1]
	}
	if strings.Contains(section, "%") {
		number *= 100
	}
	decimals := 0
	if index := strings.Index(digits, "."); index >= 0 {
		decimals = len(strings.TrimRight(digits[index+1:], "%"))
	}
	grouped := strings.Contains(digits, ",")
	return prefix + formatNumber(number, decimals, grouped) + suffix
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
	letters := []rune(pattern)
	twelveHour := hasMeridiem(pattern)
	var builder strings.Builder
	previousWasHour := false
	for index := 0; index < len(letters); {
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

func formatNumber(number float64, decimals int, grouped bool) string {
	if decimals < 0 {
		decimals = 0
	}
	// 화면에 보이는 자릿수도 사람이 적은 십진수를 따라야 한다. 그냥
	// FormatFloat 에 맡기면 이진 실수를 그대로 반올림하므로 1.005 가
	// "1.00" 이 된다. 브라우저의 Intl.NumberFormat 은 "1.01" 을 내므로,
	// 그대로 두면 화면과 TEXT 의 답이 서로 달라진다.
	text := strconv.FormatFloat(decimalRound(number, decimals, roundHalfAway), 'f', decimals, 64)
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
