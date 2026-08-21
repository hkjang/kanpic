package formula

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"kanpic/pkg/cellrange"
)

const (
	MaxRows    = 1_048_576
	MaxColumns = 16_384
)

// StructuralChange describes an insertion, a deletion or a move of Count rows
// or columns starting at Index on one sheet. CurrentSheet identifies where the
// formula lives so unqualified A1 references can be transformed without
// touching references to other sheets. Destination applies to a move only and
// names the position, in pre-move coordinates, the band lands in front of.
type StructuralChange struct {
	Axis         string
	Action       string
	Index        int
	Count        int
	Destination  int
	CurrentSheet string
	TargetSheet  string
}

type structuralCell struct {
	columnAbsolute bool
	rowAbsolute    bool
	row            int
	column         int
}

// TransformRangeAddress applies the same structural rules to a normalized A1
// metadata range. The bool is false when deletion removes the entire range.
func TransformRangeAddress(input string, change StructuralChange) (string, bool, error) {
	selected, err := cellrange.Parse(input)
	if err != nil {
		return "", false, err
	}
	start, end := selected.Start.Row, selected.End.Row
	limit := MaxRows
	if change.Axis == "column" {
		start, end, limit = selected.Start.Column, selected.End.Column, MaxColumns
	}
	start, end, exists := transformStructuralInterval(start, end, change)
	if !exists {
		return "", false, nil
	}
	if start < 1 || end > limit {
		return "", false, fmt.Errorf("transformed range exceeds spreadsheet bounds")
	}
	if change.Axis == "row" {
		selected.Start.Row, selected.End.Row = start, end
	} else {
		selected.Start.Column, selected.End.Column = start, end
	}
	first := cellrange.Address(selected.Start.Row, selected.Start.Column)
	last := cellrange.Address(selected.End.Row, selected.End.Column)
	if first == last {
		return first, true, nil
	}
	return first + ":" + last, true, nil
}

// TransformStructuralReferences preserves formula text while adjusting
// references affected by a row or column insertion/deletion.
func TransformStructuralReferences(input string, change StructuralChange) string {
	if input == "" || change.Index < 1 || change.Count < 1 ||
		(change.Axis != "row" && change.Axis != "column") ||
		(change.Action != "insert" && change.Action != "delete" && change.Action != "move") ||
		(change.Action == "move" && change.Destination < 1) {
		return input
	}
	var result strings.Builder
	for index := 0; index < len(input); {
		if input[index] == '"' {
			index = copyFormulaString(&result, input, index)
			continue
		}
		if input[index] == '\'' {
			if next, qualifier, ok := quotedQualifier(input, index); ok {
				end, replacement, reference := structuralReferenceAt(input, next, change, strings.EqualFold(qualifier, change.TargetSheet))
				if reference {
					if replacement != "#REF!" {
						result.WriteString(input[index:next])
					}
					result.WriteString(replacement)
					index = end
					continue
				}
			}
		}
		if input[index] >= '0' && input[index] <= '9' {
			// A bare number here is either a literal or the start of a 2:5 row
			// band; the digits of a cell address are consumed with its column.
			if strings.EqualFold(change.CurrentSheet, change.TargetSheet) {
				if end, replacement, band := structuralBandAt(input, index, change, true); band {
					result.WriteString(replacement)
					index = end
					continue
				}
			}
			end := index
			for end < len(input) && ((input[end] >= '0' && input[end] <= '9') || input[end] == '.') {
				end++
			}
			result.WriteString(input[index:end])
			index = end
			continue
		}
		character, size := utf8.DecodeRuneInString(input[index:])
		if formulaIdentifierStart(character) {
			tokenEnd := formulaIdentifierEnd(input, index)
			bangIndex := skipFormulaSpaces(input, tokenEnd)
			if bangIndex < len(input) && input[bangIndex] == '!' {
				referenceStart := skipFormulaSpaces(input, bangIndex+1)
				qualifier := strings.ReplaceAll(input[index:tokenEnd], "$", "")
				end, replacement, reference := structuralReferenceAt(input, referenceStart, change, strings.EqualFold(qualifier, change.TargetSheet))
				if reference {
					if replacement != "#REF!" {
						result.WriteString(input[index:referenceStart])
					}
					result.WriteString(replacement)
					index = end
					continue
				}
			}
			if strings.EqualFold(change.CurrentSheet, change.TargetSheet) {
				end, replacement, reference := structuralReferenceAt(input, index, change, true)
				if reference {
					result.WriteString(replacement)
					index = end
					continue
				}
			}
			result.WriteString(input[index:tokenEnd])
			index = tokenEnd
			continue
		}
		result.WriteRune(character)
		index += size
	}
	return result.String()
}

func copyFormulaString(result *strings.Builder, input string, start int) int {
	result.WriteByte('"')
	for index := start + 1; index < len(input); index++ {
		result.WriteByte(input[index])
		if input[index] != '"' {
			continue
		}
		if index+1 < len(input) && input[index+1] == '"' {
			result.WriteByte('"')
			index++
			continue
		}
		return index + 1
	}
	return len(input)
}

func quotedQualifier(input string, start int) (int, string, bool) {
	var name strings.Builder
	index := start + 1
	for index < len(input) {
		if input[index] != '\'' {
			character, size := utf8.DecodeRuneInString(input[index:])
			name.WriteRune(character)
			index += size
			continue
		}
		if index+1 < len(input) && input[index+1] == '\'' {
			name.WriteByte('\'')
			index += 2
			continue
		}
		index = skipFormulaSpaces(input, index+1)
		if index >= len(input) || input[index] != '!' {
			return 0, "", false
		}
		return skipFormulaSpaces(input, index+1), name.String(), true
	}
	return 0, "", false
}

func structuralReferenceAt(input string, start int, change StructuralChange, affected bool) (int, string, bool) {
	if end, replacement, band := structuralBandAt(input, start, change, affected); band {
		return end, replacement, true
	}
	first, end, ok := parseStructuralCell(input, start)
	if !ok || formulaIdentifierEnd(input, end) != end {
		return start, "", false
	}
	second, referenceEnd, ranged := first, end, false
	colon := skipFormulaSpaces(input, end)
	if colon < len(input) && input[colon] == ':' {
		secondStart := skipFormulaSpaces(input, colon+1)
		var secondOK bool
		second, referenceEnd, secondOK = parseStructuralCell(input, secondStart)
		if !secondOK || formulaIdentifierEnd(input, referenceEnd) != referenceEnd {
			return start, "", false
		}
		ranged = true
	}
	next := skipFormulaSpaces(input, referenceEnd)
	if !ranged && next < len(input) && input[next] == '(' {
		return start, "", false
	}
	if !affected {
		return referenceEnd, input[start:referenceEnd], true
	}
	transformed, exists := transformStructuralCells(first, second, ranged, change)
	if !exists {
		return referenceEnd, "#REF!", true
	}
	replacement := renderStructuralCell(transformed[0])
	if ranged {
		replacement += ":" + renderStructuralCell(transformed[1])
	}
	return referenceEnd, replacement, true
}

// structuralBandAt transforms the unbounded forms A:A, A:C and 2:5. A column
// band only moves when columns move, and a row band only when rows move, so
// SUM(A:A) keeps meaning "this column" however many rows are inserted.
func structuralBandAt(input string, start int, change StructuralChange, affected bool) (int, string, bool) {
	first, end, absolute, kind := parseStructuralBand(input, start)
	if kind == 0 {
		return start, "", false
	}
	colon := skipFormulaSpaces(input, end)
	if colon >= len(input) || input[colon] != ':' {
		return start, "", false
	}
	second, referenceEnd, secondAbsolute, secondKind := parseStructuralBand(input, skipFormulaSpaces(input, colon+1))
	if secondKind != kind || formulaIdentifierEnd(input, referenceEnd) != referenceEnd {
		return start, "", false
	}
	axis := "column"
	if kind == structuralRowBand {
		axis = "row"
	}
	if !affected || change.Axis != axis {
		return referenceEnd, input[start:referenceEnd], true
	}
	low, high := first, second
	if low > high {
		low, high = high, low
	}
	transformedLow, transformedHigh, exists := transformStructuralInterval(low, high, change)
	if !exists {
		return referenceEnd, "#REF!", true
	}
	if first > second {
		transformedLow, transformedHigh = transformedHigh, transformedLow
	}
	return referenceEnd, renderStructuralBand(transformedLow, absolute, kind) + ":" + renderStructuralBand(transformedHigh, secondAbsolute, kind), true
}

const (
	structuralColumnBand = 1
	structuralRowBand    = 2
)

// parseStructuralBand reads one half of an unbounded reference: a bare column
// name or a bare row number, optionally made absolute with $.
func parseStructuralBand(input string, start int) (int, int, bool, int) {
	index := start
	dollar := false
	if index < len(input) && input[index] == '$' {
		dollar = true
		index++
	}
	letters := index
	column := 0
	for index < len(input) && ((input[index] >= 'A' && input[index] <= 'Z') || (input[index] >= 'a' && input[index] <= 'z')) {
		column = column*26 + int(unicode.ToUpper(rune(input[index]))-'A'+1)
		index++
	}
	if index > letters {
		// Letters followed by digits are an ordinary cell, not a column band.
		if index-letters > 3 || column > MaxColumns || (index < len(input) && input[index] >= '0' && input[index] <= '9') {
			return 0, start, false, 0
		}
		return column, index, dollar, structuralColumnBand
	}
	digits := index
	for index < len(input) && input[index] >= '0' && input[index] <= '9' {
		index++
	}
	if index == digits {
		return 0, start, false, 0
	}
	row, err := strconv.Atoi(input[digits:index])
	if err != nil || row < 1 || row > MaxRows {
		return 0, start, false, 0
	}
	return row, index, dollar, structuralRowBand
}

func renderStructuralBand(position int, absolute bool, kind int) string {
	prefix := ""
	if absolute {
		prefix = "$"
	}
	if kind == structuralRowBand {
		return prefix + strconv.Itoa(position)
	}
	return prefix + columnName(position)
}

func parseStructuralCell(input string, start int) (structuralCell, int, bool) {
	index := start
	cell := structuralCell{}
	if index < len(input) && input[index] == '$' {
		cell.columnAbsolute = true
		index++
	}
	columnStart := index
	for index < len(input) && ((input[index] >= 'A' && input[index] <= 'Z') || (input[index] >= 'a' && input[index] <= 'z')) {
		cell.column = cell.column*26 + int(unicode.ToUpper(rune(input[index]))-'A'+1)
		index++
	}
	if index == columnStart || cell.column > MaxColumns {
		return structuralCell{}, start, false
	}
	if index < len(input) && input[index] == '$' {
		cell.rowAbsolute = true
		index++
	}
	rowStart := index
	for index < len(input) && input[index] >= '0' && input[index] <= '9' {
		index++
	}
	if rowStart == index {
		return structuralCell{}, start, false
	}
	row, err := strconv.Atoi(input[rowStart:index])
	if err != nil || row < 1 || row > MaxRows {
		return structuralCell{}, start, false
	}
	cell.row = row
	return cell, index, true
}

func transformStructuralCells(first, second structuralCell, ranged bool, change StructuralChange) ([2]structuralCell, bool) {
	result := [2]structuralCell{first, second}
	if !ranged {
		position := first.row
		if change.Axis == "column" {
			position = first.column
		}
		position, exists := transformStructuralPosition(position, change)
		if !exists {
			return result, false
		}
		if change.Axis == "row" {
			result[0].row = position
		} else {
			result[0].column = position
		}
		return result, true
	}
	start, end := first.row, second.row
	if change.Axis == "column" {
		start, end = first.column, second.column
	}
	start, end, exists := transformStructuralInterval(start, end, change)
	if !exists {
		return result, false
	}
	limit := MaxRows
	if change.Axis == "column" {
		limit = MaxColumns
	}
	if end > limit {
		return result, false
	}
	if change.Axis == "row" {
		result[0].row, result[1].row = start, end
	} else {
		result[0].column, result[1].column = start, end
	}
	return result, true
}

// TransformPosition moves one row or column index through a structural change.
// The bool is false when a deletion removes that index. It is exported so sheet
// features holding a bare index — a filter criterion's column, a stored row
// height — move exactly the way formula references move.
func TransformPosition(position int, change StructuralChange) (int, bool) {
	return transformStructuralPosition(position, change)
}

func transformStructuralPosition(position int, change StructuralChange) (int, bool) {
	if change.Action == "move" {
		return movedPosition(position, change), true
	}
	if change.Action == "insert" {
		if position >= change.Index {
			position += change.Count
		}
		limit := MaxRows
		if change.Axis == "column" {
			limit = MaxColumns
		}
		return position, position <= limit
	}
	deletedEnd := change.Index + change.Count - 1
	if position >= change.Index && position <= deletedEnd {
		return 0, false
	}
	if position > deletedEnd {
		position -= change.Count
	}
	return position, true
}

// TransformInterval moves a row or column span through an insertion or a
// deletion. It is exported so sheet features that own their own spans — the
// outline groups, for one — follow exactly the rules formulas follow.
func TransformInterval(start, end int, change StructuralChange) (int, int, bool) {
	return transformStructuralInterval(start, end, change)
}

func transformStructuralInterval(start, end int, change StructuralChange) (int, int, bool) {
	if start > end {
		start, end = end, start
	}
	if change.Action == "move" {
		start, end = movedInterval(start, end, change)
		return start, end, true
	}
	if change.Action == "insert" {
		switch {
		case change.Index <= start:
			return start + change.Count, end + change.Count, true
		case change.Index <= end:
			return start, end + change.Count, true
		default:
			return start, end, true
		}
	}
	deletedEnd := change.Index + change.Count - 1
	if end < change.Index {
		return start, end, true
	}
	if start > deletedEnd {
		return start - change.Count, end - change.Count, true
	}
	leftExists, rightExists := start < change.Index, end > deletedEnd
	if !leftExists && !rightExists {
		return 0, 0, false
	}
	nextStart := start
	if !leftExists {
		nextStart = deletedEnd + 1 - change.Count
	}
	nextEnd := change.Index - 1
	if rightExists {
		nextEnd = end - change.Count
	}
	return nextStart, nextEnd, true
}

// movedPosition applies the three-segment permutation a move describes: the
// moved band lands at its destination and whatever the band passed over slides
// back by Count. Every index keeps a home, so a move never yields #REF!.
func movedPosition(position int, change StructuralChange) int {
	bandEnd := change.Index + change.Count - 1
	if change.Destination > bandEnd {
		landing := change.Destination - change.Count
		switch {
		case position >= change.Index && position <= bandEnd:
			return position + landing - change.Index
		case position > bandEnd && position < change.Destination:
			return position - change.Count
		}
		return position
	}
	if change.Destination < change.Index {
		switch {
		case position >= change.Index && position <= bandEnd:
			return position - (change.Index - change.Destination)
		case position >= change.Destination && position < change.Index:
			return position + change.Count
		}
		return position
	}
	return position
}

// movedInterval maps a span through a move. A move can scatter a contiguous
// span, and a formula range has to stay contiguous, so the result is the span
// covering everything the original covered: exact when the moved band sits
// wholly inside or wholly outside the span, and a superset when a move tears a
// span in half. Widening keeps every original cell in the range, which is what
// a SUM over the span means; dropping cells silently would change the answer.
func movedInterval(start, end int, change StructuralChange) (int, int) {
	low, high, seen := 0, 0, false
	cover := func(first, last, shift int) {
		if first < start {
			first = start
		}
		if last > end {
			last = end
		}
		if first > last {
			return
		}
		first, last = first+shift, last+shift
		if !seen {
			low, high, seen = first, last, true
			return
		}
		if first < low {
			low = first
		}
		if last > high {
			high = last
		}
	}
	bandEnd := change.Index + change.Count - 1
	if change.Destination > bandEnd {
		cover(change.Index, bandEnd, change.Destination-change.Count-change.Index)
		cover(bandEnd+1, change.Destination-1, -change.Count)
		cover(1, change.Index-1, 0)
		cover(change.Destination, MaxRows, 0)
	} else if change.Destination < change.Index {
		cover(change.Index, bandEnd, change.Destination-change.Index)
		cover(change.Destination, change.Index-1, change.Count)
		cover(1, change.Destination-1, 0)
		cover(bandEnd+1, MaxRows, 0)
	} else {
		cover(start, end, 0)
	}
	if !seen {
		return start, end
	}
	return low, high
}

func renderStructuralCell(cell structuralCell) string {
	var result strings.Builder
	if cell.columnAbsolute {
		result.WriteByte('$')
	}
	result.WriteString(columnName(cell.column))
	if cell.rowAbsolute {
		result.WriteByte('$')
	}
	result.WriteString(strconv.Itoa(cell.row))
	return result.String()
}

func formulaIdentifierStart(character rune) bool {
	return unicode.IsLetter(character) || character == '_' || character == '$'
}

func formulaIdentifierEnd(input string, start int) int {
	index := start
	for index < len(input) {
		character, size := utf8.DecodeRuneInString(input[index:])
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' && character != '.' && character != '$' {
			break
		}
		index += size
	}
	return index
}

func skipFormulaSpaces(input string, start int) int {
	index := start
	for index < len(input) {
		character, size := utf8.DecodeRuneInString(input[index:])
		if !unicode.IsSpace(character) {
			break
		}
		index += size
	}
	return index
}
