package formula

import (
	"math"
	"sort"
	"strings"

	"kanpic/pkg/cellrange"
)

// evaluateArray covers the lookup and dynamic-array functions. They all need
// their arguments with the row and column shape intact, so they run before the
// evaluator flattens anything.
func evaluateArray(name string, arguments []any) (any, bool, error) {
	switch name {
	case "ARRAYFORMULA":
		// Operators and functions already broadcast over ranges here, so the
		// wrapper only has to hand back what it was given.
		if len(arguments) != 1 {
			return nil, true, argError(name)
		}
		return arguments[0], true, nil
	case "ADDRESS":
		if len(arguments) < 2 || len(arguments) > 4 {
			return nil, true, argError(name)
		}
		row, err := integerValue(scalarOrFirst(arguments[0]), name)
		if err != nil {
			return nil, true, err
		}
		column, err := integerValue(scalarOrFirst(arguments[1]), name)
		if err != nil {
			return nil, true, err
		}
		if row < 1 || column < 1 {
			return nil, true, formulaError("#VALUE!", "ADDRESS needs a positive row and column")
		}
		kind := 1
		if len(arguments) >= 3 && !omitted(arguments[2]) {
			if kind, err = integerValue(scalarOrFirst(arguments[2]), name); err != nil {
				return nil, true, err
			}
		}
		address := cellrange.Address(row, column)
		letters, digits := address[:len(address)-len(strings.TrimLeft(address, "ABCDEFGHIJKLMNOPQRSTUVWXYZ"))], strings.TrimLeft(address, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		switch kind {
		case 1:
			address = "$" + letters + "$" + digits
		case 2:
			address = letters + "$" + digits
		case 3:
			address = "$" + letters + digits
		}
		if len(arguments) == 4 && !omitted(arguments[3]) {
			if sheet := display(scalarOrFirst(arguments[3])); sheet != "" {
				address = sheet + "!" + address
			}
		}
		return address, true, nil
	case "ROWS", "COLUMNS":
		if len(arguments) != 1 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		if name == "ROWS" {
			return float64(selected.rows), true, nil
		}
		return float64(selected.columns), true, nil
	case "TRANSPOSE":
		if len(arguments) != 1 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		result := arrayValue{rows: selected.columns, columns: selected.rows, values: make([]any, len(selected.values))}
		for row := 0; row < selected.rows; row++ {
			for column := 0; column < selected.columns; column++ {
				result.values[column*result.columns+row] = selected.at(row, column)
			}
		}
		return result, true, nil
	case "FLATTEN", "TOCOL":
		values := make([]any, 0)
		for _, argument := range arguments {
			for _, value := range flatten(argument) {
				if value == nil {
					continue
				}
				values = append(values, value)
			}
		}
		if len(values) == 0 {
			return nil, true, formulaError("#N/A", name+" found no values")
		}
		return arrayValue{rows: len(values), columns: 1, values: values}, true, nil
	case "TOROW":
		values := make([]any, 0)
		for _, argument := range arguments {
			for _, value := range flatten(argument) {
				if value == nil {
					continue
				}
				values = append(values, value)
			}
		}
		if len(values) == 0 {
			return nil, true, formulaError("#N/A", "TOROW found no values")
		}
		return arrayValue{rows: 1, columns: len(values), values: values}, true, nil
	case "VSTACK", "HSTACK":
		if len(arguments) < 1 {
			return nil, true, argError(name)
		}
		parts := make([]arrayValue, 0, len(arguments))
		for _, argument := range arguments {
			if omitted(argument) {
				continue
			}
			selected, err := toArray(argument)
			if err != nil {
				return nil, true, err
			}
			if selected.rows == 0 || selected.columns == 0 {
				continue
			}
			parts = append(parts, selected)
		}
		if len(parts) == 0 {
			return nil, true, formulaError("#N/A", name+" received no values")
		}
		return stackArrays(name, parts)
	case "TAKE", "DROP":
		if len(arguments) < 2 || len(arguments) > 3 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		rows, columns := 0, 0
		if !omitted(arguments[1]) {
			if rows, err = integerValue(scalarOrFirst(arguments[1]), name); err != nil {
				return nil, true, err
			}
		}
		if len(arguments) == 3 && !omitted(arguments[2]) {
			if columns, err = integerValue(scalarOrFirst(arguments[2]), name); err != nil {
				return nil, true, err
			}
		}
		keptRows, err := sliceIndexes(name, selected.rows, rows)
		if err != nil {
			return nil, true, err
		}
		keptColumns, err := sliceIndexes(name, selected.columns, columns)
		if err != nil {
			return nil, true, err
		}
		return pickCells(name, selected, keptRows, keptColumns)
	case "WRAPROWS", "WRAPCOLS":
		if len(arguments) < 2 || len(arguments) > 3 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		// 줄 하나를 접는 함수다. 이미 두 줄 이상인 것을 접으면 무엇을 어떤
		// 차례로 읽었는지 사람이 알 수 없다.
		if selected.rows != 1 && selected.columns != 1 {
			return nil, true, formulaError("#VALUE!", name+" needs a single row or column")
		}
		count, err := integerValue(scalarOrFirst(arguments[1]), name)
		if err != nil {
			return nil, true, err
		}
		if count < 1 {
			return nil, true, formulaError("#NUM!", name+" needs a wrap count of at least 1")
		}
		var padding any
		if len(arguments) == 3 && !omitted(arguments[2]) {
			padding = scalarOrFirst(arguments[2])
		}
		values := append([]any{}, selected.values...)
		if len(values) == 0 {
			return nil, true, formulaError("#N/A", name+" found no values")
		}
		lines := (len(values) + count - 1) / count
		result := arrayValue{rows: lines, columns: count, values: make([]any, lines*count)}
		if name == "WRAPCOLS" {
			result.rows, result.columns = count, lines
		}
		for index := range result.values {
			result.values[index] = padding
		}
		for index, value := range values {
			row, column := index/count, index%count
			if name == "WRAPCOLS" {
				row, column = index%count, index/count
			}
			result.values[row*result.columns+column] = value
		}
		return result, true, nil
	case "EXPAND":
		if len(arguments) < 2 || len(arguments) > 4 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		rows, columns := selected.rows, selected.columns
		if !omitted(arguments[1]) {
			if rows, err = integerValue(scalarOrFirst(arguments[1]), name); err != nil {
				return nil, true, err
			}
		}
		if len(arguments) >= 3 && !omitted(arguments[2]) {
			if columns, err = integerValue(scalarOrFirst(arguments[2]), name); err != nil {
				return nil, true, err
			}
		}
		// 늘리는 함수다. 줄이는 것은 자르는 것이고, 그것은 TAKE 가 한다.
		// 여기서 조용히 잘라 내면 사라진 자료를 아무도 알아채지 못한다.
		if rows < selected.rows || columns < selected.columns {
			return nil, true, formulaError("#VALUE!", name+" cannot make an array smaller")
		}
		var padding any
		if len(arguments) == 4 && !omitted(arguments[3]) {
			padding = scalarOrFirst(arguments[3])
		}
		result := arrayValue{rows: rows, columns: columns, values: make([]any, rows*columns)}
		for index := range result.values {
			result.values[index] = padding
		}
		for row := 0; row < selected.rows; row++ {
			for column := 0; column < selected.columns; column++ {
				result.values[row*columns+column] = selected.at(row, column)
			}
		}
		return result, true, nil
	case "CHOOSEROWS", "CHOOSECOLS":
		if len(arguments) < 2 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		limit := selected.rows
		if name == "CHOOSECOLS" {
			limit = selected.columns
		}
		chosen := make([]int, 0, len(arguments)-1)
		for _, argument := range arguments[1:] {
			for _, value := range flatten(argument) {
				if value == nil {
					continue
				}
				index, indexErr := integerValue(value, name)
				if indexErr != nil {
					return nil, true, indexErr
				}
				if index < 0 {
					index = limit + index + 1
				}
				if index < 1 || index > limit {
					return nil, true, formulaError("#VALUE!", name+" index is outside the array")
				}
				chosen = append(chosen, index-1)
			}
		}
		if len(chosen) == 0 {
			return nil, true, argError(name)
		}
		if name == "CHOOSEROWS" {
			return pickCells(name, selected, chosen, wholeRange(selected.columns))
		}
		return pickCells(name, selected, wholeRange(selected.rows), chosen)
	case "SORTBY":
		return evaluateSortBy(arguments)
	case "UNIQUE":
		if len(arguments) < 1 || len(arguments) > 3 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		return uniqueRows(selected)
	case "SEQUENCE":
		return evaluateSequence(arguments)
	case "CHOOSE":
		if len(arguments) < 2 {
			return nil, true, argError(name)
		}
		index, err := integerValue(scalarOrFirst(arguments[0]), name)
		if err != nil {
			return nil, true, err
		}
		if index < 1 || index > len(arguments)-1 {
			return nil, true, formulaError("#VALUE!", "CHOOSE index is outside the list")
		}
		return arguments[index], true, nil
	case "ARRAY_CONSTRAIN":
		if len(arguments) != 3 {
			return nil, true, argError(name)
		}
		selected, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		rows, err := integerValue(scalarOrFirst(arguments[1]), name)
		if err != nil {
			return nil, true, err
		}
		columns, err := integerValue(scalarOrFirst(arguments[2]), name)
		if err != nil {
			return nil, true, err
		}
		if rows < 1 || columns < 1 {
			return nil, true, formulaError("#VALUE!", "ARRAY_CONSTRAIN needs positive sizes")
		}
		rows, columns = min(rows, selected.rows), min(columns, selected.columns)
		result := arrayValue{rows: rows, columns: columns, values: make([]any, 0, rows*columns)}
		for row := 0; row < rows; row++ {
			for column := 0; column < columns; column++ {
				result.values = append(result.values, selected.at(row, column))
			}
		}
		return result, true, nil
	case "SUMPRODUCT":
		if len(arguments) == 0 {
			return nil, true, argError(name)
		}
		first, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		factors := make([]arrayValue, 0, len(arguments))
		factors = append(factors, first)
		for _, argument := range arguments[1:] {
			selected, arrayErr := toArray(argument)
			if arrayErr != nil {
				return nil, true, arrayErr
			}
			if !sameShape(first, selected) {
				return nil, true, formulaError("#VALUE!", "SUMPRODUCT ranges must have the same shape")
			}
			factors = append(factors, selected)
		}
		total := 0.0
		for index := range first.values {
			product := 1.0
			for _, factor := range factors {
				number, ok := toNumber(factor.values[index])
				if !ok {
					number = 0
				}
				product *= number
			}
			total += product
		}
		return total, true, nil
	case "AGGREGATE":
		return evaluateAggregate(arguments)
	case "SUBTOTAL":
		if len(arguments) < 2 {
			return nil, true, argError(name)
		}
		code, err := integerValue(scalarOrFirst(arguments[0]), name)
		if err != nil {
			return nil, true, err
		}
		return evaluateSubtotal(code, arguments[1:])
	case "QUERY":
		return evaluateQuery(arguments)
	case "SPARKLINE":
		return evaluateSparkline(arguments)
	case "XLOOKUP":
		return evaluateExtendedLookup(arguments)
	case "XMATCH":
		return evaluateExtendedMatch(arguments)
	case "LOOKUP":
		if len(arguments) < 2 || len(arguments) > 3 {
			return nil, true, argError(name)
		}
		search, err := toArray(arguments[1])
		if err != nil {
			return nil, true, err
		}
		results := search
		if len(arguments) == 3 && !omitted(arguments[2]) {
			if results, err = toArray(arguments[2]); err != nil {
				return nil, true, err
			}
		}
		if len(results.values) < len(search.values) {
			return nil, true, formulaError("#N/A", "LOOKUP result range is too small")
		}
		found := -1
		for index, candidate := range search.values {
			if candidate == nil {
				continue
			}
			if compare(candidate, scalarOrFirst(arguments[0])) <= 0 {
				found = index
			}
		}
		if found < 0 {
			return nil, true, formulaError("#N/A", "LOOKUP found no value")
		}
		return results.values[found], true, nil
	}
	return nil, false, nil
}

func uniqueRows(selected arrayValue) (any, bool, error) {
	seen := make(map[string]struct{}, selected.rows)
	values := make([]any, 0, len(selected.values))
	rows := 0
	for row := 0; row < selected.rows; row++ {
		parts := make([]string, 0, selected.columns)
		for column := 0; column < selected.columns; column++ {
			parts = append(parts, display(selected.at(row, column)))
		}
		key := strings.Join(parts, "\x00")
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		rows++
		for column := 0; column < selected.columns; column++ {
			values = append(values, selected.at(row, column))
		}
	}
	if rows == 0 {
		return nil, true, formulaError("#N/A", "UNIQUE found no values")
	}
	if rows == 1 && selected.columns == 1 {
		return values[0], true, nil
	}
	return arrayValue{rows: rows, columns: selected.columns, values: values}, true, nil
}

func evaluateSequence(arguments []any) (any, bool, error) {
	if len(arguments) < 1 || len(arguments) > 4 {
		return nil, true, argError("SEQUENCE")
	}
	rows, err := integerValue(scalarOrFirst(arguments[0]), "SEQUENCE")
	if err != nil {
		return nil, true, err
	}
	columns := 1
	if len(arguments) >= 2 && !omitted(arguments[1]) {
		if columns, err = integerValue(scalarOrFirst(arguments[1]), "SEQUENCE"); err != nil {
			return nil, true, err
		}
	}
	start, step := 1.0, 1.0
	if len(arguments) >= 3 && !omitted(arguments[2]) {
		number, ok := toNumber(scalarOrFirst(arguments[2]))
		if !ok {
			return nil, true, formulaError("#VALUE!", "SEQUENCE start must be a number")
		}
		start = number
	}
	if len(arguments) == 4 && !omitted(arguments[3]) {
		number, ok := toNumber(scalarOrFirst(arguments[3]))
		if !ok {
			return nil, true, formulaError("#VALUE!", "SEQUENCE step must be a number")
		}
		step = number
	}
	if rows < 1 || columns < 1 || rows*columns > 100_000 {
		return nil, true, formulaError("#NUM!", "SEQUENCE size must be between one cell and 100,000")
	}
	values := make([]any, 0, rows*columns)
	for index := 0; index < rows*columns; index++ {
		values = append(values, start+float64(index)*step)
	}
	if rows == 1 && columns == 1 {
		return values[0], true, nil
	}
	return arrayValue{rows: rows, columns: columns, values: values}, true, nil
}

// evaluateSubtotal reproduces the numbered aggregate list. Codes above 100 skip
// hidden rows in Sheets; kanpic has no hidden-row context inside a formula, so
// both forms aggregate the same values.
func evaluateSubtotal(code int, arguments []any) (any, bool, error) {
	values := make([]any, 0)
	for _, argument := range arguments {
		values = append(values, flatten(argument)...)
	}
	if code > 100 {
		code -= 100
	}
	numbers := numericValues(values)
	switch code {
	case 1, 9:
		total := 0.0
		for _, number := range numbers {
			total += number
		}
		if code == 9 {
			return total, true, nil
		}
		if len(numbers) == 0 {
			return nil, true, formulaError("#DIV/0!", "SUBTOTAL average needs numbers")
		}
		return total / float64(len(numbers)), true, nil
	case 2:
		return float64(len(numbers)), true, nil
	case 3:
		count := 0
		for _, value := range values {
			if value != nil && display(value) != "" {
				count++
			}
		}
		return float64(count), true, nil
	case 4, 5:
		if len(numbers) == 0 {
			return float64(0), true, nil
		}
		result := numbers[0]
		for _, number := range numbers[1:] {
			if code == 4 {
				result = math.Max(result, number)
			} else {
				result = math.Min(result, number)
			}
		}
		return result, true, nil
	case 6:
		result := 1.0
		for _, number := range numbers {
			result *= number
		}
		return result, true, nil
	case 7, 8:
		if len(numbers) < 2 && code == 7 {
			return nil, true, formulaError("#DIV/0!", "SUBTOTAL needs at least two numbers")
		}
		return math.Sqrt(populationVariance(numbers, code == 7)), true, nil
	case 10, 11:
		if len(numbers) < 2 && code == 10 {
			return nil, true, formulaError("#DIV/0!", "SUBTOTAL needs at least two numbers")
		}
		return populationVariance(numbers, code == 10), true, nil
	}
	return nil, true, formulaError("#VALUE!", "SUBTOTAL function code must be 1-11 or 101-111")
}

// evaluateExtendedLookup implements XLOOKUP, including the not-found fallback
// and the search modes that make it the replacement for VLOOKUP.
func evaluateExtendedLookup(arguments []any) (any, bool, error) {
	if len(arguments) < 3 || len(arguments) > 6 {
		return nil, true, argError("XLOOKUP")
	}
	search, err := toArray(arguments[1])
	if err != nil {
		return nil, true, err
	}
	results, err := toArray(arguments[2])
	if err != nil {
		return nil, true, err
	}
	matchMode, searchMode := 0, 1
	if len(arguments) >= 5 && !omitted(arguments[4]) {
		if matchMode, err = integerValue(scalarOrFirst(arguments[4]), "XLOOKUP"); err != nil {
			return nil, true, err
		}
	}
	if len(arguments) == 6 && !omitted(arguments[5]) {
		if searchMode, err = integerValue(scalarOrFirst(arguments[5]), "XLOOKUP"); err != nil {
			return nil, true, err
		}
	}
	index := searchIndex(scalarOrFirst(arguments[0]), search, matchMode, searchMode)
	if index < 0 {
		if len(arguments) >= 4 && !omitted(arguments[3]) {
			return arguments[3], true, nil
		}
		return nil, true, formulaError("#N/A", "XLOOKUP found no match")
	}
	// A one-dimensional result range returns one value; a wider one returns the
	// whole matching row or column.
	if search.rows == len(search.values) && results.columns > 1 {
		row := make([]any, 0, results.columns)
		for column := 0; column < results.columns; column++ {
			row = append(row, results.at(index, column))
		}
		return arrayValue{rows: 1, columns: results.columns, values: row}, true, nil
	}
	if search.columns == len(search.values) && results.rows > 1 {
		column := make([]any, 0, results.rows)
		for row := 0; row < results.rows; row++ {
			column = append(column, results.at(row, index))
		}
		return arrayValue{rows: results.rows, columns: 1, values: column}, true, nil
	}
	if index >= len(results.values) {
		return nil, true, formulaError("#VALUE!", "XLOOKUP result range is smaller than the search range")
	}
	return results.values[index], true, nil
}

func evaluateExtendedMatch(arguments []any) (any, bool, error) {
	if len(arguments) < 2 || len(arguments) > 4 {
		return nil, true, argError("XMATCH")
	}
	search, err := toArray(arguments[1])
	if err != nil {
		return nil, true, err
	}
	matchMode, searchMode := 0, 1
	if len(arguments) >= 3 && !omitted(arguments[2]) {
		if matchMode, err = integerValue(scalarOrFirst(arguments[2]), "XMATCH"); err != nil {
			return nil, true, err
		}
	}
	if len(arguments) == 4 && !omitted(arguments[3]) {
		if searchMode, err = integerValue(scalarOrFirst(arguments[3]), "XMATCH"); err != nil {
			return nil, true, err
		}
	}
	index := searchIndex(scalarOrFirst(arguments[0]), search, matchMode, searchMode)
	if index < 0 {
		return nil, true, formulaError("#N/A", "XMATCH found no match")
	}
	return float64(index + 1), true, nil
}

// searchIndex finds a value under XLOOKUP's match and search modes: 0 exact,
// -1 next smaller, 1 next larger, 2 wildcard; search mode -1 scans backwards.
func searchIndex(target any, search arrayValue, matchMode, searchMode int) int {
	order := make([]int, 0, len(search.values))
	for index := range search.values {
		order = append(order, index)
	}
	if searchMode == -1 {
		sort.Sort(sort.Reverse(sort.IntSlice(order)))
	}
	if matchMode == 2 {
		pattern, err := wildcardExpression(display(target))
		if err != nil {
			return -1
		}
		for _, index := range order {
			if pattern.MatchString(display(search.values[index])) {
				return index
			}
		}
		return -1
	}
	best := -1
	var bestValue any
	for _, index := range order {
		candidate := search.values[index]
		if candidate == nil {
			continue
		}
		difference := compare(candidate, target)
		if difference == 0 {
			return index
		}
		if matchMode == -1 && difference < 0 {
			if best < 0 || compare(candidate, bestValue) > 0 {
				best, bestValue = index, candidate
			}
		}
		if matchMode == 1 && difference > 0 {
			if best < 0 || compare(candidate, bestValue) < 0 {
				best, bestValue = index, candidate
			}
		}
	}
	return best
}

// stackArrays joins arrays edge to edge. Excel fills the ragged corner with
// #N/A; this engine has no error value that can sit inside an array, so the
// gap is left empty and the shape is still the union of the parts.
func stackArrays(name string, parts []arrayValue) (any, bool, error) {
	rows, columns := 0, 0
	for _, part := range parts {
		if name == "VSTACK" {
			rows += part.rows
			columns = max(columns, part.columns)
			continue
		}
		rows = max(rows, part.rows)
		columns += part.columns
	}
	if rows*columns > 100_000 {
		return nil, true, formulaError("#NUM!", name+" would produce more than 100,000 cells")
	}
	result := arrayValue{rows: rows, columns: columns, values: make([]any, 0, rows*columns)}
	if name == "VSTACK" {
		for _, part := range parts {
			for row := 0; row < part.rows; row++ {
				for column := 0; column < columns; column++ {
					result.values = append(result.values, cellOrBlank(part, row, column))
				}
			}
		}
		return result, true, nil
	}
	for row := 0; row < rows; row++ {
		for _, part := range parts {
			for column := 0; column < part.columns; column++ {
				result.values = append(result.values, cellOrBlank(part, row, column))
			}
		}
	}
	return result, true, nil
}

func cellOrBlank(part arrayValue, row, column int) any {
	if row >= part.rows || column >= part.columns {
		return nil
	}
	return part.at(row, column)
}

func wholeRange(length int) []int {
	indexes := make([]int, length)
	for index := range indexes {
		indexes[index] = index
	}
	return indexes
}

// sliceIndexes turns a TAKE or DROP count into the positions that survive. A
// negative count works from the far end, and zero means the whole extent.
func sliceIndexes(name string, length, count int) ([]int, error) {
	if count == 0 {
		return wholeRange(length), nil
	}
	size := count
	if size < 0 {
		size = -size
	}
	size = min(size, length)
	if name == "TAKE" {
		if count > 0 {
			return wholeRange(length)[:size], nil
		}
		return wholeRange(length)[length-size:], nil
	}
	if count > 0 {
		return wholeRange(length)[size:], nil
	}
	return wholeRange(length)[:length-size], nil
}

func pickCells(name string, selected arrayValue, rows, columns []int) (any, bool, error) {
	if len(rows) == 0 || len(columns) == 0 {
		return nil, true, formulaError("#N/A", name+" left no values")
	}
	result := arrayValue{rows: len(rows), columns: len(columns), values: make([]any, 0, len(rows)*len(columns))}
	for _, row := range rows {
		for _, column := range columns {
			result.values = append(result.values, selected.at(row, column))
		}
	}
	return result, true, nil
}

// evaluateSortBy orders one array by the values of another. The key decides
// what moves: a column of keys sorts rows, a row of keys sorts columns.
func evaluateSortBy(arguments []any) (any, bool, error) {
	if len(arguments) < 2 {
		return nil, true, argError("SORTBY")
	}
	selected, err := toArray(arguments[0])
	if err != nil {
		return nil, true, err
	}
	if selected.rows == 0 || selected.columns == 0 {
		return selected, true, nil
	}
	type sortByKey struct {
		values     []any
		descending bool
	}
	keys := make([]sortByKey, 0, len(arguments)/2)
	byColumn := false
	for index := 1; index < len(arguments); {
		by, keyErr := toArray(arguments[index])
		if keyErr != nil {
			return nil, true, keyErr
		}
		values := flatten(by)
		if len(keys) == 0 {
			switch {
			case by.columns == 1 && by.rows == selected.rows:
			case by.rows == 1 && by.columns == selected.columns:
				byColumn = true
			default:
				return nil, true, formulaError("#VALUE!", "SORTBY needs one key per row or per column")
			}
		}
		expected := selected.rows
		if byColumn {
			expected = selected.columns
		}
		if len(values) != expected {
			return nil, true, formulaError("#VALUE!", "SORTBY keys must all be the same length")
		}
		descending := false
		index++
		if index < len(arguments) {
			if !omitted(arguments[index]) {
				order, orderErr := integerValue(scalarOrFirst(arguments[index]), "SORTBY")
				if orderErr != nil {
					return nil, true, orderErr
				}
				if order != 1 && order != -1 {
					return nil, true, formulaError("#VALUE!", "SORTBY order must be 1 or -1")
				}
				descending = order == -1
			}
			index++
		}
		keys = append(keys, sortByKey{values: values, descending: descending})
	}
	length := selected.rows
	if byColumn {
		length = selected.columns
	}
	order := wholeRange(length)
	sort.SliceStable(order, func(left, right int) bool {
		for _, key := range keys {
			comparison := compare(key.values[order[left]], key.values[order[right]])
			if comparison == 0 {
				continue
			}
			if key.descending {
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
	return arrayValue{rows: selected.rows, columns: selected.columns, values: values}, true, nil
}

// evaluateAggregate 은 엑셀·시트의 AGGREGATE 이다. SUBTOTAL 과 같은 집계를
// 하되, **오류가 든 칸을 건너뛰라고 시킬 수 있다**.
//
//	=SUM(A1:A100)           칸 하나가 #N/A 면 합계도 #N/A 다
//	=AGGREGATE(9,6,A1:A100) 그 칸만 빼고 더한다
//
// 첫 인수는 집계 방법, 둘째는 무엇을 건너뛸지다.
//
//	1 평균  2 COUNT  3 COUNTA  4 최대  5 최소  6 곱  7 표본표준편차
//	8 모표준편차  9 합  10 표본분산  11 모분산  12 중앙값  13 최빈값
//	14 K번째 큰 값  15 K번째 작은 값  16 백분위수  17 사분위수
//	18 백분위수(경계 제외)  19 사분위수(경계 제외)
//
//	0,1,4,5 오류를 그대로 둔다   2,3,6,7 오류를 건너뛴다
//
// 숨긴 행을 건너뛰라는 값(1,3,5,7)은 받아들이되 따로 다루지 않는다.
// 수식 안에서는 어느 행이 숨겨졌는지 알 수 없기 때문이다. SUBTOTAL 의
// 100번대 코드도 같은 이유로 같은 답을 낸다.
func evaluateAggregate(arguments []any) (any, bool, error) {
	if len(arguments) < 3 {
		return nil, true, argError("AGGREGATE")
	}
	code, err := integerValue(scalarOrFirst(arguments[0]), "AGGREGATE")
	if err != nil {
		return nil, true, err
	}
	options, err := integerValue(scalarOrFirst(arguments[1]), "AGGREGATE")
	if err != nil {
		return nil, true, err
	}
	if code < 1 || code > 19 {
		return nil, true, formulaError("#VALUE!", "AGGREGATE function number must be 1 to 19")
	}
	if options < 0 || options > 7 {
		return nil, true, formulaError("#VALUE!", "AGGREGATE options must be 0 to 7")
	}
	ignoreErrors := options == 2 || options == 3 || options == 6 || options == 7

	// 14~19 는 마지막 인수가 K 다. 나머지는 모두 셈할 값이다.
	data := arguments[2:]
	var extra any
	if code >= 14 {
		if len(data) < 2 {
			return nil, true, argError("AGGREGATE")
		}
		extra, data = data[len(data)-1], data[:len(data)-1]
	}
	values := make([]any, 0)
	for _, argument := range data {
		values = append(values, flatten(argument)...)
	}
	kept := make([]any, 0, len(values))
	for _, value := range values {
		if formulaErr, isError := value.(*Error); isError {
			if !ignoreErrors {
				return nil, true, formulaErr
			}
			continue
		}
		kept = append(kept, value)
	}
	if code <= 11 {
		return evaluateSubtotal(code, []any{arrayValue{rows: len(kept), columns: 1, values: kept}})
	}
	switch code {
	case 12:
		return evaluateAggregateMedian(kept)
	case 13:
		return evaluateStatistics("MODE", kept)
	case 14, 15:
		name := "LARGE"
		if code == 15 {
			name = "SMALL"
		}
		return evaluateStatistics(name, append(append([]any{}, kept...), extra))
	case 16, 17:
		name := "PERCENTILE"
		if code == 17 {
			name = "QUARTILE"
		}
		return evaluateStatistics(name, append(append([]any{}, kept...), extra))
	}
	return evaluateExclusivePercentile(code, kept, extra)
}

func evaluateAggregateMedian(values []any) (any, bool, error) {
	numbers := sortedNumbers(numericValues(values))
	if len(numbers) == 0 {
		return nil, true, formulaError("#NUM!", "AGGREGATE median needs numbers")
	}
	middle := len(numbers) / 2
	if len(numbers)%2 == 1 {
		return numbers[middle], true, nil
	}
	return (numbers[middle-1] + numbers[middle]) / 2, true, nil
}

// evaluateExclusivePercentile 은 경계를 뺀 백분위수다. 자리를 k*(n+1) 로
// 잡으므로 0 번째와 n+1 번째는 자료 밖이 되어 #NUM! 이 된다. 경계를 넣는
// 쪽(16, 17)은 k*(n-1)+1 로 잡아 양 끝이 최소·최대가 된다.
func evaluateExclusivePercentile(code int, values []any, extra any) (any, bool, error) {
	fraction, ok := toNumber(scalarOrFirst(extra))
	if !ok {
		return nil, true, formulaError("#VALUE!", "AGGREGATE requires a number")
	}
	if code == 19 {
		if fraction < 1 || fraction > 3 || fraction != math.Trunc(fraction) {
			return nil, true, formulaError("#NUM!", "AGGREGATE exclusive quartile takes 1 to 3")
		}
		fraction /= 4
	}
	numbers := sortedNumbers(numericValues(values))
	if len(numbers) == 0 {
		return nil, true, formulaError("#NUM!", "AGGREGATE needs numbers")
	}
	position := fraction * float64(len(numbers)+1)
	if position < 1 || position > float64(len(numbers)) {
		return nil, true, formulaError("#NUM!", "AGGREGATE exclusive percentile is outside the data")
	}
	lower := math.Floor(position)
	remainder := position - lower
	index := int(lower) - 1
	if index+1 >= len(numbers) {
		return numbers[index], true, nil
	}
	return numbers[index] + remainder*(numbers[index+1]-numbers[index]), true, nil
}
