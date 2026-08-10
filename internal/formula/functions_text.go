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
		if len(values) >= 2 {
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
	text := strconv.FormatFloat(number, 'f', decimals, 64)
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
	case "SPLIT":
		if len(arguments) < 2 || len(arguments) > 4 {
			return nil, true, argError(name)
		}
		text := display(scalarOrFirst(arguments[0]))
		separators := display(scalarOrFirst(arguments[1]))
		eachCharacter := true
		if len(arguments) >= 3 {
			flag, err := booleanValue(scalarOrFirst(arguments[2]), name)
			if err != nil {
				return nil, true, err
			}
			eachCharacter = flag
		}
		removeEmpty := true
		if len(arguments) == 4 {
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
