package formula

import (
	"math"
	"strings"
	"unicode"

	"kanpic/pkg/cellrange"
)

// referenceFunction resolves the functions that talk about cells rather than
// values. They are built while parsing, which is what lets ROW(A5) see the
// reference itself and lets a constant OFFSET or INDIRECT join the dependency
// graph like any ordinary range.
func (p *parser) referenceFunction(name string, arguments []node) (node, bool, error) {
	switch name {
	case "ROW", "COLUMN":
		row, column := 0, 0
		switch len(arguments) {
		case 0:
			selected, err := cellrange.Parse(p.anchorAddress())
			if err != nil {
				return nil, true, formulaError("#N/A", name+"() needs to be used inside a cell")
			}
			row, column = selected.Start.Row, selected.Start.Column
		case 1:
			_, first, _, _, ok := nodeReference(arguments[0])
			if !ok {
				return nil, true, formulaError("#VALUE!", name+" requires a cell reference")
			}
			row, column = first.Row, first.Column
		default:
			return nil, true, argError(name)
		}
		if name == "ROW" {
			return literalNode{float64(row)}, true, nil
		}
		return literalNode{float64(column)}, true, nil
	case "OFFSET":
		if len(arguments) < 3 || len(arguments) > 5 {
			return nil, true, argError(name)
		}
		sheetID, first, rows, columns, ok := nodeReference(arguments[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "OFFSET requires a cell reference")
		}
		offsets, constant := constantIntegers(arguments[1:])
		if !constant {
			return dynamicReferenceNode{kind: name, arguments: arguments, sheetID: sheetID, base: first}, true, nil
		}
		height, width := rows, columns
		if len(offsets) >= 3 && offsets[2] != offsetSkipped {
			height = offsets[2]
		}
		if len(offsets) == 4 && offsets[3] != offsetSkipped {
			width = offsets[3]
		}
		offsets[0], offsets[1] = skippedAsZero(offsets[0]), skippedAsZero(offsets[1])
		startRow, startColumn := first.Row+offsets[0], first.Column+offsets[1]
		if startRow < 1 || startColumn < 1 || height < 1 || width < 1 {
			return nil, true, formulaError("#REF!", "OFFSET moved outside the sheet")
		}
		result, err := p.buildRangeAt(sheetID, startRow, startColumn, startRow+height-1, startColumn+width-1)
		return result, true, err
	case "IMPORTRANGE":
		result, err := p.evaluateImportRange(arguments)
		return result, true, err
	case "WEBSERVICE", "IMPORTDATA":
		result, err := p.evaluateExternal(name, arguments)
		return result, true, err
	case "INDIRECT":
		if len(arguments) < 1 || len(arguments) > 2 {
			return nil, true, argError(name)
		}
		literal, isLiteral := arguments[0].(literalNode)
		if !isLiteral {
			return dynamicReferenceNode{kind: name, arguments: arguments, sheetID: p.scope.CurrentSheet}, true, nil
		}
		result, err := p.textReference(display(literal.value))
		return result, true, err
	}
	return nil, false, nil
}

func (p *parser) anchorAddress() string {
	_, address, valid := SplitCellKey(p.scope.Anchor)
	if !valid {
		return ""
	}
	return address
}

// textReference turns "Sheet1!A1:B2" into a real reference so INDIRECT with a
// fixed target behaves exactly like typing the range.
func (p *parser) textReference(text string) (node, error) {
	text = strings.TrimSpace(text)
	qualifier := ""
	if index := strings.LastIndex(text, "!"); index >= 0 {
		qualifier = strings.Trim(strings.TrimSpace(text[:index]), "'")
		text = strings.TrimSpace(text[index+1:])
	}
	sheetID, err := p.sheetFor(qualifier)
	if err != nil {
		return nil, err
	}
	selected, parseErr := cellrange.Parse(strings.ReplaceAll(text, "$", ""))
	if parseErr != nil {
		return nil, formulaError("#REF!", "INDIRECT received "+text+", which is not a reference")
	}
	if selected.Start == selected.End {
		key := CellKey(sheetID, cellrange.Address(selected.Start.Row, selected.Start.Column))
		p.dependencies[key] = struct{}{}
		return referenceNode{key}, nil
	}
	return p.buildRangeAt(sheetID, selected.Start.Row, selected.Start.Column, selected.End.Row, selected.End.Column)
}

// dynamicReferenceNode resolves a reference that is only known once the sheet
// has been calculated. Its dependencies cannot be known in advance, so the
// workbook treats formulas that use it as volatile and recalculates them
// whenever anything changes.
type dynamicReferenceNode struct {
	kind      string
	arguments []node
	sheetID   string
	base      cellrange.Position
}

func (n dynamicReferenceNode) eval(cells map[string]any) (any, error) {
	if n.kind == "INDIRECT" {
		text, err := n.arguments[0].eval(cells)
		if err != nil {
			return nil, err
		}
		return readReference(cells, n.sheetID, display(text))
	}
	offsets := make([]int, 0, 4)
	for _, argument := range n.arguments[1:] {
		value, err := argument.eval(cells)
		if err != nil {
			return nil, err
		}
		if omitted(value) {
			offsets = append(offsets, offsetSkipped)
			continue
		}
		number, ok := toNumber(value)
		if !ok {
			return nil, formulaError("#VALUE!", "OFFSET requires whole numbers")
		}
		offsets = append(offsets, int(number))
	}
	height, width := 1, 1
	if len(offsets) >= 3 && offsets[2] != offsetSkipped {
		height = offsets[2]
	}
	if len(offsets) == 4 && offsets[3] != offsetSkipped {
		width = offsets[3]
	}
	offsets[0], offsets[1] = skippedAsZero(offsets[0]), skippedAsZero(offsets[1])
	startRow, startColumn := n.base.Row+offsets[0], n.base.Column+offsets[1]
	if startRow < 1 || startColumn < 1 || height < 1 || width < 1 {
		return nil, formulaError("#REF!", "OFFSET moved outside the sheet")
	}
	return readRectangle(cells, n.sheetID, startRow, startColumn, startRow+height-1, startColumn+width-1)
}

func readReference(cells map[string]any, sheetID, text string) (any, error) {
	text = strings.TrimSpace(text)
	if index := strings.LastIndex(text, "!"); index >= 0 {
		// A computed sheet name cannot be resolved to an identifier here, so
		// the name is used as given.
		sheetID = strings.ToUpper(strings.Trim(strings.TrimSpace(text[:index]), "'"))
		text = strings.TrimSpace(text[index+1:])
	}
	selected, err := cellrange.Parse(strings.ReplaceAll(text, "$", ""))
	if err != nil {
		return nil, formulaError("#REF!", "INDIRECT received "+text+", which is not a reference")
	}
	return readRectangle(cells, sheetID, selected.Start.Row, selected.Start.Column, selected.End.Row, selected.End.Column)
}

func readRectangle(cells map[string]any, sheetID string, firstRow, firstColumn, lastRow, lastColumn int) (any, error) {
	count := (lastRow - firstRow + 1) * (lastColumn - firstColumn + 1)
	if count > 100_000 {
		return nil, formulaError("#VALUE!", "range is too large")
	}
	values := make([]any, 0, count)
	for row := firstRow; row <= lastRow; row++ {
		for column := firstColumn; column <= lastColumn; column++ {
			value := cells[CellKey(sheetID, cellrange.Address(row, column))]
			if formulaErr, isError := value.(*Error); isError {
				return nil, formulaErr
			}
			values = append(values, value)
		}
	}
	if count == 1 {
		return values[0], nil
	}
	return arrayValue{rows: lastRow - firstRow + 1, columns: lastColumn - firstColumn + 1, values: values}, nil
}

// nodeReference recovers the address behind an already-parsed reference.
func nodeReference(value node) (string, cellrange.Position, int, int, bool) {
	address := ""
	rows, columns := 1, 1
	switch typed := value.(type) {
	case referenceNode:
		address = typed.address
	case rangeNode:
		if len(typed.addresses) == 0 {
			return "", cellrange.Position{}, 0, 0, false
		}
		address, rows, columns = typed.addresses[0], typed.rows, typed.columns
	default:
		return "", cellrange.Position{}, 0, 0, false
	}
	sheetID, cell, valid := SplitCellKey(address)
	if !valid {
		return "", cellrange.Position{}, 0, 0, false
	}
	selected, err := cellrange.Parse(cell)
	if err != nil {
		return "", cellrange.Position{}, 0, 0, false
	}
	return sheetID, selected.Start, rows, columns, true
}

// offsetSkipped stands for an argument OFFSET was not given, which is not the
// same as a zero: `OFFSET(A1,1,1,,2)` keeps the source height.
const offsetSkipped = math.MinInt

func skippedAsZero(value int) int {
	if value == offsetSkipped {
		return 0
	}
	return value
}

func constantIntegers(arguments []node) ([]int, bool) {
	result := make([]int, 0, len(arguments))
	for _, argument := range arguments {
		literal, ok := argument.(literalNode)
		if !ok {
			return nil, false
		}
		if omitted(literal.value) {
			result = append(result, offsetSkipped)
			continue
		}
		number, isNumber := toNumber(literal.value)
		if !isNumber {
			return nil, false
		}
		result = append(result, int(number))
	}
	return result, true
}

// volatileFunctions recalculate on every change because their result does not
// depend only on the cells they name.
var volatileFunctions = []string{"INDIRECT(", "OFFSET(", "RAND(", "RANDBETWEEN(", "RANDARRAY(", "TODAY(", "NOW(", "IMPORTRANGE("}

// IsVolatile reports whether a formula has to be recalculated whenever
// anything in the workbook changes.
func IsVolatile(input string) bool {
	upper := strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return -1
		}
		return unicode.ToUpper(character)
	}, input)
	for _, name := range volatileFunctions {
		if strings.Contains(upper, name) {
			return true
		}
	}
	return false
}
