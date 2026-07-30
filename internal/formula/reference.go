package formula

import (
	"regexp"
	"strconv"
	"strings"
)

var a1ReferencePattern = regexp.MustCompile(`(\$?)([A-Za-z]{1,3})(\$?)([1-9][0-9]*)`)

// ShiftReferences moves relative A1 references while preserving absolute axes
// and quoted string literals. It is shared by structural workbook operations
// such as row sorting, moving, and future insert/delete support.
func ShiftReferences(input string, rowDelta, columnDelta int) string {
	if input == "" || (rowDelta == 0 && columnDelta == 0) {
		return input
	}
	var result, segment strings.Builder
	inString := false
	flush := func() {
		value := segment.String()
		segment.Reset()
		if inString {
			result.WriteString(value)
		} else {
			result.WriteString(shiftReferenceSegment(value, rowDelta, columnDelta))
		}
	}
	for index := 0; index < len(input); index++ {
		character := input[index]
		if character != '"' {
			segment.WriteByte(character)
			continue
		}
		if inString && index+1 < len(input) && input[index+1] == '"' {
			segment.WriteString(`""`)
			index++
			continue
		}
		flush()
		result.WriteByte('"')
		inString = !inString
	}
	flush()
	return result.String()
}

func shiftReferenceSegment(segment string, rowDelta, columnDelta int) string {
	matches := a1ReferencePattern.FindAllStringSubmatchIndex(segment, -1)
	if len(matches) == 0 {
		return segment
	}
	var result strings.Builder
	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		if (start > 0 && referenceIdentifierByte(segment[start-1])) || (end < len(segment) && referenceIdentifierByte(segment[end])) {
			continue
		}
		result.WriteString(segment[last:start])
		columnAbsolute := segment[match[2]:match[3]]
		columnLetters := segment[match[4]:match[5]]
		rowAbsolute := segment[match[6]:match[7]]
		rowDigits := segment[match[8]:match[9]]
		column := columnNumber(columnLetters)
		row, _ := strconv.Atoi(rowDigits)
		if columnAbsolute == "" {
			column += columnDelta
		}
		if rowAbsolute == "" {
			row += rowDelta
		}
		if column < 1 || row < 1 {
			result.WriteString("#REF!")
		} else {
			result.WriteString(columnAbsolute)
			result.WriteString(columnName(column))
			result.WriteString(rowAbsolute)
			result.WriteString(strconv.Itoa(row))
		}
		last = end
	}
	if last == 0 {
		return segment
	}
	result.WriteString(segment[last:])
	return result.String()
}

func referenceIdentifierByte(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '.'
}

func columnNumber(name string) int {
	value := 0
	for _, character := range strings.ToUpper(name) {
		value = value*26 + int(character-'A'+1)
	}
	return value
}

func columnName(column int) string {
	var result string
	for column > 0 {
		column--
		result = string(rune('A'+column%26)) + result
		column /= 26
	}
	return result
}
