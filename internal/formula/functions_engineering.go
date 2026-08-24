package formula

import (
	"math"
	"strconv"
	"strings"
)

// 공학 함수는 진법 바꾸기, 비트 셈, 그리고 두 값을 견주는 몇 가지다. 하나도
// 없어서 가져온 파일에 들어 있으면 그대로 #NAME? 이었다.
//
// 엑셀은 진법 함수의 값을 **10자리 2의 보수** 로 다룬다. 그래서 BIN2DEC 에
// 1111111111 을 넣으면 1023 이 아니라 -1 이다. 16진법은 10자리, 8진법도
// 10자리다. 자리 수가 다르면 부호를 잘못 읽는다.
func evaluateEngineering(name string, values []any) (any, bool, error) {
	switch name {
	case "BIN2DEC", "BIN2HEX", "BIN2OCT", "OCT2DEC", "OCT2BIN", "OCT2HEX", "HEX2DEC", "HEX2BIN", "HEX2OCT",
		"DEC2BIN", "DEC2HEX", "DEC2OCT":
		return evaluateBaseConversion(name, values)
	case "BITAND", "BITOR", "BITXOR", "BITLSHIFT", "BITRSHIFT":
		return evaluateBitwise(name, values)
	case "DELTA":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		first, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "DELTA requires numbers")
		}
		second := 0.0
		if len(values) == 2 && !omitted(values[1]) {
			if second, ok = toNumber(values[1]); !ok {
				return nil, true, formulaError("#VALUE!", "DELTA requires numbers")
			}
		}
		if first == second {
			return float64(1), true, nil
		}
		return float64(0), true, nil
	case "GESTEP":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "GESTEP requires numbers")
		}
		step := 0.0
		if len(values) == 2 && !omitted(values[1]) {
			if step, ok = toNumber(values[1]); !ok {
				return nil, true, formulaError("#VALUE!", "GESTEP requires numbers")
			}
		}
		if number >= step {
			return float64(1), true, nil
		}
		return float64(0), true, nil
	case "ERF", "ERFC":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		lower, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires numbers")
		}
		if name == "ERFC" {
			if len(values) != 1 {
				return nil, true, argError(name)
			}
			return math.Erfc(lower), true, nil
		}
		if len(values) == 2 && !omitted(values[1]) {
			upper, upperOK := toNumber(values[1])
			if !upperOK {
				return nil, true, formulaError("#VALUE!", "ERF requires numbers")
			}
			return math.Erf(upper) - math.Erf(lower), true, nil
		}
		return math.Erf(lower), true, nil
	}
	return nil, false, nil
}

// baseDigits 는 각 진법의 글자 수 한도다. 엑셀은 이만큼만 받고, 그 안에서
// 맨 앞자리가 1 이면 음수로 읽는다.
var baseDigits = map[int]int{2: 10, 8: 10, 16: 10}

func evaluateBaseConversion(name string, values []any) (any, bool, error) {
	from, to := conversionBases(name)
	if len(values) < 1 || len(values) > 2 {
		return nil, true, argError(name)
	}
	number, err := decodeBase(name, values[0], from)
	if err != nil {
		return nil, true, err
	}
	if to == 10 {
		return float64(number), true, nil
	}
	text, err := encodeBase(name, number, to)
	if err != nil {
		return nil, true, err
	}
	if len(values) == 2 && !omitted(values[1]) {
		places, ok := toNumber(values[1])
		if !ok || places < 0 || places != math.Trunc(places) {
			return nil, true, formulaError("#VALUE!", name+" places must be a whole number")
		}
		if number < 0 {
			// 음수는 이미 자리를 다 쓰므로 자리 수를 지정할 수 없다.
			return text, true, nil
		}
		if len(text) > int(places) {
			return nil, true, formulaError("#NUM!", name+" needs more places than requested")
		}
		text = strings.Repeat("0", int(places)-len(text)) + text
	}
	return text, true, nil
}

func conversionBases(name string) (int, int) {
	bases := map[string]int{"BIN": 2, "OCT": 8, "DEC": 10, "HEX": 16}
	return bases[name[:3]], bases[name[4:]]
}

func decodeBase(name string, value any, base int) (int64, error) {
	scalar, err := scalarValue(value)
	if err != nil {
		return 0, err
	}
	if base == 10 {
		number, ok := toNumber(scalar)
		if !ok {
			return 0, formulaError("#VALUE!", name+" requires a number")
		}
		if number != math.Trunc(number) {
			number = math.Trunc(number)
		}
		if number < -549755813888 || number > 549755813887 {
			return 0, formulaError("#NUM!", name+" is limited to 40 bits")
		}
		return int64(number), nil
	}
	text := strings.ToUpper(strings.TrimSpace(display(scalar)))
	if text == "" {
		return 0, nil
	}
	digits := baseDigits[base]
	if len(text) > digits {
		return 0, formulaError("#NUM!", name+" takes at most "+strconv.Itoa(digits)+" digits")
	}
	parsed, parseErr := strconv.ParseInt(text, base, 64)
	if parseErr != nil {
		return 0, formulaError("#NUM!", name+" cannot read "+text+" in base "+strconv.Itoa(base))
	}
	// 맨 앞자리가 켜져 있으면 2의 보수로 읽는다. 이것이 엑셀의 규칙이다.
	if len(text) == digits {
		bits := uint(digits) * uint(math.Log2(float64(base)))
		if parsed >= int64(1)<<(bits-1) {
			parsed -= int64(1) << bits
		}
	}
	return parsed, nil
}

func encodeBase(name string, number int64, base int) (string, error) {
	digits := baseDigits[base]
	bits := uint(digits) * uint(math.Log2(float64(base)))
	limit := int64(1) << (bits - 1)
	if number >= limit || number < -limit {
		return "", formulaError("#NUM!", name+" result does not fit in "+strconv.Itoa(digits)+" digits")
	}
	if number < 0 {
		number += int64(1) << bits
	}
	return strings.ToUpper(strconv.FormatInt(number, base)), nil
}

func evaluateBitwise(name string, values []any) (any, bool, error) {
	if len(values) != 2 {
		return nil, true, argError(name)
	}
	first, firstOK := toNumber(values[0])
	second, secondOK := toNumber(values[1])
	if !firstOK || !secondOK {
		return nil, true, formulaError("#VALUE!", name+" requires numbers")
	}
	// 비트 함수는 음수와 소수를 받지 않는다. 48비트까지가 한도다.
	const maxBitValue = 281474976710655.0
	if first < 0 || second < 0 || first != math.Trunc(first) || second != math.Trunc(second) {
		return nil, true, formulaError("#NUM!", name+" needs whole numbers that are not negative")
	}
	if first > maxBitValue {
		return nil, true, formulaError("#NUM!", name+" is limited to 48 bits")
	}
	left, right := int64(first), int64(second)
	switch name {
	case "BITAND":
		if right > int64(maxBitValue) {
			return nil, true, formulaError("#NUM!", name+" is limited to 48 bits")
		}
		return float64(left & right), true, nil
	case "BITOR":
		if right > int64(maxBitValue) {
			return nil, true, formulaError("#NUM!", name+" is limited to 48 bits")
		}
		return float64(left | right), true, nil
	case "BITXOR":
		if right > int64(maxBitValue) {
			return nil, true, formulaError("#NUM!", name+" is limited to 48 bits")
		}
		return float64(left ^ right), true, nil
	case "BITLSHIFT", "BITRSHIFT":
		if right > 53 {
			return nil, true, formulaError("#NUM!", name+" cannot shift more than 53 places")
		}
		shifted := left << uint(right)
		if name == "BITRSHIFT" {
			shifted = left >> uint(right)
		}
		if shifted > int64(maxBitValue) {
			return nil, true, formulaError("#NUM!", name+" result is larger than 48 bits")
		}
		return float64(shifted), true, nil
	}
	return nil, false, nil
}
