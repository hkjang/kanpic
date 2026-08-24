package ai

import (
	"encoding/json"
	"slices"
	"testing"

	"kanpic/internal/workbook"
)

func TestRouteIntentUsesSafeDeterministicModes(t *testing.T) {
	cases := map[string]routedIntent{
		"C열 증가율 계산해줘":        {Mode: ModeFormula, Skill: "formula_generation"},
		"잘못된 수식 찾아서 고쳐줘":     {Mode: ModeFix, Skill: "formula_repair"},
		"이 데이터를 보기 좋게 정리해줘":  {Mode: ModeFormat, Skill: "sheet_formatting"},
		"월별 매출 차트 만들어줘":      {Mode: ModeChart, Skill: "chart_generation"},
		"막대 차트를 선 차트로 바꿔줘":   {Mode: ModeChart, Skill: "chart_update"},
		"지역별 매출 피벗 만들어줘":     {Mode: ModeAgent, Skill: "workbook_orchestration"},
		"이 시트 분석해줘":          {Mode: ModeSummarize, Skill: "data_analysis"},
		"/clean 날짜 형식을 통일해줘": {Mode: ModeClean, Skill: "data_cleanup"},
		"지난번 작업 취소해줘":        {Mode: ModeAgent, Skill: "rollback"},
	}
	for prompt, expected := range cases {
		if actual := routeIntent(prompt); actual != expected {
			t.Errorf("routeIntent(%q) = %#v, want %#v", prompt, actual, expected)
		}
	}
}

func TestRiskForModeRequiresApprovalForWrites(t *testing.T) {
	if got := riskForMode(ModeSummarize, 0); got != RiskRead {
		t.Fatalf("summarize risk = %s", got)
	}
	if got := riskForMode(ModeFormula, 20); got != RiskMedium {
		t.Fatalf("formula risk = %s", got)
	}
	if got := riskForMode(ModeClean, 20); got != RiskHigh {
		t.Fatalf("clean risk = %s", got)
	}
	if got := riskForMode(ModeFormula, 1000); got != RiskCritical {
		t.Fatalf("bulk risk = %s", got)
	}
}

func TestRouteFollowUpIntentUsesConversationForEllipticalChartRequests(t *testing.T) {
	history := []ConversationMessage{{Role: "user", Content: "선택 범위로 막대 차트를 만들어줘"}, {Role: "assistant", Content: "막대 차트 생성 계획입니다."}}
	charts := []workbook.Chart{{ID: "chart-1", Type: "bar"}}
	routed := routeFollowUpIntent("선으로 바꿔줘", routeIntent("선으로 바꿔줘"), history, charts)
	if routed != (routedIntent{Mode: ModeChart, Skill: "chart_update"}) {
		t.Fatalf("elliptical follow-up route=%#v", routed)
	}
	if actual := routeFollowUpIntent("선으로 바꿔줘", routeIntent("선으로 바꿔줘"), history, nil); actual.Mode != ModeSummarize {
		t.Fatalf("follow-up without a current chart widened permissions: %#v", actual)
	}
}

func TestRequiredApprovalScopesIncludesEveryPlannedToolCapability(t *testing.T) {
	scopes := RequiredApprovalScopes(Action{Mode: ModeAgent, ToolCalls: []ToolCall{{Name: "update_chart"}}})
	if len(scopes) != 2 || scopes[0] != "range.write" || scopes[1] != "chart.write" {
		t.Fatalf("agent chart scopes=%#v", scopes)
	}
}

// 도구가 만드는 것은 REST·MCP 에서 각자의 scope 로 지키는 물건들이다.
// 에이전트만 range.write 로 다 만들 수 있으면, 에이전트가 권한을 넘는
// 지름길이 된다. 여기 적힌 짝은 httpapi 의 경로별 scope 와 같아야 한다.
func TestAgentToolsAskForTheSameScopeAsTheirDirectRoute(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tool   ToolCall
		wanted string
	}{
		{ToolCall{Name: "create_conditional_format"}, "format.write"},
		{ToolCall{Name: "create_pivot"}, "pivot.write"},
		{ToolCall{Name: "create_data_validation"}, "range.write"},
		{ToolCall{Name: "create_chart"}, "chart.write"},
		// 보고서 안의 차트도 차트다. 차트를 품고 있으면 차트 권한이 든다.
		{ToolCall{Name: "create_report_sheet", Arguments: json.RawMessage(`{"name":"보고","chart":{"type":"bar","source_range":"A1:B2"}}`)}, "chart.write"},
	}
	for _, item := range cases {
		scopes := RequiredApprovalScopes(Action{Mode: ModeAgent, ToolCalls: []ToolCall{item.tool}})
		if !slices.Contains(scopes, item.wanted) {
			t.Errorf("%s 는 %s 를 요구하지 않는다: %#v", item.tool.Name, item.wanted, scopes)
		}
	}
	// 차트 없는 보고서까지 차트 권한을 요구하면 필요 없는 권한을 강요한다.
	plain := RequiredApprovalScopes(Action{Mode: ModeAgent, ToolCalls: []ToolCall{{Name: "create_report_sheet", Arguments: json.RawMessage(`{"name":"보고"}`)}}})
	if slices.Contains(plain, "chart.write") {
		t.Errorf("차트 없는 보고서가 차트 권한을 요구한다: %#v", plain)
	}
	// 요구한 scope 가 정렬 목록에 없다고 조용히 사라지면 검사에서 빠진다.
	mixed := RequiredApprovalScopes(Action{Mode: ModeAgent, ToolCalls: []ToolCall{{Name: "create_pivot"}, {Name: "create_conditional_format"}, {Name: "create_chart"}}})
	if len(mixed) != 4 {
		t.Fatalf("scopes=%#v", mixed)
	}
}
