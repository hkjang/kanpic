package formula

import (
	"math"
	"strconv"
	"strings"
)

// 여기 모인 것은 구글 시트가 아는데 우리가 몰랐던 수학 함수들이다. 삼각의
// 역수와 쌍곡선, 자리 올림의 정밀한 갈래, 조합과 몇 가지 제곱합이다.
func evaluateMathExtra(name string, values []any) (any, bool, error) {
	switch name {
	case "SEC", "CSC", "COT", "SECH", "CSCH", "COTH", "ACOT", "ACOTH", "ACOSH", "ASINH", "ATANH", "GAMMALN", "SQRTPI":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		return extraSingleArgumentMath(name, number)
	case "CEILING.MATH", "FLOOR.MATH", "CEILING.PRECISE", "FLOOR.PRECISE", "ISO.CEILING":
		return evaluatePreciseRounding(name, values)
	case "COMBINA", "SUMX2MY2", "SUMX2PY2", "SUMXMY2", "FACTDOUBLE", "MULTINOMIAL", "BASE", "DECIMAL", "SERIESSUM", "MUNIT", "RANDARRAY":
		return evaluateMathExtraMulti(name, values)
	}
	return nil, false, nil
}

func extraSingleArgumentMath(name string, number float64) (any, bool, error) {
	// 나누는 쪽이 0 이면 답이 없다. 무한대를 내면 뒤의 셈이 조용히 망가진다.
	divide := func(divisor float64) (any, bool, error) {
		if divisor == 0 {
			return nil, true, formulaError("#DIV/0!", name+" is not defined here")
		}
		return 1 / divisor, true, nil
	}
	switch name {
	case "SEC":
		return divide(math.Cos(number))
	case "CSC":
		return divide(math.Sin(number))
	case "COT":
		return divide(math.Tan(number))
	case "SECH":
		return divide(math.Cosh(number))
	case "CSCH":
		return divide(math.Sinh(number))
	case "COTH":
		return divide(math.Tanh(number))
	case "ACOT":
		// 엑셀의 ACOT 는 0 과 파이 사이의 값을 낸다. atan(1/x) 를 쓰면
		// 음수에서 부호가 뒤집힌다.
		return math.Pi/2 - math.Atan(number), true, nil
	case "ACOTH":
		if math.Abs(number) <= 1 {
			return nil, true, formulaError("#NUM!", "ACOTH needs a number outside -1 and 1")
		}
		return math.Atanh(1 / number), true, nil
	case "ACOSH":
		if number < 1 {
			return nil, true, formulaError("#NUM!", "ACOSH needs a number of 1 or more")
		}
		return math.Acosh(number), true, nil
	case "ASINH":
		return math.Asinh(number), true, nil
	case "ATANH":
		if math.Abs(number) >= 1 {
			return nil, true, formulaError("#NUM!", "ATANH needs a number between -1 and 1")
		}
		return math.Atanh(number), true, nil
	case "GAMMALN":
		if number <= 0 {
			return nil, true, formulaError("#NUM!", "GAMMALN needs a positive number")
		}
		result, _ := math.Lgamma(number)
		return result, true, nil
	case "SQRTPI":
		if number < 0 {
			return nil, true, formulaError("#NUM!", "SQRTPI needs a number of 0 or more")
		}
		return math.Sqrt(number * math.Pi), true, nil
	}
	return nil, false, nil
}

func evaluateMathExtraMulti(name string, values []any) (any, bool, error) {
	switch name {
	case "COMBINA":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		total, chosen, err := twoWholeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if total < 0 || chosen < 0 || (total == 0 && chosen > 0) {
			return nil, true, formulaError("#NUM!", "COMBINA needs numbers that are not negative")
		}
		// 되풀이를 허용한 조합은 C(n+k-1, k) 다.
		return math.Round(binomial(total+chosen-1, chosen)), true, nil
	case "FACTDOUBLE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "FACTDOUBLE requires a number")
		}
		whole := math.Trunc(number)
		if whole < -1 {
			return nil, true, formulaError("#NUM!", "FACTDOUBLE needs a number of -1 or more")
		}
		result := 1.0
		for step := whole; step > 1; step -= 2 {
			result *= step
		}
		return result, true, nil
	case "MULTINOMIAL":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return nil, true, formulaError("#VALUE!", "MULTINOMIAL requires numbers")
		}
		total, result := 0.0, 1.0
		for _, number := range numbers {
			whole := math.Trunc(number)
			if whole < 0 {
				return nil, true, formulaError("#NUM!", "MULTINOMIAL needs numbers that are not negative")
			}
			total += whole
			result *= factorialOf(whole)
		}
		return math.Round(factorialOf(total) / result), true, nil
	case "SUMX2MY2", "SUMX2PY2", "SUMXMY2":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		first, err := toArray(values[0])
		if err != nil {
			return nil, true, err
		}
		second, err := toArray(values[1])
		if err != nil {
			return nil, true, err
		}
		left, right := numericValues(first.values), numericValues(second.values)
		if len(left) != len(right) {
			return nil, true, formulaError("#N/A", name+" needs two ranges of the same size")
		}
		total := 0.0
		for index := range left {
			switch name {
			case "SUMX2MY2":
				total += left[index]*left[index] - right[index]*right[index]
			case "SUMX2PY2":
				total += left[index]*left[index] + right[index]*right[index]
			default:
				difference := left[index] - right[index]
				total += difference * difference
			}
		}
		return total, true, nil
	case "BASE":
		if len(values) < 2 || len(values) > 3 {
			return nil, true, argError(name)
		}
		number, radix, err := twoWholeNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		if number < 0 {
			return nil, true, formulaError("#NUM!", "BASE needs a number that is not negative")
		}
		if radix < 2 || radix > 36 {
			return nil, true, formulaError("#NUM!", "BASE radix must be between 2 and 36")
		}
		text := strings.ToUpper(strconv.FormatInt(int64(number), int(radix)))
		if len(values) == 3 && !omitted(values[2]) {
			length, ok := toNumber(values[2])
			if !ok || length < 0 {
				return nil, true, formulaError("#NUM!", "BASE length must not be negative")
			}
			if int(length) > len(text) {
				text = strings.Repeat("0", int(length)-len(text)) + text
			}
		}
		return text, true, nil
	case "DECIMAL":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		radix, ok := toNumber(values[1])
		if !ok || radix < 2 || radix > 36 || radix != math.Trunc(radix) {
			return nil, true, formulaError("#NUM!", "DECIMAL radix must be a whole number between 2 and 36")
		}
		text := strings.ToUpper(strings.TrimSpace(display(values[0])))
		if text == "" {
			return float64(0), true, nil
		}
		parsed, parseErr := strconv.ParseInt(text, int(radix), 64)
		if parseErr != nil {
			return nil, true, formulaError("#NUM!", "DECIMAL cannot read "+text+" in base "+strconv.Itoa(int(radix)))
		}
		return float64(parsed), true, nil
	case "SERIESSUM":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		x, xOK := toNumber(values[0])
		start, startOK := toNumber(values[1])
		step, stepOK := toNumber(values[2])
		if !xOK || !startOK || !stepOK {
			return nil, true, formulaError("#VALUE!", "SERIESSUM requires numbers")
		}
		coefficients, err := toArray(values[3])
		if err != nil {
			return nil, true, err
		}
		numbers := numericValues(coefficients.values)
		total := 0.0
		for index, coefficient := range numbers {
			total += coefficient * math.Pow(x, start+float64(index)*step)
		}
		return total, true, nil
	case "RANDARRAY":
		// 인수는 모두 생략할 수 있다. 아무것도 주지 않으면 0 과 1 사이의
		// 값 하나다.
		if len(values) > 5 {
			return nil, true, argError(name)
		}
		argument := func(index int, fallback float64) (float64, bool) {
			if index >= len(values) || omitted(values[index]) {
				return fallback, true
			}
			number, ok := toNumber(values[index])
			return number, ok
		}
		rows, rowsOK := argument(0, 1)
		columns, columnsOK := argument(1, 1)
		low, lowOK := argument(2, 0)
		high, highOK := argument(3, 1)
		if !rowsOK || !columnsOK || !lowOK || !highOK {
			return nil, true, formulaError("#VALUE!", "RANDARRAY requires numbers")
		}
		whole := false
		if len(values) >= 5 && !omitted(values[4]) {
			whole = truthy(values[4])
		}
		if rows < 1 || columns < 1 || rows != math.Trunc(rows) || columns != math.Trunc(columns) {
			return nil, true, formulaError("#VALUE!", "RANDARRAY needs whole row and column counts of 1 or more")
		}
		if rows*columns > 100000 {
			return nil, true, formulaError("#NUM!", "RANDARRAY is limited to 100,000 cells")
		}
		if high < low {
			return nil, true, formulaError("#VALUE!", "RANDARRAY needs a maximum that is not below the minimum")
		}
		cells := make([]any, int(rows*columns))
		for index := range cells {
			value := low + randomFraction()*(high-low)
			if whole {
				// 정수를 달라고 하면 양 끝을 모두 포함해야 한다.
				value = math.Floor(low) + math.Floor(randomFraction()*(math.Floor(high)-math.Floor(low)+1))
			}
			cells[index] = value
		}
		return arrayValue{rows: int(rows), columns: int(columns), values: cells}, true, nil
	case "MUNIT":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		size, ok := toNumber(values[0])
		if !ok || size < 1 || size != math.Trunc(size) {
			return nil, true, formulaError("#VALUE!", "MUNIT needs a whole number of 1 or more")
		}
		side := int(size)
		if side > 1000 {
			return nil, true, formulaError("#NUM!", "MUNIT is limited to 1000 rows")
		}
		cells := make([]any, side*side)
		for row := 0; row < side; row++ {
			for column := 0; column < side; column++ {
				value := 0.0
				if row == column {
					value = 1
				}
				cells[row*side+column] = value
			}
		}
		return arrayValue{rows: side, columns: side, values: cells}, true, nil
	}
	return nil, false, nil
}

// 자리 올림에는 갈래가 여럿이고, 갈리는 곳은 **음수** 다.
//
//	CEILING.MATH(-5.5)      -> -5   0 쪽으로 올린다(기본)
//	CEILING.MATH(-5.5,1,1)  -> -6   0 에서 멀어지는 쪽으로
//	FLOOR.MATH(-5.5)        -> -6   아래로 내린다(기본)
//	FLOOR.MATH(-5.5,1,1)    -> -5   0 쪽으로
//	CEILING.PRECISE(-4.3)   -> -4   언제나 위로, 기준의 부호는 무시
//	FLOOR.PRECISE(-3.2)     -> -4   언제나 아래로
//
// 하나로 뭉뚱그리면 음수에서 조용히 다른 값을 낸다.
func evaluatePreciseRounding(name string, values []any) (any, bool, error) {
	if len(values) < 1 || len(values) > 3 {
		return nil, true, argError(name)
	}
	if len(values) == 3 && name != "CEILING.MATH" && name != "FLOOR.MATH" {
		return nil, true, argError(name)
	}
	number, ok := toNumber(values[0])
	if !ok {
		return nil, true, formulaError("#VALUE!", name+" requires a number")
	}
	significance := 1.0
	if len(values) >= 2 && !omitted(values[1]) {
		if significance, ok = toNumber(values[1]); !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
	}
	if significance == 0 || number == 0 {
		return float64(0), true, nil
	}
	// 이 갈래들은 모두 기준의 부호를 무시한다. CEILING·FLOOR 와 다른 점이다.
	significance = math.Abs(significance)
	awayFromZero := false
	if len(values) == 3 && !omitted(values[2]) {
		mode, modeOK := toNumber(values[2])
		if !modeOK {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		awayFromZero = mode != 0
	}
	up := name == "CEILING.MATH" || name == "CEILING.PRECISE" || name == "ISO.CEILING"
	if number < 0 && awayFromZero {
		// mode 를 준 음수는 0 에서 멀어지는 쪽으로 간다. 올림과 내림이
		// 서로 뒤바뀐다.
		up = !up
	}
	quotient := number / significance
	if up {
		return math.Ceil(quotient) * significance, true, nil
	}
	return math.Floor(quotient) * significance, true, nil
}

func twoWholeNumbers(name string, values []any) (float64, float64, error) {
	first, firstOK := toNumber(values[0])
	second, secondOK := toNumber(values[1])
	if !firstOK || !secondOK {
		return 0, 0, formulaError("#VALUE!", name+" requires numbers")
	}
	return math.Trunc(first), math.Trunc(second), nil
}

func factorialOf(number float64) float64 {
	result := 1.0
	for step := 2.0; step <= number; step++ {
		result *= step
	}
	return result
}

func binomial(total, chosen float64) float64 {
	if chosen < 0 || chosen > total {
		return 0
	}
	result := 1.0
	for step := 0.0; step < chosen; step++ {
		result = result * (total - step) / (step + 1)
	}
	return result
}
