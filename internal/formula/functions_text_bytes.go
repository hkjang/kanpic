package formula

import (
	"math"
	"strings"
)

// 한글과 한자는 엑셀이 **두 바이트** 로 센다. 그래서 LEN 과 LENB 의 답이
// 다르고, 자리를 바이트로 세는 함수들이 따로 있다. 한국어 문서를 다루는
// 표에서는 이쪽이 필요한 자리가 있다.
//
//	LEN("한글")  -> 2
//	LENB("한글") -> 4
//
// 자리를 바이트로 셀 때 두 바이트짜리 글자의 **가운데** 가 잘리면 엑셀은
// 그 반쪽을 공백 하나로 바꾼다. 반쪽 글자를 낼 수는 없기 때문이다.
func evaluateByteText(name string, values []any) (any, bool, error) {
	switch name {
	case "LENB", "LEFTB", "RIGHTB", "MIDB", "FINDB", "SEARCHB", "REPLACEB":
	default:
		return nil, false, nil
	}
	if len(values) < 1 {
		return nil, true, argError(name)
	}
	switch name {
	case "LENB":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return float64(byteLength(display(values[0]))), true, nil
	case "LEFTB", "RIGHTB":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		text := display(values[0])
		count := 1.0
		if len(values) == 2 && !omitted(values[1]) {
			number, ok := toNumber(values[1])
			if !ok || number < 0 {
				return nil, true, formulaError("#VALUE!", name+" needs a count of 0 or more")
			}
			count = math.Trunc(number)
		}
		if name == "LEFTB" {
			return sliceBytes(text, 1, int(count)), true, nil
		}
		total := byteLength(text)
		start := total - int(count) + 1
		if start < 1 {
			start = 1
		}
		return sliceBytes(text, start, int(count)), true, nil
	case "MIDB":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		text := display(values[0])
		start, count, err := twoNumbers(name, values[1:3])
		if err != nil {
			return nil, true, err
		}
		if start < 1 || count < 0 {
			return nil, true, formulaError("#VALUE!", "MIDB needs a start of 1 or more and a count of 0 or more")
		}
		return sliceBytes(text, int(math.Trunc(start)), int(math.Trunc(count))), true, nil
	case "FINDB", "SEARCHB":
		if len(values) < 2 || len(values) > 3 {
			return nil, true, argError(name)
		}
		needle, haystack := display(values[0]), display(values[1])
		start := 1
		if len(values) == 3 && !omitted(values[2]) {
			number, ok := toNumber(values[2])
			if !ok || number < 1 {
				return nil, true, formulaError("#VALUE!", name+" needs a start of 1 or more")
			}
			start = int(math.Trunc(number))
		}
		// 시작 자리도 바이트로 센다. 글자 자리로 세면 한글이 섞였을 때
		// 엉뚱한 곳에서 찾기 시작한다.
		offset := runesBefore(haystack, start)
		if offset < 0 {
			return nil, true, formulaError("#VALUE!", name+" start is past the end of the text")
		}
		body := string([]rune(haystack)[offset:])
		index := strings.Index(body, needle)
		if name == "SEARCHB" {
			index = strings.Index(strings.ToLower(body), strings.ToLower(needle))
		}
		if index < 0 {
			return nil, true, formulaError("#VALUE!", name+" did not find the text")
		}
		return float64(byteLength(string([]rune(haystack)[:offset])) + byteLength(body[:index]) + 1), true, nil
	case "REPLACEB":
		if len(values) != 4 {
			return nil, true, argError(name)
		}
		text := display(values[0])
		start, count, err := twoNumbers(name, values[1:3])
		if err != nil {
			return nil, true, err
		}
		if start < 1 || count < 0 {
			return nil, true, formulaError("#VALUE!", "REPLACEB needs a start of 1 or more and a count of 0 or more")
		}
		head := sliceBytes(text, 1, int(math.Trunc(start))-1)
		tail := sliceBytes(text, int(math.Trunc(start))+int(math.Trunc(count)), byteLength(text))
		return head + display(values[3]) + tail, true, nil
	}
	return nil, false, nil
}

// wideRune 은 엑셀이 두 바이트로 세는 글자인지 본다. 한글·한자·가나와
// 전각 기호가 여기 든다.
func wideRune(value rune) bool {
	switch {
	case value >= 0x1100 && value <= 0x115F, // 한글 자모
		value >= 0x2E80 && value <= 0xA4CF, // 한자와 부수, 가나
		value >= 0xAC00 && value <= 0xD7A3, // 한글 음절
		value >= 0xF900 && value <= 0xFAFF, // 호환 한자
		value >= 0xFE30 && value <= 0xFE6F, // 세로쓰기 기호
		value >= 0xFF00 && value <= 0xFF60, // 전각 영숫자
		value >= 0xFFE0 && value <= 0xFFE6: // 전각 통화기호
		return true
	}
	return false
}

func byteLength(text string) int {
	total := 0
	for _, character := range text {
		if wideRune(character) {
			total += 2
			continue
		}
		total++
	}
	return total
}

// sliceBytes 는 바이트 자리로 잘라 낸다. 두 바이트짜리 글자의 가운데가
// 잘리면 그 반쪽을 공백 하나로 바꾼다.
func sliceBytes(text string, start, count int) string {
	if count <= 0 {
		return ""
	}
	var out strings.Builder
	position := 1
	for _, character := range text {
		width := 1
		if wideRune(character) {
			width = 2
		}
		first, last := position, position+width-1
		position += width
		if last < start || first > start+count-1 {
			continue
		}
		if first >= start && last <= start+count-1 {
			out.WriteRune(character)
			continue
		}
		// 반쪽만 걸렸다.
		out.WriteRune(' ')
	}
	return out.String()
}

// runesBefore 는 바이트 자리 앞에 글자가 몇 개 있는지 센다.
func runesBefore(text string, byteStart int) int {
	position := 1
	for index, character := range []rune(text) {
		if position >= byteStart {
			return index
		}
		if wideRune(character) {
			position += 2
			continue
		}
		position++
	}
	if position >= byteStart {
		return len([]rune(text))
	}
	return -1
}

// ASC 는 전각을 반각으로, JIS 는 반각을 전각으로 바꾼다. 붙여넣은 글에
// 전각 숫자가 섞이면 숫자로 읽히지 않으므로 고칠 자리가 필요하다.
func evaluateWidthConversion(name string, values []any) (any, bool, error) {
	switch name {
	case "ASC", "JIS", "DBCS":
	default:
		return nil, false, nil
	}
	if len(values) != 1 {
		return nil, true, argError(name)
	}
	text := display(values[0])
	var out strings.Builder
	for _, character := range text {
		if name == "ASC" {
			switch {
			case character >= 0xFF01 && character <= 0xFF5E:
				out.WriteRune(character - 0xFEE0)
				continue
			case character == 0x3000:
				out.WriteRune(' ')
				continue
			}
			out.WriteRune(character)
			continue
		}
		switch {
		case character >= '!' && character <= '~':
			out.WriteRune(character + 0xFEE0)
		case character == ' ':
			out.WriteRune(0x3000)
		default:
			out.WriteRune(character)
		}
	}
	return out.String(), true, nil
}
