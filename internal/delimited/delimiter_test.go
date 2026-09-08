package delimited

import (
	"strings"
	"testing"
)

func TestDelimiterFollowsTheLinesThatAgree(t *testing.T) {
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
		{"줄 끝의 CR 은 세는 데 걸리지 않는다", "이름;금액\r\n김;1200\r\n", ';'},
		{"한 줄씩만 가르는 것들은 쉼표로 갈린다", "id,amount\n김;1;2;3\n", ','},
		{"짝이 맞지 않는 따옴표는 쉼표로 돌아온다", `그가 "말했다; 정말로; 그랬다`, ','},
		// 보고서를 내보내는 도구들은 표 위에 제목이나 빈 줄을 한 줄 얹는다.
		// 그 줄은 아무것으로도 갈리지 않으므로 파일이 어떻게 갈리는지에 대해
		// 아무 말도 하지 않는다 — 그 아래 줄들이 정한다.
		{"제목 줄 아래의 세미콜론 파일", "2026년 1분기 보고서\n지점;매출;비고\n서울;1200;\n부산;900;\n", ';'},
		{"빈 첫 줄 아래의 세미콜론 파일", "\n지점;매출\n서울;1200\n부산;900\n", ';'},
		{"제목에 쉼표가 있어도 여러 줄이 이긴다", "보고서, 1분기\n지점;매출\n서울;1200\n부산;900\n", ';'},
		{"제목 줄 아래의 탭 파일", "매출 보고서\n지점\t매출\n서울\t1200\n", '\t'},
		// 본문에 흩어져 있는 세미콜론은 줄마다 같은 수로 나오지 않는다.
		{"본문에 섞인 세미콜론이 열을 가르지 않는다", "id,name,note\n1,김,가;나\n2,박,다;라;마\n3,최,바\n", ','},
		{"열 수가 들쭉날쭉해도 가르는 것은 하나다", "a,b,c\n1,2\n3,4,5,6\n7,8\n", ','},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Delimiter(testCase.body); got != testCase.want {
				t.Fatalf("Delimiter(%q) = %q, want %q", testCase.body, got, testCase.want)
			}
		})
	}
}

// 규칙은 머리의 몇 줄만 읽는다. 파일 전체를 세면 이십 메가바이트짜리 업로드를
// 가르는 것을 고르려고 통째로 훑게 된다.
func TestDelimiterReadsOnlyTheHead(t *testing.T) {
	body := strings.Repeat("a,b,c\n", maxSampledLines) + strings.Repeat("가;나;다;라\n", 1000)
	if got := Delimiter(body); got != ',' {
		t.Fatalf("Delimiter = %q, want ','", got)
	}
}
