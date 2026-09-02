package formula

import (
	"math"
	"math/rand/v2"
)

// randomFraction is the single source of randomness so tests can reason about
// the volatile functions in one place.
var randomFraction = rand.Float64

// evaluateMath covers the arithmetic functions Google Sheets offers beyond the
// handful kanpic started with.
func evaluateMath(name string, values []any) (any, bool, error) {
	switch name {
	case "CEILING", "FLOOR", "MROUND":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		factor := 1.0
		if len(values) == 2 && !omitted(values[1]) {
			if factor, ok = toNumber(values[1]); !ok {
				return nil, true, formulaError("#VALUE!", name+" requires a number")
			}
		}
		if factor == 0 {
			return float64(0), true, nil
		}
		// MROUND 는 부호가 어긋나면 답이 없다. CEILING·FLOOR 는 음수를
		// 양의 배수로 맞추는 것을 받아 준다 — CEILING(-4.5,2) 는 -4 다.
		// 양수를 음의 배수로 맞추는 것만 #NUM! 이다.
		if name == "MROUND" && number*factor < 0 {
			return nil, true, formulaError("#NUM!", name+" needs a factor with the same sign as the number")
		}
		if number > 0 && factor < 0 {
			return nil, true, formulaError("#NUM!", name+" needs a factor with the same sign as the number")
		}
		// ROUND 와 같은 십진 셈을 쓴다. 배수를 이진 실수로 나누면
		// CEILING(0.1+0.2, 0.1) 이 0.3 이 아니라 0.4 가 된다.
		//
		// 몫이 음수이면 0 에서 멀어지는 쪽과 위쪽이 서로 반대다. CEILING 은
		// 언제나 몫을 위로, FLOOR 는 언제나 아래로 보내야 한다.
		mode := roundHalfAway
		negativeQuotient := (number < 0) != (factor < 0)
		switch name {
		case "CEILING":
			mode = roundAwayFromZero
			if negativeQuotient {
				mode = roundTowardZero
			}
		case "FLOOR":
			mode = roundTowardZero
			if negativeQuotient {
				mode = roundAwayFromZero
			}
		}
		if result, ok := decimalMultiple(number, factor, mode); ok {
			return result, true, nil
		}
		switch name {
		case "CEILING":
			return math.Ceil(number/factor) * factor, true, nil
		case "FLOOR":
			return math.Floor(number/factor) * factor, true, nil
		}
		return math.Round(number/factor) * factor, true, nil
	case "TRUNC":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "TRUNC requires a number")
		}
		digits := 0.0
		if len(values) == 2 && !omitted(values[1]) {
			digits, _ = toNumber(values[1])
		}
		// ROUNDDOWN 과 같은 십진 셈을 쓴다. 이진 실수로 자리를 밀면
		// 0.29*100 이 28.999999999999996 이 되어 TRUNC(0.29,2) 가 0.28 을,
		// TRUNC(1.001,3) 이 1 을 냈다 — 이미 그 자리에 딱 맞는 수인데도
		// 마지막 자리가 깎였다.
		return decimalRound(number, decimalPlaces(digits), roundTowardZero), true, nil
	case "SIGN", "EXP", "LN", "LOG10", "SIN", "COS", "TAN", "ASIN", "ACOS", "ATAN", "SINH", "COSH", "TANH", "RADIANS", "DEGREES", "FACT", "EVEN", "ODD":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		return singleArgumentMath(name, number)
	case "LOG":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok || number <= 0 {
			return nil, true, formulaError("#NUM!", "LOG requires a positive number")
		}
		base := 10.0
		if len(values) == 2 && !omitted(values[1]) {
			if base, ok = toNumber(values[1]); !ok || base <= 0 || base == 1 {
				return nil, true, formulaError("#NUM!", "LOG base must be positive and not 1")
			}
		}
		return math.Log(number) / math.Log(base), true, nil
	case "ATAN2":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		x, xOK := toNumber(values[0])
		y, yOK := toNumber(values[1])
		if !xOK || !yOK {
			return nil, true, formulaError("#VALUE!", "ATAN2 requires numbers")
		}
		return math.Atan2(y, x), true, nil
	case "PI":
		if len(values) != 0 {
			return nil, true, argError(name)
		}
		return math.Pi, true, nil
	case "QUOTIENT":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		dividend, ok1 := toNumber(values[0])
		divisor, ok2 := toNumber(values[1])
		if !ok1 || !ok2 {
			return nil, true, formulaError("#VALUE!", "QUOTIENT requires numbers")
		}
		if divisor == 0 {
			return nil, true, formulaError("#DIV/0!", "division by zero")
		}
		return math.Trunc(dividend / divisor), true, nil
	case "GCD", "LCM":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return nil, true, argError(name)
		}
		result := math.Abs(math.Trunc(numbers[0]))
		for _, number := range numbers[1:] {
			current := math.Abs(math.Trunc(number))
			if name == "GCD" {
				result = greatestCommonDivisor(result, current)
				continue
			}
			divisor := greatestCommonDivisor(result, current)
			if divisor == 0 {
				result = 0
				continue
			}
			result = result / divisor * current
		}
		return result, true, nil
	case "SUMSQ":
		total := 0.0
		for _, number := range numericValues(values) {
			total += number * number
		}
		return total, true, nil
	case "COMBIN", "PERMUT":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		total, ok1 := toNumber(values[0])
		chosen, ok2 := toNumber(values[1])
		if !ok1 || !ok2 || total < 0 || chosen < 0 || chosen > total {
			return nil, true, formulaError("#NUM!", name+" needs 0 <= k <= n")
		}
		result := 1.0
		for index := 0.0; index < chosen; index++ {
			result *= total - index
			if name == "COMBIN" {
				result /= index + 1
			}
		}
		return math.Round(result), true, nil
	case "RAND":
		if len(values) != 0 {
			return nil, true, argError(name)
		}
		return randomFraction(), true, nil
	case "RANDBETWEEN":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		low, ok1 := toNumber(values[0])
		high, ok2 := toNumber(values[1])
		if !ok1 || !ok2 || high < low {
			return nil, true, formulaError("#NUM!", "RANDBETWEEN needs a low and a high bound")
		}
		low, high = math.Ceil(low), math.Floor(high)
		return low + math.Floor(randomFraction()*(high-low+1)), true, nil
	}
	return nil, false, nil
}

func singleArgumentMath(name string, number float64) (any, bool, error) {
	switch name {
	case "SIGN":
		if number == 0 {
			return float64(0), true, nil
		}
		return sign(number), true, nil
	case "EXP":
		return math.Exp(number), true, nil
	case "LN":
		if number <= 0 {
			return nil, true, formulaError("#NUM!", "LN requires a positive number")
		}
		return math.Log(number), true, nil
	case "LOG10":
		if number <= 0 {
			return nil, true, formulaError("#NUM!", "LOG10 requires a positive number")
		}
		return math.Log10(number), true, nil
	case "SIN":
		return math.Sin(number), true, nil
	case "COS":
		return math.Cos(number), true, nil
	case "TAN":
		return math.Tan(number), true, nil
	case "ASIN", "ACOS":
		if number < -1 || number > 1 {
			return nil, true, formulaError("#NUM!", name+" takes a number between -1 and 1")
		}
		if name == "ASIN" {
			return math.Asin(number), true, nil
		}
		return math.Acos(number), true, nil
	case "ATAN":
		return math.Atan(number), true, nil
	case "SINH":
		return math.Sinh(number), true, nil
	case "COSH":
		return math.Cosh(number), true, nil
	case "TANH":
		return math.Tanh(number), true, nil
	case "RADIANS":
		return number * math.Pi / 180, true, nil
	case "DEGREES":
		return number * 180 / math.Pi, true, nil
	case "FACT":
		if number < 0 || number > 170 {
			return nil, true, formulaError("#NUM!", "FACT takes 0 to 170")
		}
		result := 1.0
		for index := 2.0; index <= math.Trunc(number); index++ {
			result *= index
		}
		return result, true, nil
	case "EVEN", "ODD":
		away := math.Ceil(math.Abs(number))
		if name == "EVEN" {
			if math.Mod(away, 2) != 0 {
				away++
			}
		} else if math.Mod(away, 2) == 0 {
			away++
		}
		if number < 0 {
			return -away, true, nil
		}
		return away, true, nil
	}
	return nil, false, nil
}

func greatestCommonDivisor(left, right float64) float64 {
	for right != 0 {
		left, right = right, math.Mod(left, right)
	}
	return left
}
