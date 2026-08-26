package workbook

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// 격자와 서버는 같은 글자를 같은 날로 읽어야 한다. 검증은 화면에서 먼저
// 보고 서버가 다시 확정하므로, 둘이 어긋나면 화면이 "괜찮다" 한 값을
// 서버가 물리친다. 사람은 값이 나타났다 사라지는 것을 본다.
//
// 예전에는 격자가 Date.parse 를 그대로 썼다. "2023/03/15" 나 "March 15,
// 2023" 을 받아 주었고, "2023-03-15 00:00:00" 은 그 자리의 시각으로 읽어
// 서버보다 하루 앞선 날이 되기도 했다.
//
// testdata/validation-dates.json 을 web/src/lib/validationDate.fixture.test.ts
// 와 함께 읽는다. 한쪽만 고치면 양쪽 다 걸린다.
func TestValidationDatesMatchTheGrid(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../testdata/validation-dates.json")
	if err != nil {
		t.Fatalf("날짜 목록을 읽지 못했다: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Text string  `json:"text"`
			ISO  *string `json:"iso"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("날짜 목록을 읽지 못했다: %v", err)
	}
	if len(fixture.Cases) < 20 {
		t.Fatalf("날짜 목록이 %d 줄뿐이다. 잘못 읽었다", len(fixture.Cases))
	}
	for _, testCase := range fixture.Cases {
		moment, ok := validationDate(testCase.Text)
		if testCase.ISO == nil {
			if ok {
				t.Errorf("%q 는 날짜가 아니어야 하는데 %s 로 읽었다", testCase.Text, moment.Format(time.RFC3339))
			}
			continue
		}
		if !ok {
			t.Errorf("%q 를 날짜로 읽지 못했다. %s 여야 한다", testCase.Text, *testCase.ISO)
			continue
		}
		if got := moment.UTC().Format("2006-01-02T15:04:05Z"); got != *testCase.ISO {
			t.Errorf("%q 를 %s 로 읽었다. 격자는 %s 로 읽는다", testCase.Text, got, *testCase.ISO)
		}
	}
}
