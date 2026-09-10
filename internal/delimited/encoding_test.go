package delimited

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// asUTF16 writes text the way Windows PowerShell 과 엑셀의 '유니코드 텍스트' 저장이
// 쓴다 — 어느 쪽 끝부터 읽는지 알리는 표시를 앞에 두고 두 바이트씩.
func asUTF16(text string, order binary.ByteOrder) []byte {
	units := utf16.Encode([]rune(text))
	data := make([]byte, 0, 2+len(units)*2)
	if order == binary.LittleEndian {
		data = append(data, 0xFF, 0xFE)
	} else {
		data = append(data, 0xFE, 0xFF)
	}
	for _, unit := range units {
		pair := make([]byte, 2)
		order.PutUint16(pair, unit)
		data = append(data, pair...)
	}
	return data
}

func TestToUTF8ReadsWhatTheFileSaysItIs(t *testing.T) {
	for _, testCase := range []struct {
		name string
		data []byte
		want string
	}{
		{name: "표시가 없으면 그대로 둔다", data: []byte("지점,매출\n서울,1200\n"), want: "지점,매출\n서울,1200\n"},
		{name: "UTF-8 표시는 뗀다", data: append([]byte{0xEF, 0xBB, 0xBF}, "지점,매출\n"...), want: "지점,매출\n"},
		{name: "UTF-16LE 를 옮겨 읽는다", data: asUTF16("지점,매출\r\n서울,1200\r\n", binary.LittleEndian), want: "지점,매출\r\n서울,1200\r\n"},
		{name: "UTF-16BE 를 옮겨 읽는다", data: asUTF16("지점,매출\r\n서울,1200\r\n", binary.BigEndian), want: "지점,매출\r\n서울,1200\r\n"},
		{name: "두 칸을 차지하는 글자도 온전하다", data: asUTF16("이모지,🙂\n", binary.LittleEndian), want: "이모지,🙂\n"},
		{name: "빈 파일도 표시만 떼고 만다", data: []byte{0xFF, 0xFE}, want: ""},
		{name: "반만 적힌 마지막 글자는 버린다", data: append(asUTF16("가", binary.LittleEndian), 0x00), want: "가"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := string(ToUTF8(testCase.data)); got != testCase.want {
				t.Fatalf("%q 를 읽어 %q 가 나왔다, 원하는 것은 %q", testCase.data, got, testCase.want)
			}
		})
	}
}

// UTF-32LE 는 UTF-16LE 와 같은 표시로 열고 뒤에 0 을 둘 더 둔다. 우리는 그것을 읽지
// 못하므로, 반쯤 읽어 글자 아닌 것을 만들어 내는 대신 손대지 않고 두어 부르는 쪽이
// 거절하게 한다.
func TestToUTF8DoesNotMistakeUTF32ForUTF16(t *testing.T) {
	data := []byte{0xFF, 0xFE, 0x00, 0x00, 'A', 0x00, 0x00, 0x00}
	if got := ToUTF8(data); string(got) != string(data) {
		t.Fatalf("읽지 못하는 파일에 손을 댔다: %q", got)
	}
}

// 표시가 없는 파일은 아무것도 짐작하지 않는다 — UTF-16 처럼 두 바이트씩 읽어도 말이
// 되는 UTF-8 파일이 있으므로, 짐작은 멀쩡한 파일을 망가뜨리는 쪽으로만 틀린다.
func TestToUTF8GuessesNothing(t *testing.T) {
	body := []byte("a,b\n1,2\n")
	if got := ToUTF8(body); string(got) != string(body) {
		t.Fatalf("표시 없는 파일을 옮겨 읽었다: %q", got)
	}
	broken := []byte{0xFF, 'a', ',', 'b'}
	if got := ToUTF8(broken); string(got) != string(broken) {
		t.Fatalf("표시가 아닌 0xFF 를 표시로 보았다: %q", got)
	}
}
