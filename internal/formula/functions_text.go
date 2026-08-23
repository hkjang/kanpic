package formula

import (
	"regexp"
	"strconv"
	"strings"
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
		return moment.Format(goDateLayout(pattern))
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

func isDatePattern(pattern string) bool {
	lower := strings.ToLower(pattern)
	return strings.Contains(lower, "yy") || strings.Contains(lower, "mmm") ||
		(strings.Contains(lower, "d") && strings.Contains(lower, "m")) || strings.Contains(lower, "hh")
}

// goDateLayout translates the spreadsheet date pattern to a Go layout, longest
// token first so mm is not consumed as two m tokens.
func goDateLayout(pattern string) string {
	replacements := []struct{ from, to string }{
		{"yyyy", "2006"}, {"yy", "06"},
		{"mmmm", "January"}, {"mmm", "Jan"}, {"mm", "01"},
		{"dddd", "Monday"}, {"ddd", "Mon"}, {"dd", "02"},
		{"hh", "15"}, {"ss", "05"},
	}
	result := strings.ToLower(pattern)
	for _, replacement := range replacements {
		result = strings.ReplaceAll(result, replacement.from, replacement.to)
	}
	// A lone m between date tokens means the month; after 15 it means minutes.
	result = strings.ReplaceAll(result, "15:m", "15:04")
	result = strings.ReplaceAll(result, "d", "2")
	result = strings.ReplaceAll(result, "m", "1")
	return result
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
