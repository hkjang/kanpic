package ai

import (
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
