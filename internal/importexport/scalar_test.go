package importexport

import "testing"

// CSV 로 들어오는 값도 붙여넣기와 같은 한도를 지켜야 한다. 스무 자리
// 계좌번호를 실수로 읽으면 파일에 적힌 것과 다른 값이 칸에 들어가고,
// 되돌릴 방법이 없다.
//
// 짝: web/src/lib/spreadsheetNumber.ts 의 significantDigits,
// web/src/lib/clipboardNumber.ts 의 parsePastedNumber.
func TestParseScalarKeepsNumbersItCannotHoldExactly(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		text string
		want any
	}{
		{"1000", 1000.0},
		{"1.5", 1.5},
		{"999999999999999", 999999999999999.0},
		{"12345678901234567890", "12345678901234567890"},
		{"-12345678901234567890", "-12345678901234567890"},
		{"1234567890.12345678", "1234567890.12345678"},
		// 앞의 0 은 번호를 뜻하므로 예전부터 글자로 두었다.
		{"007", "007"},
		// 지수로 적은 것은 사람이 수로 적은 것이다.
		{"1e30", 1e30},
	} {
		if got := parseScalar(testCase.text); got != testCase.want {
			t.Errorf("parseScalar(%q) = %#v, want %#v", testCase.text, got, testCase.want)
		}
	}
}

// 가져오기 미리보기가 글자로 담긴 숫자를 세어 알려 준다. 조용히 두면
// =SUM 이 그 칸들을 빼고 셈하는데 이유가 아무 데도 적히지 않는다.
func TestLooksLikeNumberStoredAsText(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"1,234", "₩5,000", "(500)", "50%", "1,234원", "$1,234.50"} {
		if !looksLikeNumberStoredAsText(text) {
			t.Errorf("%q 는 글자로 담긴 숫자로 세어야 한다", text)
		}
	}
	for _, text := range []string{"", "사과", "1000", "1.5", "-", "007", "abc,def"} {
		if looksLikeNumberStoredAsText(text) {
			t.Errorf("%q 는 글자로 담긴 숫자가 아니다", text)
		}
	}
}
