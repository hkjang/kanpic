package workbook

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

// 정렬은 두 번 일어난다. 화면이 먼저 자기 계산으로 줄을 옮겨 보여주고,
// 곧이어 서버가 보낸 결과가 그 위에 덮인다. 두 계산이 다른 답을 내면 줄이
// 눈앞에서 한 번 튄다.
//
// 실제로 두 번 어긋났었다. 화면은 UTF-16 조각을, 서버는 UTF-8 바이트를
// 견주고 있었고(v0.123.4), 대소문자를 낮추는 방식도 달랐다(v0.123.5).
// 둘 다 사람 눈에는 보이지 않다가 줄이 튀는 순간에만 드러난다.
//
// testdata/sort-order.json 을 web/src/lib/naturalOrder.test.ts 와 함께
// 읽는다. 한쪽만 고치면 양쪽 다 걸린다.
func TestSortOrderMatchesTheGrid(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../testdata/sort-order.json")
	if err != nil {
		t.Fatalf("정렬 목록을 읽지 못했다: %v", err)
	}
	var fixture struct {
		Corpus              []string `json:"corpus"`
		Sorted              []string `json:"sorted"`
		SortedCaseSensitive []string `json:"sortedCaseSensitive"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("정렬 목록을 읽지 못했다: %v", err)
	}
	if len(fixture.Corpus) < 100 {
		t.Fatalf("목록이 %d 줄뿐이다. 잘못 읽었다", len(fixture.Corpus))
	}
	folder := newCaseFolder()
	for _, testCase := range []struct {
		name      string
		sensitive bool
		want      []string
	}{
		{"대소문자를 무시할 때", false, fixture.Sorted},
		{"대소문자를 구분할 때", true, fixture.SortedCaseSensitive},
	} {
		items := append([]string(nil), fixture.Corpus...)
		sort.SliceStable(items, func(first, second int) bool {
			left, right := items[first], items[second]
			if !testCase.sensitive {
				left, right = folder.String(left), folder.String(right)
			}
			return compareNatural(left, right) < 0
		})
		if reflect.DeepEqual(items, testCase.want) {
			continue
		}
		for index := range items {
			if items[index] != testCase.want[index] {
				t.Fatalf("%s: %d번째가 %q 인데 격자는 %q 를 놓는다", testCase.name, index, items[index], testCase.want[index])
			}
		}
	}
}
