package formula

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// arrayValue preserves spreadsheet row and column shape while remaining an
// internal evaluator value. Results crossing the API or workbook boundary are
// converted to [][]any by publicValue.
type arrayValue struct {
	rows    int
	columns int
	values  []any
}

func isArrayOperand(value any) bool {
	switch value.(type) {
	case arrayValue, []any, [][]any:
		return true
	default:
		return false
	}
}

func evaluateUnary(operator string, value any) (any, error) {
	if !isArrayOperand(value) {
		return evaluateScalarUnary(operator, value)
	}
	selected, err := toArray(value)
	if err != nil {
		return nil, err
	}
	result := arrayValue{rows: selected.rows, columns: selected.columns, values: make([]any, len(selected.values))}
	for index, item := range selected.values {
		result.values[index], err = evaluateScalarUnary(operator, item)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func evaluateScalarUnary(operator string, value any) (any, error) {
	number, ok := toNumber(value)
	if !ok {
		return nil, formulaError("#VALUE!", "unary operator requires a number")
	}
	if operator == "-" {
		return -number, nil
	}
	return number, nil
}

func evaluateBinary(operator string, left, right any) (any, error) {
	if !isArrayOperand(left) && !isArrayOperand(right) {
		return evaluateScalarBinary(operator, left, right)
	}
	leftArray, err := toArray(left)
	if err != nil {
		return nil, err
	}
	rightArray, err := toArray(right)
	if err != nil {
		return nil, err
	}
	rows, columns := leftArray.rows, leftArray.columns
	leftScalar := leftArray.rows == 1 && leftArray.columns == 1
	rightScalar := rightArray.rows == 1 && rightArray.columns == 1
	if leftScalar {
		rows, columns = rightArray.rows, rightArray.columns
	} else if !rightScalar && !sameShape(leftArray, rightArray) {
		return nil, formulaError("#VALUE!", "array operands must have the same shape")
	}
	result := arrayValue{rows: rows, columns: columns, values: make([]any, rows*columns)}
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			var leftValue, rightValue any
			if leftScalar {
				leftValue = leftArray.at(0, 0)
			} else {
				leftValue = leftArray.at(row, column)
			}
			if rightScalar {
				rightValue = rightArray.at(0, 0)
			} else {
				rightValue = rightArray.at(row, column)
			}
			result.values[row*columns+column], err = evaluateScalarBinary(operator, leftValue, rightValue)
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func evaluateScalarBinary(operator string, left, right any) (any, error) {
	switch operator {
	case "&":
		return display(left) + display(right), nil
	case "=", "<>", "<", ">", "<=", ">=":
		comparison := compare(left, right)
		switch operator {
		case "=":
			return comparison == 0, nil
		case "<>":
			return comparison != 0, nil
		case "<":
			return comparison < 0, nil
		case ">":
			return comparison > 0, nil
		case "<=":
			return comparison <= 0, nil
		default:
			return comparison >= 0, nil
		}
	}
	if operator == "+" || operator == "-" {
		if result, err, handled := dateArithmetic(operator, left, right); handled {
			return result, err
		}
	}
	a, aok := toNumber(left)
	b, bok := toNumber(right)
	if !aok || !bok {
		return nil, formulaError("#VALUE!", "arithmetic requires numbers")
	}
	switch operator {
	case "+":
		return a + b, nil
	case "-":
		return a - b, nil
	case "*":
		return a * b, nil
	case "/":
		if b == 0 {
			return nil, formulaError("#DIV/0!", "division by zero")
		}
		return a / b, nil
	case "^":
		return math.Pow(a, b), nil
	default:
		return nil, formulaError("#ERROR!", "unknown operator")
	}
}

func (value arrayValue) at(row, column int) any {
	return value.values[row*value.columns+column]
}

func (value arrayValue) matrix() [][]any {
	result := make([][]any, value.rows)
	for row := 0; row < value.rows; row++ {
		result[row] = make([]any, value.columns)
		for column := 0; column < value.columns; column++ {
			result[row][column] = publicValue(value.at(row, column))
		}
	}
	return result
}

func toArray(value any) (arrayValue, error) {
	switch typed := value.(type) {
	case arrayValue:
		return typed, nil
	case [][]any:
		return matrixArray(typed)
	case []any:
		if len(typed) == 0 {
			return arrayValue{}, nil
		}
		rows := make([][]any, 0, len(typed))
		matrix := true
		for _, item := range typed {
			switch row := item.(type) {
			case []any:
				rows = append(rows, row)
			case arrayValue:
				if row.rows != 1 {
					matrix = false
					break
				}
				rows = append(rows, append([]any(nil), row.values...))
			default:
				matrix = false
			}
			if !matrix {
				break
			}
		}
		if matrix {
			return matrixArray(rows)
		}
		return arrayValue{rows: len(typed), columns: 1, values: append([]any(nil), typed...)}, nil
	default:
		return arrayValue{rows: 1, columns: 1, values: []any{value}}, nil
	}
}

func matrixArray(rows [][]any) (arrayValue, error) {
	if len(rows) == 0 {
		return arrayValue{}, nil
	}
	columns := len(rows[0])
	values := make([]any, 0, len(rows)*columns)
	for _, row := range rows {
		if len(row) != columns {
			return arrayValue{}, formulaError("#VALUE!", "array rows must have the same number of columns")
		}
		values = append(values, row...)
	}
	return arrayValue{rows: len(rows), columns: columns, values: values}, nil
}

func scalarValue(value any) (any, error) {
	array, err := toArray(value)
	if err != nil {
		return nil, err
	}
	if array.rows != 1 || array.columns != 1 {
		return nil, formulaError("#VALUE!", "a scalar value is required")
	}
	return array.at(0, 0), nil
}

func integerValue(value any, name string) (int, error) {
	scalar, err := scalarValue(value)
	if err != nil {
		return 0, err
	}
	number, ok := toNumber(scalar)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
		return 0, formulaError("#VALUE!", name+" must be an integer")
	}
	return int(number), nil
}

func booleanValue(value any, name string) (bool, error) {
	scalar, err := scalarValue(value)
	if err != nil {
		return false, err
	}
	switch typed := scalar.(type) {
	case bool:
		return typed, nil
	case string:
		parsed, parseErr := strconv.ParseBool(strings.TrimSpace(typed))
		if parseErr == nil {
			return parsed, nil
		}
	}
	if number, ok := toNumber(scalar); ok {
		return number != 0, nil
	}
	return false, formulaError("#VALUE!", name+" must be TRUE or FALSE")
}

func evaluateConditionalAggregate(name string, arguments []any) (any, error) {
	switch name {
	case "COUNTIF":
		if len(arguments) != 2 {
			return nil, argError(name)
		}
		criteriaRange, err := toArray(arguments[0])
		if err != nil {
			return nil, err
		}
		criterion, err := compileCriterion(arguments[1])
		if err != nil {
			return nil, err
		}
		count := 0
		for _, value := range criteriaRange.values {
			if criterion(value) {
				count++
			}
		}
		return float64(count), nil
	case "SUMIF":
		if len(arguments) < 2 || len(arguments) > 3 {
			return nil, argError(name)
		}
		criteriaRange, err := toArray(arguments[0])
		if err != nil {
			return nil, err
		}
		sumRange := criteriaRange
		if len(arguments) == 3 && !omitted(arguments[2]) {
			sumRange, err = toArray(arguments[2])
			if err != nil {
				return nil, err
			}
		}
		if !sameShape(criteriaRange, sumRange) {
			return nil, formulaError("#VALUE!", "SUMIF ranges must have the same shape")
		}
		criterion, err := compileCriterion(arguments[1])
		if err != nil {
			return nil, err
		}
		return sumMatching(sumRange, []arrayValue{criteriaRange}, []criterionMatcher{criterion}), nil
	case "COUNTIFS":
		if len(arguments) < 2 || len(arguments)%2 != 0 {
			return nil, argError(name)
		}
		ranges, criteria, err := conditionalPairs(arguments)
		if err != nil {
			return nil, err
		}
		count := 0
		for index := range ranges[0].values {
			if criteriaMatchAt(ranges, criteria, index) {
				count++
			}
		}
		return float64(count), nil
	case "SUMIFS":
		if len(arguments) < 3 || len(arguments)%2 != 1 {
			return nil, argError(name)
		}
		sumRange, err := toArray(arguments[0])
		if err != nil {
			return nil, err
		}
		ranges, criteria, err := conditionalPairs(arguments[1:])
		if err != nil {
			return nil, err
		}
		if len(ranges) == 0 || !sameShape(sumRange, ranges[0]) {
			return nil, formulaError("#VALUE!", "SUMIFS ranges must have the same shape")
		}
		return sumMatching(sumRange, ranges, criteria), nil
	default:
		return nil, formulaError("#NAME?", "unknown conditional aggregate "+name)
	}
}

type criterionMatcher func(any) bool

func conditionalPairs(arguments []any) ([]arrayValue, []criterionMatcher, error) {
	ranges := make([]arrayValue, 0, len(arguments)/2)
	criteria := make([]criterionMatcher, 0, len(arguments)/2)
	for index := 0; index < len(arguments); index += 2 {
		selected, err := toArray(arguments[index])
		if err != nil {
			return nil, nil, err
		}
		if len(ranges) > 0 && !sameShape(ranges[0], selected) {
			return nil, nil, formulaError("#VALUE!", "criteria ranges must have the same shape")
		}
		matcher, err := compileCriterion(arguments[index+1])
		if err != nil {
			return nil, nil, err
		}
		ranges = append(ranges, selected)
		criteria = append(criteria, matcher)
	}
	return ranges, criteria, nil
}

func sameShape(left, right arrayValue) bool {
	return left.rows == right.rows && left.columns == right.columns
}

func criteriaMatchAt(ranges []arrayValue, criteria []criterionMatcher, index int) bool {
	for criterionIndex, selected := range ranges {
		if !criteria[criterionIndex](selected.values[index]) {
			return false
		}
	}
	return true
}

func sumMatching(sumRange arrayValue, ranges []arrayValue, criteria []criterionMatcher) float64 {
	result := 0.0
	for index, value := range sumRange.values {
		if !criteriaMatchAt(ranges, criteria, index) || value == nil {
			continue
		}
		if number, ok := toNumber(value); ok {
			result += number
		}
	}
	return result
}

func compileCriterion(value any) (criterionMatcher, error) {
	scalar, err := scalarValue(value)
	if err != nil {
		return nil, err
	}
	if scalar == nil {
		return func(candidate any) bool { return candidate == nil || display(candidate) == "" }, nil
	}
	text, isText := scalar.(string)
	if !isText {
		return func(candidate any) bool { return compare(candidate, scalar) == 0 }, nil
	}
	operator, operand := "=", text
	for _, candidate := range []string{"<=", ">=", "<>", "=", "<", ">"} {
		if strings.HasPrefix(operand, candidate) {
			operator, operand = candidate, operand[len(candidate):]
			break
		}
	}
	operand = strings.TrimSpace(operand)
	var expected any = operand
	if number, parseErr := strconv.ParseFloat(operand, 64); parseErr == nil {
		expected = number
	} else if boolean, parseErr := strconv.ParseBool(operand); parseErr == nil {
		expected = boolean
	}
	wildcard, wildcardErr := wildcardExpression(operand)
	if wildcardErr != nil {
		return nil, formulaError("#VALUE!", wildcardErr.Error())
	}
	return func(candidate any) bool {
		comparison := compare(candidate, expected)
		matched := comparison == 0
		if wildcard != nil && (operator == "=" || operator == "<>") {
			matched = wildcard.MatchString(display(candidate))
		}
		switch operator {
		case "=":
			return matched
		case "<>":
			return !matched
		case "<":
			return comparison < 0
		case ">":
			return comparison > 0
		case "<=":
			return comparison <= 0
		default:
			return comparison >= 0
		}
	}, nil
}

func wildcardExpression(pattern string) (*regexp.Regexp, error) {
	var builder strings.Builder
	hasWildcard := false
	builder.WriteString("(?i)^")
	runes := []rune(pattern)
	for index := 0; index < len(runes); index++ {
		switch runes[index] {
		case '~':
			if index+1 < len(runes) && (runes[index+1] == '*' || runes[index+1] == '?' || runes[index+1] == '~') {
				hasWildcard = true
				index++
				builder.WriteString(regexp.QuoteMeta(string(runes[index])))
				continue
			}
			builder.WriteString(regexp.QuoteMeta("~"))
		case '*':
			hasWildcard = true
			builder.WriteString(".*")
		case '?':
			hasWildcard = true
			builder.WriteString(".")
		default:
			builder.WriteString(regexp.QuoteMeta(string(runes[index])))
		}
	}
	if !hasWildcard {
		return nil, nil
	}
	builder.WriteString("$")
	return regexp.Compile(builder.String())
}

func evaluateLookup(name string, arguments []any) (any, error) {
	switch name {
	case "VLOOKUP":
		return evaluateTableLookup(name, arguments, false)
	case "HLOOKUP":
		return evaluateTableLookup(name, arguments, true)
	case "INDEX":
		return evaluateIndex(arguments)
	case "MATCH":
		return evaluateMatch(arguments)
	default:
		return nil, formulaError("#NAME?", "unknown lookup "+name)
	}
}

func evaluateTableLookup(name string, arguments []any, horizontal bool) (any, error) {
	if len(arguments) < 3 || len(arguments) > 4 {
		return nil, argError(name)
	}
	lookup, err := scalarValue(arguments[0])
	if err != nil {
		return nil, err
	}
	table, err := toArray(arguments[1])
	if err != nil {
		return nil, err
	}
	index, err := integerValue(arguments[2], name+" index")
	if err != nil {
		return nil, err
	}
	maximum := table.columns
	if horizontal {
		maximum = table.rows
	}
	if index < 1 || index > maximum || table.rows == 0 || table.columns == 0 {
		return nil, formulaError("#REF!", name+" index is outside the table")
	}
	approximate := true
	if len(arguments) == 4 && !omitted(arguments[3]) {
		approximate, err = booleanValue(arguments[3], name+" range lookup")
		if err != nil {
			return nil, err
		}
	}
	length := table.rows
	if horizontal {
		length = table.columns
	}
	match := -1
	for position := 0; position < length; position++ {
		candidate := table.at(position, 0)
		if horizontal {
			candidate = table.at(0, position)
		}
		if exactLookupMatch(candidate, lookup) {
			match = position
			break
		}
		if approximate && compare(candidate, lookup) <= 0 {
			match = position
		}
	}
	if match < 0 {
		return nil, formulaError("#N/A", name+" did not find a match")
	}
	if horizontal {
		return table.at(index-1, match), nil
	}
	return table.at(match, index-1), nil
}

func exactLookupMatch(candidate, lookup any) bool {
	text, ok := lookup.(string)
	if !ok {
		return compare(candidate, lookup) == 0
	}
	wildcard, err := wildcardExpression(text)
	if err == nil && wildcard != nil {
		return wildcard.MatchString(display(candidate))
	}
	return compare(candidate, lookup) == 0
}

func evaluateIndex(arguments []any) (any, error) {
	if len(arguments) < 2 || len(arguments) > 3 {
		return nil, argError("INDEX")
	}
	selected, err := toArray(arguments[0])
	if err != nil {
		return nil, err
	}
	// A skipped row or column means "the whole of it", which is how
	// `INDEX(A1:C9,,2)` returns a column rather than one cell.
	row := 0
	if !omitted(arguments[1]) {
		if row, err = integerValue(arguments[1], "INDEX row"); err != nil {
			return nil, err
		}
	}
	column := 1
	if len(arguments) == 2 && selected.rows == 1 {
		column, row = row, 1
	} else if len(arguments) == 3 {
		column = 0
		if !omitted(arguments[2]) {
			if column, err = integerValue(arguments[2], "INDEX column"); err != nil {
				return nil, err
			}
		}
	}
	if row < 0 || column < 0 || row > selected.rows || column > selected.columns || selected.rows == 0 || selected.columns == 0 {
		return nil, formulaError("#REF!", "INDEX is outside the array")
	}
	if row == 0 && column == 0 {
		return selected, nil
	}
	if row == 0 {
		values := make([]any, selected.rows)
		for index := range values {
			values[index] = selected.at(index, column-1)
		}
		return arrayValue{rows: selected.rows, columns: 1, values: values}, nil
	}
	if column == 0 {
		values := make([]any, selected.columns)
		for index := range values {
			values[index] = selected.at(row-1, index)
		}
		return arrayValue{rows: 1, columns: selected.columns, values: values}, nil
	}
	return selected.at(row-1, column-1), nil
}

func evaluateMatch(arguments []any) (any, error) {
	if len(arguments) < 2 || len(arguments) > 3 {
		return nil, argError("MATCH")
	}
	lookup, err := scalarValue(arguments[0])
	if err != nil {
		return nil, err
	}
	selected, err := toArray(arguments[1])
	if err != nil {
		return nil, err
	}
	if selected.rows > 1 && selected.columns > 1 {
		return nil, formulaError("#N/A", "MATCH requires one row or one column")
	}
	matchType := 1
	if len(arguments) == 3 && !omitted(arguments[2]) {
		matchType, err = integerValue(arguments[2], "MATCH type")
		if err != nil {
			return nil, err
		}
	}
	if matchType != -1 && matchType != 0 && matchType != 1 {
		return nil, formulaError("#VALUE!", "MATCH type must be -1, 0, or 1")
	}
	position := -1
	for index, candidate := range selected.values {
		if matchType == 0 {
			if exactLookupMatch(candidate, lookup) {
				position = index
				break
			}
		} else if matchType == 1 && compare(candidate, lookup) <= 0 {
			position = index
		} else if matchType == -1 && compare(candidate, lookup) >= 0 {
			position = index
		}
	}
	if position < 0 {
		return nil, formulaError("#N/A", "MATCH did not find a value")
	}
	return float64(position + 1), nil
}

func evaluateFilter(arguments []any) (any, error) {
	if len(arguments) < 2 {
		return nil, argError("FILTER")
	}
	selected, err := toArray(arguments[0])
	if err != nil {
		return nil, err
	}
	if selected.rows == 0 || selected.columns == 0 {
		return nil, formulaError("#N/A", "FILTER source is empty")
	}
	conditions := make([]arrayValue, 0, len(arguments)-1)
	axis := ""
	var fallback any
	hasFallback := false
	for index, argument := range arguments[1:] {
		condition, conditionErr := toArray(argument)
		if conditionErr != nil {
			return nil, conditionErr
		}
		conditionAxis := ""
		switch {
		case condition.rows == selected.rows && condition.columns == 1:
			conditionAxis = "rows"
		case condition.rows == 1 && condition.columns == selected.columns:
			conditionAxis = "columns"
		case condition.rows == 1 && condition.columns == 1 && len(arguments) == 2:
			conditionAxis = "all"
		}
		if conditionAxis == "" || (axis != "" && conditionAxis != axis) {
			if index == len(arguments)-2 {
				fallback, err = scalarValue(argument)
				if err != nil {
					return nil, formulaError("#VALUE!", "FILTER fallback must be a scalar")
				}
				hasFallback = true
				continue
			}
			return nil, formulaError("#VALUE!", "FILTER conditions must match rows or columns")
		}
		if axis == "" {
			axis = conditionAxis
		}
		conditions = append(conditions, condition)
	}
	if len(conditions) == 0 {
		return nil, formulaError("#VALUE!", "FILTER requires a compatible condition")
	}
	if axis == "all" {
		if truthy(conditions[0].at(0, 0)) {
			return selected, nil
		}
		return emptyFilterResult(fallback, hasFallback)
	}
	if axis == "rows" {
		values := make([]any, 0, len(selected.values))
		rows := 0
		for row := 0; row < selected.rows; row++ {
			include := true
			for _, condition := range conditions {
				include = include && truthy(condition.at(row, 0))
			}
			if include {
				rows++
				for column := 0; column < selected.columns; column++ {
					values = append(values, selected.at(row, column))
				}
			}
		}
		if rows == 0 {
			return emptyFilterResult(fallback, hasFallback)
		}
		return arrayValue{rows: rows, columns: selected.columns, values: values}, nil
	}
	values := make([]any, 0, len(selected.values))
	columns := 0
	for column := 0; column < selected.columns; column++ {
		include := true
		for _, condition := range conditions {
			include = include && truthy(condition.at(0, column))
		}
		if include {
			columns++
			for row := 0; row < selected.rows; row++ {
				values = append(values, selected.at(row, column))
			}
		}
	}
	if columns == 0 {
		return emptyFilterResult(fallback, hasFallback)
	}
	// Column selection was appended column-major; normalize to row-major.
	rowMajor := make([]any, 0, len(values))
	for row := 0; row < selected.rows; row++ {
		for column := 0; column < columns; column++ {
			rowMajor = append(rowMajor, values[column*selected.rows+row])
		}
	}
	return arrayValue{rows: selected.rows, columns: columns, values: rowMajor}, nil
}

func emptyFilterResult(fallback any, hasFallback bool) (any, error) {
	if hasFallback {
		return fallback, nil
	}
	return nil, formulaError("#N/A", "FILTER returned no values")
}

type sortSpec struct {
	index      int
	descending bool
}

func evaluateSort(arguments []any) (any, error) {
	if len(arguments) < 1 {
		return nil, argError("SORT")
	}
	selected, err := toArray(arguments[0])
	if err != nil {
		return nil, err
	}
	if selected.rows == 0 || selected.columns == 0 {
		return selected, nil
	}
	byColumn := false
	specs := []sortSpec{{index: 1}}
	if len(arguments) > 1 {
		specs = nil
		googleStyle := len(arguments) > 4
		if len(arguments) >= 3 && !omitted(arguments[2]) {
			if scalar, scalarErr := scalarValue(arguments[2]); scalarErr == nil {
				_, googleStyle = scalar.(bool)
			}
		}
		if googleStyle {
			if (len(arguments)-1)%2 != 0 {
				return nil, argError("SORT")
			}
			for index := 1; index < len(arguments); index += 2 {
				sortIndex, indexErr := integerValue(arguments[index], "SORT index")
				if indexErr != nil {
					return nil, indexErr
				}
				ascending, boolErr := booleanValue(arguments[index+1], "SORT ascending")
				if boolErr != nil {
					return nil, boolErr
				}
				specs = append(specs, sortSpec{index: sortIndex, descending: !ascending})
			}
		} else {
			sortIndex, indexErr := integerValue(arguments[1], "SORT index")
			if indexErr != nil {
				return nil, indexErr
			}
			order := 1
			if len(arguments) >= 3 && !omitted(arguments[2]) {
				order, err = integerValue(arguments[2], "SORT order")
				if err != nil {
					return nil, err
				}
				if order != 1 && order != -1 {
					return nil, formulaError("#VALUE!", "SORT order must be 1 or -1")
				}
			}
			if len(arguments) == 4 && !omitted(arguments[3]) {
				byColumn, err = booleanValue(arguments[3], "SORT by column")
				if err != nil {
					return nil, err
				}
			} else if len(arguments) > 4 {
				return nil, argError("SORT")
			}
			specs = append(specs, sortSpec{index: sortIndex, descending: order == -1})
		}
	}
	maximum := selected.columns
	length := selected.rows
	if byColumn {
		maximum = selected.rows
		length = selected.columns
	}
	for _, spec := range specs {
		if spec.index < 1 || spec.index > maximum {
			return nil, formulaError("#VALUE!", "SORT index is outside the array")
		}
	}
	order := make([]int, length)
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool {
		leftPosition, rightPosition := order[left], order[right]
		for _, spec := range specs {
			leftValue, rightValue := selected.at(leftPosition, spec.index-1), selected.at(rightPosition, spec.index-1)
			if byColumn {
				leftValue, rightValue = selected.at(spec.index-1, leftPosition), selected.at(spec.index-1, rightPosition)
			}
			comparison := compare(leftValue, rightValue)
			if comparison == 0 {
				continue
			}
			if spec.descending {
				return comparison > 0
			}
			return comparison < 0
		}
		return false
	})
	values := make([]any, 0, len(selected.values))
	if byColumn {
		for row := 0; row < selected.rows; row++ {
			for _, column := range order {
				values = append(values, selected.at(row, column))
			}
		}
	} else {
		for _, row := range order {
			for column := 0; column < selected.columns; column++ {
				values = append(values, selected.at(row, column))
			}
		}
	}
	return arrayValue{rows: selected.rows, columns: selected.columns, values: values}, nil
}
