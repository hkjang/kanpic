package formula

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 사용자 가이드가 없는 함수를 설명하고 있으면 안 된다. 문서를 보고 그대로
// 적었는데 #NAME? 이 나오면, 무엇이 잘못됐는지 알아낼 방법이 없다.
//
// 함수 목록(catalog)이 실제 셈과 맞는지는 다른 시험이 지킨다. 여기서는
// **가이드 문서** 가 목록과 맞는지 본다. 문서는 사람이 손으로 쓰므로 함수
// 이름을 바꾸거나 뺄 때 함께 고쳐지지 않기 쉽다.
func TestEveryFunctionTheGuideMentionsExists(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../docs/USER_GUIDE.md")
	if err != nil {
		t.Fatalf("사용자 가이드를 읽지 못했다: %v", err)
	}
	guide := string(raw)
	mentioned := map[string]struct{}{}
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile("`=([A-Z][A-Z0-9_.]*)\\s*\\("),
		regexp.MustCompile("`([A-Z][A-Z0-9_.]{1,20})\\("),
	} {
		for _, match := range pattern.FindAllStringSubmatch(guide, -1) {
			mentioned[match[1]] = struct{}{}
		}
	}
	if len(mentioned) < 50 {
		t.Fatalf("가이드에서 함수 이름을 %d개만 찾았다. 뽑는 방식이 잘못됐다", len(mentioned))
	}
	names := make([]string, 0, len(mentioned))
	for name := range mentioned {
		names = append(names, name)
	}
	sort.Strings(names)

	engine := New()
	missing := make([]string, 0)
	for _, name := range names {
		// 인수 없이 불러도 없는 이름이면 #NAME? 이 난다. 인수 개수가
		// 틀렸다는 오류는 이름이 있다는 뜻이므로 지나간다.
		result := engine.Evaluate("="+name+"()", map[string]any{})
		if result.Error != nil && result.Error.Code == "#NAME?" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("가이드가 설명하지만 엔진이 모르는 함수: %s", strings.Join(missing, ", "))
	}
}
