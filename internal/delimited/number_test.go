package delimited

import "testing"

// 파일에서 온 값을 칸에 담는 자는 수식 엔진의 자보다 좁다. 셈에서는 "00123"
// 이 123 이어도 되지만, 칸에 그렇게 담으면 우편번호의 앞자리 0 과 계좌번호의
// 뒷자리가 사라져 파일에 적힌 것과 다른 값이 남는다.
func TestNumberKeepsWhatAFileCannotGetBack(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		text string
		want float64
		ok   bool
	}{
		{"1000", 1000, true},
		{"-2.5", -2.5, true},
		{"1e3", 1000, true},
		{".5", 0.5, true},
		{"5.", 5, true},
		// 한 자리 0 과 소수점 앞의 0 은 자리이지 번호가 아니다.
		{"0", 0, true},
		{"0.5", 0.5, true},
		{"-0", 0, true},
		{"+0", 0, true},
		{"007.5", 7.5, true},
		// 앞자리 0 이 두 자리 이상이고 소수점이 없으면 번호다.
		{"00123", 0, false},
		{"007", 0, false},
		{"-007", 0, false},
		{"+007", 0, false},
		{"00", 0, false},
		// 열여섯 자리 넘는 정수·소수는 배정밀도가 정확히 담지 못한다.
		{"999999999999999", 999999999999999, true},
		{"12345678901234567890", 0, false},
		{"-12345678901234567890", 0, false},
		{"1234567890123456.5", 0, false},
		{"1234567890.12345678", 0, false},
		// 지수로 적은 것은 사람이 수로 적은 것이다.
		{"1e30", 1e30, true},
		// Go 만 수라고 부르는 것은 수식 엔진의 자가 이미 거른다.
		{"NaN", 0, false},
		{"Inf", 0, false},
		{"1_000", 0, false},
		{"0x1p4", 0, false},
		{"", 0, false},
		{"사과", 0, false},
		{"1,200", 0, false},
		{"12%", 0, false},
		// 앞뒤 빈칸은 부르는 쪽이 정한다.
		{" 12", 0, false},
	} {
		got, ok := Number(testCase.text)
		if ok != testCase.ok || got != testCase.want {
			t.Errorf("Number(%q) = %v, %v; want %v, %v", testCase.text, got, ok, testCase.want, testCase.ok)
		}
	}
}
