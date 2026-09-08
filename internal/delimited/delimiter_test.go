package delimited

import "testing"

func TestDelimiterFollowsTheFirstLine(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		want rune
	}{
		{"쉼표가 기본", "id,amount\n1,2\n", ','},
		{"세미콜론으로 가른 유럽식 CSV", "이름;금액;메모\n김;1200;\n", ';'},
		{"탭으로 가른 표", "id\tamount\tnote\n1\t2\t\n", '\t'},
		{"가르는 것이 없으면 쉼표", "한 줄뿐\n", ','},
		{"수가 같으면 쉼표가 이긴다", "a,b;c\n", ','},
		{"따옴표 안의 세미콜론은 세지 않는다", `이름,"가;나;다;라"`, ','},
		{"따옴표로 감싼 세미콜론 파일", `"이름";"금액";"메모"`, ';'},
		{"따옴표 안의 쉼표는 세지 않는다", `이름;"가,나,다,라"`, ';'},
		{"둘째 줄은 보지 않는다", "id,amount\n김;1;2;3\n", ','},
		{"짝이 맞지 않는 따옴표는 쉼표로 돌아온다", `그가 "말했다; 정말로; 그랬다`, ','},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Delimiter(testCase.body); got != testCase.want {
				t.Fatalf("Delimiter(%q) = %q, want %q", testCase.body, got, testCase.want)
			}
		})
	}
}
