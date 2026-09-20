package delimited

import (
	"regexp"
	"strings"

	"kanpic/internal/formula"
)

// Number 는 파일에서 온 한 칸을 수로 담을지 정한다 — 수식 엔진의 자
// (formula.DecimalNumber)에 맞되, 수로 담으면 파일에 적힌 것과 다른 값이
// 남는 두 가지는 글자로 둔다.
//
// 셈에 끼어드는 값과 파일에서 온 값의 규칙은 일부러 다르다. =SUM 에 글자
// "00123" 이 들어오면 123 으로 세는 것이 맞고 엑셀도 그렇다. 그러나 파일의
// "00123" 을 칸에 123 으로 담으면 우편번호·사번의 앞자리 0 이 사라지고, 스무
// 자리 계좌번호는 1.2345678901234567e+19 로 뭉개져 되돌릴 길이 없다. 칸에
// 담는 자리는 업로드 가져오기와 IMPORTDATA 두 문인데, 같은 파일이 어느 문으로
// 들어오든 같은 표여야 하므로 그 규칙은 여기 하나뿐이다.
//
// 앞뒤 빈칸은 부르는 쪽이 정한다 — formula.DecimalNumber 와 같다.
func Number(text string) (float64, bool) {
	if hasSignificantLeadingZero(text) || tooLongToHoldExactly(text) {
		return 0, false
	}
	return formula.DecimalNumber(text)
}

// hasSignificantLeadingZero 는 앞의 0 이 번호를 뜻하는지 본다. "0"·"0.5"·"-0"
// 의 0 은 자리이고, "00123"·"007" 처럼 두 자리 이상이면서 소수점이 없을 때만
// 번호다.
func hasSignificantLeadingZero(value string) bool {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	return len(trimmed) > 1 && trimmed[0] == '0' && trimmed[1] >= '0' && trimmed[1] <= '9' && !strings.Contains(trimmed, ".")
}

// tooLongToHoldExactly 는 배정밀도가 정확히 담지 못할 만큼 긴 수인지 본다.
//
// 스무 자리 계좌번호를 실수로 읽으면 1.2345678901234567e+19 가 되어 뒤가
// 뭉개진다. 파일에 적힌 것과 다른 값이 칸에 들어가고, 되돌릴 방법이 없다.
// 그런 것은 금액이 아니라 번호이므로 글자로 둔다 — 적어도 그대로 남는다.
//
// 지수로 적은 것(1e30)은 사람이 수로 적은 것이므로 건드리지 않는다.
// web/src/lib/spreadsheetNumber.ts 의 significantDigits 와 같은 한도다.
func tooLongToHoldExactly(value string) bool {
	if !plainNumber.MatchString(value) {
		return false
	}
	digits := strings.TrimLeft(strings.Replace(strings.TrimLeft(value, "+-"), ".", "", 1), "0")
	return len(digits) > 15
}

var plainNumber = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)
