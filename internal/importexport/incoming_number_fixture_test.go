package importexport

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type incomingNumberCase struct {
	Text   string  `json:"text"`
	Number bool    `json:"number"`
	Value  float64 `json:"value"`
}

// 밖에서 온 글자를 칸에 수로 담는 규칙은 문이 둘이다 — 파일(업로드
// 가져오기·IMPORTDATA)과 클립보드(평문·HTML 붙여넣기). 같은 표를 CSV 로
// 올리느냐 복사해 붙이느냐에 따라 우편번호의 앞자리 0 이 사라지면 사람은
// 자기가 옮긴 표와 다른 것을 보게 된다.
//
// testdata/incoming-number.json 을 웹의 clipboardNumber.fixture.test.ts 와
// 함께 읽는다 — 한쪽 문만 고치면 양쪽 다 걸린다. 여기서는 업로드 문을 실제로
// 통과시킨다(Parse 가 parseScalar 를 거쳐 delimited.Number 를 부른다).
// IMPORTDATA 문이 업로드와 같은 값을 내는 것은 internal/external 의
// TestImportDataKeepsNumbersThatUploadKeepsAsText 가 붙들고 있다.
func TestIncomingNumberFixtureHoldsAtTheUploadDoor(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../testdata/incoming-number.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []incomingNumberCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 25 {
		t.Fatalf("자료가 %d개뿐이다 — 파일을 잘못 읽었다", len(fixture.Cases))
	}

	// 한 줄에 한 칸씩 담은 CSV 를 실제 업로드 문으로 넣는다.
	lines := make([]string, 0, len(fixture.Cases))
	for _, item := range fixture.Cases {
		if strings.ContainsAny(item.Text, ",\"\r\n\t") {
			t.Fatalf("%q 는 CSV 한 칸에 그대로 담을 수 없다 — 이 자료는 평문 숫자만 다룬다", item.Text)
		}
		lines = append(lines, item.Text)
	}
	parsed, err := Parse("번호.csv", []byte(strings.Join(lines, "\n")+"\n"), 0)
	if err != nil {
		t.Fatalf("가져오지 못했다: %v", err)
	}
	stored := map[int]any{}
	for _, cell := range parsed.Sheets[0].Cells {
		var value any
		if err := json.Unmarshal(cell.Value, &value); err != nil {
			t.Fatalf("%d:%d 칸을 읽지 못했다: %v", cell.Row, cell.Column, err)
		}
		if cell.Column != 1 {
			t.Fatalf("%d번째 줄이 %d칸으로 갈렸다", cell.Row, cell.Column)
		}
		stored[cell.Row] = value
	}
	for index, item := range fixture.Cases {
		got := stored[index+1]
		if item.Number {
			if number, ok := got.(float64); !ok || number != item.Value {
				t.Errorf("%q -> %#v, 수 %v 로 담겨야 한다", item.Text, got, item.Value)
			}
			continue
		}
		if text, ok := got.(string); !ok || text != item.Text {
			t.Errorf("%q -> %#v, 적힌 글자 그대로 남아야 한다", item.Text, got)
		}
	}
}
