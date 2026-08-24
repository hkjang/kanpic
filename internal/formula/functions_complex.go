package formula

import (
	"math"
	"math/cmplx"
	"strconv"
	"strings"
)

// 엑셀은 복소수를 "3+4i" 같은 **글자** 로 다룬다. 셈할 때마다 읽고 다시
// 적으므로, 읽는 쪽과 적는 쪽이 서로를 되돌려야 한다.
//
// 허수 단위는 i 와 j 둘 다 쓴다. 한 식 안에서 둘을 섞으면 엑셀은 거부한다 —
// 어느 쪽으로 적을지 정할 수 없기 때문이다.

// parseComplex 는 글자를 복소수로 읽고 쓰인 허수 단위를 함께 돌려준다.
func parseComplex(value any) (complex128, string, bool) {
	if number, ok := value.(float64); ok {
		return complex(number, 0), "", true
	}
	text := strings.TrimSpace(display(value))
	if text == "" {
		return 0, "", true
	}
	suffix := ""
	switch {
	case strings.HasSuffix(text, "i"):
		suffix = "i"
	case strings.HasSuffix(text, "j"):
		suffix = "j"
	}
	if suffix == "" {
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, "", false
		}
		return complex(number, 0), "", true
	}
	body := text[:len(text)-1]
	// 실수부와 허수부가 갈리는 자리를 찾는다. 지수 표기의 부호는 자리가
	// 아니므로 앞 글자가 e 나 E 면 지나간다.
	split := -1
	for index := 1; index < len(body); index++ {
		if body[index] != '+' && body[index] != '-' {
			continue
		}
		if previous := body[index-1]; previous == 'e' || previous == 'E' {
			continue
		}
		split = index
	}
	realPart, imaginaryText := 0.0, body
	if split > 0 {
		parsed, err := strconv.ParseFloat(body[:split], 64)
		if err != nil {
			return 0, "", false
		}
		realPart = parsed
		imaginaryText = body[split:]
	}
	switch imaginaryText {
	case "", "+":
		return complex(realPart, 1), suffix, true
	case "-":
		return complex(realPart, -1), suffix, true
	}
	imaginary, err := strconv.ParseFloat(imaginaryText, 64)
	if err != nil {
		return 0, "", false
	}
	return complex(realPart, imaginary), suffix, true
}

// formatComplex 는 복소수를 엑셀이 적는 꼴로 되돌린다. 허수부가 0 이면
// 실수만, 실수부가 0 이면 허수만 적는다. 1 과 -1 은 숫자를 적지 않는다.
func formatComplex(value complex128, suffix string) string {
	if suffix == "" {
		suffix = "i"
	}
	realPart, imaginary := real(value), imag(value)
	if imaginary == 0 {
		return trimFloat(realPart)
	}
	unit := trimFloat(imaginary)
	switch imaginary {
	case 1:
		unit = ""
	case -1:
		unit = "-"
	}
	if realPart == 0 {
		return unit + suffix
	}
	sign := "+"
	if imaginary < 0 {
		sign = ""
	}
	return trimFloat(realPart) + sign + unit + suffix
}

func trimFloat(number float64) string {
	return strconv.FormatFloat(number, 'g', 15, 64)
}

// complexArguments 는 인수를 모두 읽고 허수 단위가 섞이지 않았는지 본다.
func complexArguments(name string, values []any) ([]complex128, string, error) {
	numbers := make([]complex128, 0, len(values))
	suffix := ""
	for _, value := range values {
		if value == nil {
			continue
		}
		parsed, used, ok := parseComplex(value)
		if !ok {
			return nil, "", formulaError("#NUM!", name+" needs numbers written like 3+4i")
		}
		if used != "" {
			if suffix != "" && used != suffix {
				return nil, "", formulaError("#VALUE!", name+" cannot mix i and j in one calculation")
			}
			suffix = used
		}
		numbers = append(numbers, parsed)
	}
	return numbers, suffix, nil
}

func evaluateComplex(name string, values []any) (any, bool, error) {
	switch name {
	case "COMPLEX":
		if len(values) < 2 || len(values) > 3 {
			return nil, true, argError(name)
		}
		realPart, imaginary, err := twoNumbers(name, values)
		if err != nil {
			return nil, true, err
		}
		suffix := "i"
		if len(values) == 3 && !omitted(values[2]) {
			suffix = strings.TrimSpace(display(values[2]))
			if suffix != "i" && suffix != "j" {
				return nil, true, formulaError("#VALUE!", "COMPLEX suffix must be i or j")
			}
		}
		return formatComplex(complex(realPart, imaginary), suffix), true, nil
	case "IMREAL", "IMAGINARY", "IMABS", "IMARGUMENT", "IMCONJUGATE",
		"IMSQRT", "IMEXP", "IMLN", "IMLOG10", "IMLOG2":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		numbers, suffix, err := complexArguments(name, values)
		if err != nil {
			return nil, true, err
		}
		if len(numbers) != 1 {
			return nil, true, argError(name)
		}
		number := numbers[0]
		switch name {
		case "IMREAL":
			return real(number), true, nil
		case "IMAGINARY":
			return imag(number), true, nil
		case "IMABS":
			return cmplx.Abs(number), true, nil
		case "IMARGUMENT":
			if number == 0 {
				return nil, true, formulaError("#DIV/0!", "IMARGUMENT has no angle for zero")
			}
			return cmplx.Phase(number), true, nil
		case "IMCONJUGATE":
			return formatComplex(cmplx.Conj(number), suffix), true, nil
		case "IMSQRT":
			return formatComplex(cmplx.Sqrt(number), suffix), true, nil
		case "IMEXP":
			return formatComplex(cmplx.Exp(number), suffix), true, nil
		case "IMLN", "IMLOG10", "IMLOG2":
			if number == 0 {
				return nil, true, formulaError("#NUM!", name+" has no answer for zero")
			}
			result := cmplx.Log(number)
			switch name {
			case "IMLOG10":
				result /= complex(math.Log(10), 0)
			case "IMLOG2":
				result /= complex(math.Ln2, 0)
			}
			return formatComplex(result, suffix), true, nil
		}
	case "IMSUM", "IMPRODUCT":
		numbers, suffix, err := complexArguments(name, values)
		if err != nil {
			return nil, true, err
		}
		if len(numbers) == 0 {
			return nil, true, argError(name)
		}
		total := numbers[0]
		for _, number := range numbers[1:] {
			if name == "IMSUM" {
				total += number
				continue
			}
			total *= number
		}
		return formatComplex(total, suffix), true, nil
	case "IMSUB", "IMDIV", "IMPOWER":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		if name == "IMPOWER" {
			numbers, suffix, err := complexArguments(name, values[:1])
			if err != nil {
				return nil, true, err
			}
			power, ok := toNumber(values[1])
			if !ok {
				return nil, true, formulaError("#VALUE!", "IMPOWER requires a number")
			}
			if numbers[0] == 0 && power <= 0 {
				return nil, true, formulaError("#NUM!", "IMPOWER cannot raise zero to this power")
			}
			return formatComplex(cmplx.Pow(numbers[0], complex(power, 0)), suffix), true, nil
		}
		numbers, suffix, err := complexArguments(name, values)
		if err != nil {
			return nil, true, err
		}
		if len(numbers) != 2 {
			return nil, true, argError(name)
		}
		if name == "IMSUB" {
			return formatComplex(numbers[0]-numbers[1], suffix), true, nil
		}
		if numbers[1] == 0 {
			return nil, true, formulaError("#NUM!", "IMDIV cannot divide by zero")
		}
		return formatComplex(numbers[0]/numbers[1], suffix), true, nil
	}
	return nil, false, nil
}
