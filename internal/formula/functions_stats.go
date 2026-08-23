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
		if len(arguments) == 3 && !omitted(arguments[2]) {
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
		// 이름 끝에 A 가 붙은 것은 숫자가 아닌 값도 0 으로 세는 쪽이다.
		// STDEV 는 "x" 를 건너뛰고 STDEVA 는 0 으로 센다 — 엑셀과 시트가
		// 그렇게 나눈다. AVERAGEA 는 이미 그렇게 세고 있었다.
		numbers := numericValues(values)
		switch name {
		case "STDEVA", "STDEVPA", "VARA", "VARPA":
			numbers = numbersCountingTextAsZero(values)
		}
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
	case "TREND":
		return evaluateTrend(arguments)
	case "FORECAST":
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

// numbersCountingTextAsZero 는 AVERAGEA, STDEVA, VARA 처럼 이름 끝에 A 가
// 붙은 함수가 값을 세는 방식이다. 빈 칸은 건너뛰고, 숫자로 읽히지 않는 값은
// 0 으로 센다. 참은 1, 거짓은 0 으로 읽히므로 따로 다루지 않는다.
func numbersCountingTextAsZero(values []any) []float64 {
	numbers := make([]float64, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		number, ok := toNumber(value)
		if !ok {
			number = 0
		}
		numbers = append(numbers, number)
	}
	return numbers
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

// evaluateTrend 은 최소제곱 직선 위의 값을 돌려준다. FORECAST 와 셈은 같지만
// **인수 차례가 다르다**. 엑셀과 시트가 이렇게 나눠 놓았다.
//
//	FORECAST(구할 x, 알려진 y, 알려진 x)   — 구할 값이 맨 앞
//	TREND(알려진 y, [알려진 x], [구할 x], [b]) — 구할 값이 뒤
//
// 예전에는 둘을 한 갈래로 묶어 두어, TREND 를 문서대로 쓰면 인수가 조용히
// 뒤바뀌어 엉뚱한 수가 나왔다. 오류도 없이 그럴듯한 수가 나오는 쪽이 더
// 나쁘다.
//
// 알려진 x 를 적지 않으면 1, 2, 3… 을 쓴다. 구할 x 를 적지 않으면 알려진 x
// 를 그대로 쓴다. b 를 거짓으로 두면 직선이 원점을 지나게 한다.
func evaluateTrend(arguments []any) (any, bool, error) {
	if len(arguments) < 1 || len(arguments) > 4 {
		return nil, true, argError("TREND")
	}
	knownY := numericSeries(arguments[0])
	if len(knownY) == 0 {
		return nil, true, formulaError("#VALUE!", "TREND needs known y values")
	}
	knownX := make([]float64, len(knownY))
	for index := range knownX {
		knownX[index] = float64(index + 1)
	}
	if len(arguments) >= 2 && !omitted(arguments[1]) {
		knownX = numericSeries(arguments[1])
		if len(knownX) != len(knownY) {
			return nil, true, formulaError("#REF!", "TREND needs known x and y series of the same length")
		}
	}
	if len(knownY) < 2 {
		return nil, true, formulaError("#DIV/0!", "TREND needs at least two known points")
	}
	intercept := true
	if len(arguments) == 4 && !omitted(arguments[3]) {
		truth, err := booleanValue(scalarOrFirst(arguments[3]), "TREND")
		if err != nil {
			return nil, true, err
		}
		intercept = truth
	}
	var slope, offset float64
	if intercept {
		meanX, meanY := mean(knownX), mean(knownY)
		var covariance, varianceX float64
		for index := range knownX {
			covariance += (knownX[index] - meanX) * (knownY[index] - meanY)
			varianceX += (knownX[index] - meanX) * (knownX[index] - meanX)
		}
		if varianceX == 0 {
			return nil, true, formulaError("#DIV/0!", "TREND needs varying x values")
		}
		slope = covariance / varianceX
		offset = meanY - slope*meanX
	} else {
		var sumXY, sumXX float64
		for index := range knownX {
			sumXY += knownX[index] * knownY[index]
			sumXX += knownX[index] * knownX[index]
		}
		if sumXX == 0 {
			return nil, true, formulaError("#DIV/0!", "TREND needs varying x values")
		}
		slope = sumXY / sumXX
	}
	// 구할 x 를 적지 않으면 알려진 x 자리를 그대로 되짚는다.
	if len(arguments) < 3 || omitted(arguments[2]) {
		results := make([]any, len(knownX))
		for index, x := range knownX {
			results[index] = offset + slope*x
		}
		return trendShape(arrayValue{rows: len(results), columns: 1, values: results}), true, nil
	}
	// 결과는 구할 x 와 같은 모양으로 돌려준다. 한 칸이면 한 칸이다.
	target, err := toArray(arguments[2])
	if err != nil {
		return nil, true, err
	}
	results := make([]any, len(target.values))
	for index, value := range target.values {
		number, ok := toNumber(value)
		if !ok {
			return nil, true, formulaError("#VALUE!", "TREND needs numbers to forecast at")
		}
		results[index] = offset + slope*number
	}
	return trendShape(arrayValue{rows: target.rows, columns: target.columns, values: results}), true, nil
}

// 한 칸짜리 답은 배열이 아니라 값으로 돌려준다. 그래야 =TREND(…)*2 처럼
// 이어 쓸 때 자연스럽다.
func trendShape(result arrayValue) any {
	if len(result.values) == 1 {
		return result.values[0]
	}
	return result
}

// numericSeries 는 인수를 펴서 숫자만 모은다. 빈 칸과 글자는 건너뛴다.
func numericSeries(value any) []float64 {
	return numericValues(flatten(value))
}
