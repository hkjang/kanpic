package formula

import "strings"

// evaluateKoreanIdentifiers 는 한국에서 쓰는 번호를 다루는 함수들이다.
// 엑셀에도 구글 시트에도 없어, 지금까지는 사람이 매크로를 짜거나 손으로
// 확인하던 자리다.
func evaluateKoreanIdentifiers(name string, values []any) (any, bool, error) {
	switch name {
	case "ISBIZNO", "ISCORPNO":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		digits := onlyDigits(display(values[0]))
		if name == "ISBIZNO" {
			return validBusinessNumber(digits), true, nil
		}
		return validCorporateNumber(digits), true, nil
	case "FORMATBIZNO":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		digits := onlyDigits(display(values[0]))
		if len(digits) != 10 {
			return nil, true, formulaError("#VALUE!", "FORMATBIZNO needs ten digits")
		}
		return digits[:3] + "-" + digits[3:5] + "-" + digits[5:], true, nil
	case "MASKRRN":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		keep := 1
		if len(values) == 2 {
			number, ok := toNumber(values[1])
			if !ok || number < 0 || number > 7 {
				return nil, true, formulaError("#VALUE!", "MASKRRN keeps between 0 and 7 of the trailing digits")
			}
			keep = int(number)
		}
		digits := onlyDigits(display(values[0]))
		// 가릴 수 없는 것을 그대로 돌려주면, 가린 줄 알았던 자리에 번호가
		// 그대로 남는다. 가리는 함수는 못 가렸다고 말해야 한다.
		if len(digits) != 13 {
			return nil, true, formulaError("#VALUE!", "MASKRRN needs thirteen digits")
		}
		return digits[:6] + "-" + digits[6:6+keep] + strings.Repeat("*", 7-keep), true, nil
	}
	return nil, false, nil
}

func onlyDigits(text string) string {
	var out strings.Builder
	for _, character := range text {
		if character >= '0' && character <= '9' {
			out.WriteRune(character)
		}
	}
	return out.String()
}

// validBusinessNumber 는 사업자등록번호의 검사 숫자를 본다.
//
// 국세청이 쓰는 셈이다. 앞 아홉 자리에 1,3,7 을 되풀이해 곱하고, 아홉째
// 자리는 5를 곱한 몫을 한 번 더 더한다. 마지막 자리가 그 나머지를 10에서
// 뺀 값과 같아야 한다.
func validBusinessNumber(digits string) bool {
	if len(digits) != 10 {
		return false
	}
	weights := []int{1, 3, 7, 1, 3, 7, 1, 3, 5}
	sum := 0
	for index, weight := range weights {
		sum += int(digits[index]-'0') * weight
	}
	sum += int(digits[8]-'0') * 5 / 10
	return (10-sum%10)%10 == int(digits[9]-'0')
}

// validCorporateNumber 는 법인등록번호의 검사 숫자를 본다. 앞 열두 자리에
// 1과 2를 번갈아 곱한다.
func validCorporateNumber(digits string) bool {
	if len(digits) != 13 {
		return false
	}
	sum := 0
	for index := 0; index < 12; index++ {
		weight := 1
		if index%2 == 1 {
			weight = 2
		}
		sum += int(digits[index]-'0') * weight
	}
	return (10-sum%10)%10 == int(digits[12]-'0')
}
