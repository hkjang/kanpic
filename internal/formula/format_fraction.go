package formula

import (
	"math"
	"strconv"
	"strings"
)

// 분수 서식은 값을 소수가 아니라 분수로 적는다 — 2.75 는 "2 3/4" 다.
// 엑셀 기본 서식 12·13 번(`# ?/?`, `# ??/??`)이라 XLSX 로 그냥 들어온다.
//
// 예전에는 자리 기호만 세고 "/" 를 그냥 글자로 흘려 보내, `# ?/?` 가 0.5 를
// "1/2" 가 아니라 "1/" 로 그렸다 — 반올림한 값 뒤에 빗금만 남은 꼴이다.
//
// web/src/lib/cellFormat.ts 의 parseFractionFormat·renderFraction 이 같은
// 규칙을 쓴다. testdata/cell-formats.json 이 둘을 함께 붙잡는다.

// maxFractionValue 를 넘는 값은 분수로 적지 않는다. 그만큼 큰 수에서는
// 소수 부분이 이미 이진 실수의 어긋남뿐이고, 가분수의 분자가 int 를 넘는다.
const maxFractionValue = 1e15

// fractionFormat 은 분수 서식 한 구역을 뜯어 놓은 것이다.
type fractionFormat struct {
	prefix, suffix string
	// integer 는 정수 자리 기호다("#", "#,##0"). 비어 있으면 정수 자리가
	// 따로 없다는 뜻이고, 그때는 가분수로 적는다 — `#/#` 은 5.25 를 "21/4"
	// 로 적는다. 엑셀은 빈칸으로 갈라 적은 것만 대분수로 본다.
	integer string
	// denominator 는 서식에 숫자로 못 박은 분모다(`?/8` 의 8). 0 이면
	// 자리 기호 개수로 분모의 상한을 정한다 — `?/?` 는 9, `??/??` 는 99 다.
	denominator    int
	maxDenominator int
}

// parseFractionFormat 은 서식 한 구역이 분수 서식인지 보고 뜯는다.
//
// 자리 기호가 빗금 양쪽에 붙어 있어야 분수 서식이다. 날짜 서식의 빗금은
// "m/d/yyyy" 처럼 글자 사이에 있으므로 여기 걸리지 않는다.
func parseFractionFormat(section string) (fractionFormat, bool) {
	runes := []rune(section)
	slash := -1
	for index := 0; index < len(runes) && slash < 0; index++ {
		switch character := runes[index]; character {
		case '\\':
			index++
		case '"', '[':
			closer := '"'
			if character == '[' {
				closer = ']'
			}
			for index++; index < len(runes) && runes[index] != closer; index++ {
			}
		case '/':
			slash = index
		}
	}
	if slash < 0 {
		return fractionFormat{}, false
	}
	// 분자는 빗금 바로 앞에 이어진 자리 기호다.
	start := slash
	for start > 0 && strings.ContainsRune("0#?", runes[start-1]) {
		start--
	}
	if start == slash {
		return fractionFormat{}, false
	}
	// 분모는 빗금 바로 뒤에 이어진 자리 기호이거나, 못 박은 숫자다.
	end := slash + 1
	for end < len(runes) && strings.ContainsRune("0#?", runes[end]) {
		end++
	}
	spec := fractionFormat{}
	switch places := end - slash - 1; {
	case places > 0:
		if places > 9 {
			places = 9
		}
		spec.maxDenominator = int(math.Pow10(places)) - 1
	default:
		for end < len(runes) && runes[end] >= '0' && runes[end] <= '9' {
			end++
		}
		fixed, err := strconv.Atoi(string(runes[slash+1 : end]))
		if err != nil || fixed <= 0 {
			return fractionFormat{}, false
		}
		spec.denominator = fixed
	}
	head, integer, headTail := splitFormatSection(string(runes[:start]))
	tailHead, _, tail := splitFormatSection(string(runes[end:]))
	spec.prefix, spec.suffix = head+headTail, tailHead+tail
	if strings.ContainsAny(integer, "0#?") {
		spec.integer = integer
	}
	return spec, true
}

// renderFraction 은 값을 분수 서식대로 적는다.
func renderFraction(number float64, spec fractionFormat) string {
	value := math.Abs(number)
	whole := 0.0
	if spec.integer != "" {
		whole = math.Floor(value)
		value -= whole
	}
	numerator, denominator := bestFraction(value, spec)
	// 0.99 를 `# ?/?` 로 적으면 분자가 분모까지 올라간다. 1/1 이 아니라
	// 정수 자리로 올려 적는다.
	if spec.integer != "" && numerator >= float64(denominator) {
		whole += math.Floor(numerator / float64(denominator))
		numerator = math.Mod(numerator, float64(denominator))
	}
	grouped := strings.Contains(spec.integer, ",")
	body := ""
	switch {
	case spec.integer != "" && numerator == 0:
		body = formatNumber(whole, 0, grouped)
	case spec.integer != "" && (whole != 0 || strings.Contains(spec.integer, "0")):
		body = formatNumber(whole, 0, grouped) + " " + fractionText(numerator, denominator)
	default:
		body = fractionText(numerator, denominator)
	}
	rendered := spec.prefix + body + spec.suffix
	// -0.02 를 `# ?/?` 로 적으면 0 이 된다. "-0" 은 잘못 그린 것처럼 읽힌다.
	if number < 0 && !(whole == 0 && numerator == 0) && !strings.HasPrefix(rendered, "-") {
		rendered = "-" + rendered
	}
	return rendered
}

func fractionText(numerator float64, denominator int) string {
	return strconv.FormatFloat(numerator, 'f', -1, 64) + "/" + strconv.Itoa(denominator)
}

// bestFraction 은 값에 가장 가까운 분수를 고른다. 분모를 못 박은 서식이면
// 그 분모를 그대로 쓰고(`?/8` 은 0.5 를 4/8 로 적는다), 아니면 자리 기호가
// 허락하는 분모를 모두 재어 가장 덜 어긋나는 것을 고른다. 같은 만큼
// 어긋나면 분모가 작은 쪽이다 — 0.5 는 `# ??/??` 에서도 1/2 다.
func bestFraction(value float64, spec fractionFormat) (float64, int) {
	if spec.denominator > 0 {
		return math.Round(value * float64(spec.denominator)), spec.denominator
	}
	bestDenominator, bestNumerator := 1, math.Round(value)
	bestError := math.Abs(value - bestNumerator)
	for denominator := 2; denominator <= spec.maxDenominator; denominator++ {
		numerator := math.Round(value * float64(denominator))
		if difference := math.Abs(value - numerator/float64(denominator)); difference < bestError-1e-12 {
			bestDenominator, bestNumerator, bestError = denominator, numerator, difference
		}
	}
	return bestNumerator, bestDenominator
}
