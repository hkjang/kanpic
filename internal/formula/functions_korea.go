package formula

import (
	"math"
	"strings"
)

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
	case "HANGULNUM", "HANGULWON":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" needs a number")
		}
		// 원 단위 아래는 한글로 적을 자리가 없다. 조용히 버리면 적힌 금액과
		// 셈한 금액이 달라지므로, 반올림은 사람이 뜻을 가지고 하게 둔다.
		if number != math.Trunc(number) {
			return nil, true, formulaError("#VALUE!", name+" needs a whole number; round it first")
		}
		if math.Abs(number) > maxHangulAmount {
			return nil, true, formulaError("#NUM!", name+" cannot spell an amount that large exactly")
		}
		amount := int64(math.Abs(number))
		text := hangulNumber(amount)
		if name == "HANGULWON" {
			text = "일금 " + text + "원정"
		}
		if number < 0 {
			text = "마이너스 " + text
		}
		return text, true, nil
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

// hangulDigits 는 자릿수 글자다. 십·백·천은 네 자리 묶음 안에서 쓰고,
// 만·억·조는 묶음마다 붙인다.
var (
	hangulDigits = []rune("영일이삼사오육칠팔구")
	hangulSmall  = []string{"", "십", "백", "천"}
	hangulBig    = []string{"", "만", "억", "조"}
)

// maxHangulAmount 는 한글로 적을 수 있는 가장 큰 금액이다.
//
// 표 프로그램의 숫자는 배정도 실수라, 9,007,199,254,740,992 를 넘으면 정수를
// 그대로 담지 못한다. 담지 못한 값을 한글로 적으면 세금계산서에 틀린 금액이
// 조용히 적힌다. 그럴 바에는 못 적는다고 말하는 편이 낫다.
const maxHangulAmount = 9_007_199_254_740_992

// hangulNumber 는 수를 한글로 적는다. 네 자리씩 끊어 만·억·조를 붙인다.
//
// 십·백·천 앞의 일은 뺀다 — 15 는 십오지 일십오가 아니다. 다만 만·억·조
// 앞에서는 남긴다: 10000 은 일만이다. 문서에 적는 꼴이 그렇다.
func hangulNumber(amount int64) string {
	if amount == 0 {
		return "영"
	}
	parts := make([]string, 0, 4)
	for index := 0; amount > 0; index++ {
		group := amount % 10000
		if group != 0 {
			text := hangulGroup(int(group))
			if index > 0 && group == 1 {
				text = "일"
			}
			parts = append(parts, text+hangulBig[index])
		}
		amount /= 10000
	}
	var out strings.Builder
	for index := len(parts) - 1; index >= 0; index-- {
		out.WriteString(parts[index])
	}
	return out.String()
}

func hangulGroup(group int) string {
	var out strings.Builder
	for position := 3; position >= 0; position-- {
		digit := (group / powerOfTen(position)) % 10
		if digit == 0 {
			continue
		}
		if position > 0 && digit == 1 {
			out.WriteString(hangulSmall[position])
			continue
		}
		out.WriteRune(hangulDigits[digit])
		out.WriteString(hangulSmall[position])
	}
	return out.String()
}

func powerOfTen(exponent int) int {
	result := 1
	for index := 0; index < exponent; index++ {
		result *= 10
	}
	return result
}
