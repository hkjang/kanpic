package ai

import "testing"

func TestRouteIntentUsesSafeDeterministicModes(t *testing.T) {
	cases := map[string]routedIntent{
		"C열 증가율 계산해줘":        {Mode: ModeFormula, Skill: "formula_generation"},
		"잘못된 수식 찾아서 고쳐줘":     {Mode: ModeFix, Skill: "formula_repair"},
		"이 데이터를 보기 좋게 정리해줘":  {Mode: ModeFormat, Skill: "sheet_formatting"},
		"월별 매출 차트 만들어줘":      {Mode: ModeChart, Skill: "chart_generation"},
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
