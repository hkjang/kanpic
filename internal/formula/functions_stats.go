package formula

import (
	"math"
	"sort"
)

// evaluateConditionalExtra covers the criteria aggregates that sit beside
// SUMIF and COUNTIF in Google Sheets. They share the same shape rules, so a
// mismatched range is reported the same way here as it is there.
func evaluateConditionalExtra(name string, arguments []any) (any, bool, error) {
	switch name {
	case "AVERAGEIF":
		if len(arguments) < 2 || len(arguments) > 3 {
			return nil, true, argError(name)
		}
		criteriaRange, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		valueRange := criteriaRange
		if len(arguments) == 3 {
			if valueRange, err = toArray(arguments[2]); err != nil {
				return nil, true, err
			}
		}
		if !sameShape(criteriaRange, valueRange) {
			return nil, true, formulaError("#VALUE!", "AVERAGEIF ranges must have the same shape")
		}
		criterion, err := compileCriterion(arguments[1])
		if err != nil {
			return nil, true, err
		}
		result, err := matchedNumbers(valueRange, []arrayValue{criteriaRange}, []criterionMatcher{criterion}, "AVERAGE")
		return result, true, err
	case "AVERAGEIFS", "MAXIFS", "MINIFS":
		if len(arguments) < 3 || len(arguments)%2 != 1 {
			return nil, true, argError(name)
		}
		valueRange, err := toArray(arguments[0])
		if err != nil {
			return nil, true, err
		}
		ranges, criteria, err := conditionalPairs(arguments[1:])
		if err != nil {
			return nil, true, err
		}
		if len(ranges) == 0 || !sameShape(valueRange, ranges[0]) {
			return nil, true, formulaError("#VALUE!", name+" ranges must have the same shape")
		}
		mode := "AVERAGE"
		if name == "MAXIFS" {
			mode = "MAX"
		} else if name == "MINIFS" {
			mode = "MIN"
		}
		result, err := matchedNumbers(valueRange, ranges, criteria, mode)
		return result, true, err
	}
	return nil, false, nil
}

func matchedNumbers(values arrayValue, ranges []arrayValue, criteria []criterionMatcher, mode string) (any, error) {
	matched := make([]float64, 0, len(values.values))
	for index, value := range values.values {
		if !criteriaMatchAt(ranges, criteria, index) {
			continue
		}
		if number, ok := toNumber(value); ok && value != nil {
			matched = append(matched, number)
		}
	}
	if len(matched) == 0 {
		if mode == "AVERAGE" {
			return nil, formulaError("#DIV/0!", "no cell matched the criteria")
		}
		// MAXIFS and MINIFS report zero for an empty match, as Sheets does.
		return float64(0), nil
	}
	switch mode {
	case "MAX":
		result := matched[0]
		for _, number := range matched[1:] {
			result = math.Max(result, number)
		}
		return result, nil
	case "MIN":
		result := matched[0]
		for _, number := range matched[1:] {
			result = math.Min(result, number)
		}
		return result, nil
	}
	total := 0.0
	for _, number := range matched {
		total += number
	}
	return total / float64(len(matched)), nil
}

// evaluateStatistics handles the distribution functions, which all work on the
// flattened numbers of their arguments.
func evaluateStatistics(name string, values []any) (any, bool, error) {
	switch name {
	case "STDEV", "STDEVA", "STDEVP", "STDEVPA", "VAR", "VARA", "VARP", "VARPA":
		numbers := numericValues(values)
		sample := name == "STDEV" || name == "STDEVA" || name == "VAR" || name == "VARA"
		if len(numbers) < 2 && sample {
			return nil, true, formulaError("#DIV/0!", name+" needs at least two numbers")
		}
		if len(numbers) == 0 {
			return nil, true, formulaError("#DIV/0!", name+" needs at least one number")
		}
		variance := populationVariance(numbers, sample)
		if name == "VAR" || name == "VARA" || name == "VARP" || name == "VARPA" {
			return variance, true, nil
		}
		return math.Sqrt(variance), true, nil
	case "AVERAGEA":
		if len(values) == 0 {
			return nil, true, formulaError("#DIV/0!", "AVERAGEA needs a value")
		}
		total, count := 0.0, 0
		for _, value := range values {
			if value == nil {
				continue
			}
			number, ok := toNumber(value)
			if !ok {
				number = 0
			}
			total += number
			count++
		}
		if count == 0 {
			return nil, true, formulaError("#DIV/0!", "AVERAGEA needs a value")
		}
		return total / float64(count), true, nil
	case "COUNTUNIQUE":
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if value == nil || display(value) == "" {
				continue
			}
			seen[display(value)] = struct{}{}
		}
		return float64(len(seen)), true, nil
	case "LARGE", "SMALL":
		if len(values) < 2 {
			return nil, true, argError(name)
		}
		position, err := integerValue(values[len(values)-1], name)
		if err != nil {
			return nil, true, err
		}
		numbers := numericValues(values[:len(values)-1])
		if position < 1 || position > len(numbers) {
			return nil, true, formulaError("#NUM!", name+" position is outside the data")
		}
		sorted := sortedNumbers(numbers)
		if name == "SMALL" {
			return sorted[position-1], true, nil
		}
		return sorted[len(sorted)-position], true, nil
	case "MODE":
		numbers := numericValues(values)
		counts := make(map[float64]int, len(numbers))
		best, bestCount := 0.0, 0
		for _, number := range numbers {
			counts[number]++
			if counts[number] > bestCount {
				best, bestCount = number, counts[number]
			}
		}
		if bestCount < 2 {
			return nil, true, formulaError("#N/A", "MODE found no repeated number")
		}
		return best, true, nil
	case "RANK":
		if len(values) < 2 {
			return nil, true, argError(name)
		}
		target, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "RANK requires a number")
		}
		ascending := false
		numbers := values[1:]
		if last, isNumber := toNumber(values[len(values)-1]); isNumber && len(values) > 2 && looksLikeFlag(values[len(values)-1]) {
			ascending = last != 0
			numbers = values[1 : len(values)-1]
		}
		population := numericValues(numbers)
		if len(population) == 0 {
			return nil, true, formulaError("#N/A", "RANK needs numbers to rank against")
		}
		rank := 1
		for _, number := range population {
			if (!ascending && number > target) || (ascending && number < target) {
				rank++
			}
		}
		return float64(rank), true, nil
	case "PERCENTILE", "QUARTILE":
		if len(values) < 2 {
			return nil, true, argError(name)
		}
		fraction, ok := toNumber(values[len(values)-1])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		if name == "QUARTILE" {
			if fraction < 0 || fraction > 4 {
				return nil, true, formulaError("#NUM!", "QUARTILE takes 0 to 4")
			}
			fraction /= 4
		}
		if fraction < 0 || fraction > 1 {
			return nil, true, formulaError("#NUM!", "PERCENTILE takes 0 to 1")
		}
		numbers := sortedNumbers(numericValues(values[:len(values)-1]))
		if len(numbers) == 0 {
			return nil, true, formulaError("#NUM!", name+" needs numbers")
		}
		return percentileOf(numbers, fraction), true, nil
	case "GEOMEAN", "HARMEAN":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return nil, true, formulaError("#NUM!", name+" needs numbers")
		}
		if name == "GEOMEAN" {
			total := 0.0
			for _, number := range numbers {
				if number <= 0 {
					return nil, true, formulaError("#NUM!", "GEOMEAN needs positive numbers")
				}
				total += math.Log(number)
			}
			return math.Exp(total / float64(len(numbers))), true, nil
		}
		total := 0.0
		for _, number := range numbers {
			if number <= 0 {
				return nil, true, formulaError("#NUM!", "HARMEAN needs positive numbers")
			}
			total += 1 / number
		}
		return float64(len(numbers)) / total, true, nil
	}
	return nil, false, nil
}

// looksLikeFlag distinguishes RANK's optional order argument from another
// value in the population: only 0 and 1 can be the flag.
func looksLikeFlag(value any) bool {
	number, ok := toNumber(value)
	return ok && (number == 0 || number == 1)
}

// evaluatePairedStatistics covers the two-series functions, which need both
// arguments to line up value by value.
func evaluatePairedStatistics(name string, arguments []any) (any, bool, error) {
	switch name {
	case "CORREL", "PEARSON", "RSQ", "SLOPE", "INTERCEPT", "COVAR":
		if len(arguments) != 2 {
			return nil, true, argError(name)
		}
		// SLOPE and INTERCEPT take the dependent series first.
		firstIndex, secondIndex := 0, 1
		if name == "SLOPE" || name == "INTERCEPT" {
			firstIndex, secondIndex = 1, 0
		}
		x, y, err := alignedSeries(name, arguments[firstIndex], arguments[secondIndex])
		if err != nil {
			return nil, true, err
		}
		meanX, meanY := mean(x), mean(y)
		var covariance, varianceX, varianceY float64
		for index := range x {
			covariance += (x[index] - meanX) * (y[index] - meanY)
			varianceX += (x[index] - meanX) * (x[index] - meanX)
			varianceY += (y[index] - meanY) * (y[index] - meanY)
		}
		switch name {
		case "COVAR":
			return covariance / float64(len(x)), true, nil
		case "SLOPE":
			if varianceX == 0 {
				return nil, true, formulaError("#DIV/0!", "SLOPE needs varying x values")
			}
			return covariance / varianceX, true, nil
		case "INTERCEPT":
			if varianceX == 0 {
				return nil, true, formulaError("#DIV/0!", "INTERCEPT needs varying x values")
			}
			return meanY - covariance/varianceX*meanX, true, nil
		}
		if varianceX == 0 || varianceY == 0 {
			return nil, true, formulaError("#DIV/0!", name+" needs varying values")
		}
		correlation := covariance / math.Sqrt(varianceX*varianceY)
		if name == "RSQ" {
			return correlation * correlation, true, nil
		}
		return correlation, true, nil
	case "FORECAST", "TREND":
		if len(arguments) != 3 {
			return nil, true, argError(name)
		}
		target, ok := toNumber(scalarOrFirst(arguments[0]))
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number to forecast at")
		}
		y, x, err := alignedSeries(name, arguments[1], arguments[2])
		if err != nil {
			return nil, true, err
		}
		meanX, meanY := mean(x), mean(y)
		var covariance, varianceX float64
		for index := range x {
			covariance += (x[index] - meanX) * (y[index] - meanY)
			varianceX += (x[index] - meanX) * (x[index] - meanX)
		}
		if varianceX == 0 {
			return nil, true, formulaError("#DIV/0!", name+" needs varying x values")
		}
		slope := covariance / varianceX
		return meanY + slope*(target-meanX), true, nil
	}
	return nil, false, nil
}

func alignedSeries(name string, first, second any) ([]float64, []float64, error) {
	left, right := flatten(first), flatten(second)
	if len(left) != len(right) {
		return nil, nil, formulaError("#N/A", name+" needs two series of the same length")
	}
	x := make([]float64, 0, len(left))
	y := make([]float64, 0, len(left))
	for index := range left {
		leftNumber, leftOK := toNumber(left[index])
		rightNumber, rightOK := toNumber(right[index])
		if !leftOK || !rightOK || left[index] == nil || right[index] == nil {
			continue
		}
		x = append(x, leftNumber)
		y = append(y, rightNumber)
	}
	if len(x) < 2 {
		return nil, nil, formulaError("#DIV/0!", name+" needs at least two paired numbers")
	}
	return x, y, nil
}

func scalarOrFirst(value any) any {
	items := flatten(value)
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

func mean(numbers []float64) float64 {
	total := 0.0
	for _, number := range numbers {
		total += number
	}
	return total / float64(len(numbers))
}

func populationVariance(numbers []float64, sample bool) float64 {
	average := mean(numbers)
	total := 0.0
	for _, number := range numbers {
		total += (number - average) * (number - average)
	}
	divisor := float64(len(numbers))
	if sample {
		divisor--
	}
	if divisor <= 0 {
		return 0
	}
	return total / divisor
}

func sortedNumbers(numbers []float64) []float64 {
	sorted := append([]float64(nil), numbers...)
	sort.Float64s(sorted)
	return sorted
}

func percentileOf(sorted []float64, fraction float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := fraction * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	return sorted[lower] + (position-float64(lower))*(sorted[upper]-sorted[lower])
}
