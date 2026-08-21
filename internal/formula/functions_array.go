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
		if len(arguments) >= 3 {
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
		if len(arguments) == 4 {
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
		if len(arguments) == 3 {
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
	if len(arguments) >= 2 {
		if columns, err = integerValue(scalarOrFirst(arguments[1]), "SEQUENCE"); err != nil {
			return nil, true, err
		}
	}
	start, step := 1.0, 1.0
	if len(arguments) >= 3 {
		number, ok := toNumber(scalarOrFirst(arguments[2]))
		if !ok {
			return nil, true, formulaError("#VALUE!", "SEQUENCE start must be a number")
		}
		start = number
	}
	if len(arguments) == 4 {
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
	if len(arguments) >= 5 {
		if matchMode, err = integerValue(scalarOrFirst(arguments[4]), "XLOOKUP"); err != nil {
			return nil, true, err
		}
	}
	if len(arguments) == 6 {
		if searchMode, err = integerValue(scalarOrFirst(arguments[5]), "XLOOKUP"); err != nil {
			return nil, true, err
		}
	}
	index := searchIndex(scalarOrFirst(arguments[0]), search, matchMode, searchMode)
	if index < 0 {
		if len(arguments) >= 4 {
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
	if len(arguments) >= 3 {
		if matchMode, err = integerValue(scalarOrFirst(arguments[2]), "XMATCH"); err != nil {
			return nil, true, err
		}
	}
	if len(arguments) == 4 {
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
