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

// 도구를 다섯 개 붙여 놓고도 사람이 그 일을 말로 시키면 읽기 전용 모드로
// 갔다. "매출 100 이상만 보이게 해줘" 는 summarize 로, "매출 많은 순으로
// 정렬해줘" 는 서식으로 샜다. 읽기 전용 모드는 도구를 아예 쓸 수 없으므로,
// 만들어 놓은 도구에 닿는 길이 없었던 것이다.
func TestNaturalRequestsReachTheToolThatDoesTheJob(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		message string
		skill   string
	}{
		{"부서별 매출 합계를 요약해줘", "pivot_analysis"},
		{"월별 평균을 내줘", "pivot_analysis"},
		{"제품별 판매 건수를 집계해줘", "pivot_analysis"},
		{"성별 인원수를 세줘", "pivot_analysis"},
		{"매출 100 이상만 보이게 해줘", "filter_view"},
		{"완료된 항목은 빼고 보여줘", "filter_view"},
		{"필터를 걸어줘", "filter_view"},
		{"매출 많은 순으로 정렬해줘", "range_sort"},
		{"이름 기준으로 오름차순 정렬", "range_sort"},
		{"최신순으로 줄 세워줘", "range_sort"},
		{"상태 열은 진행/완료만 고르게 해줘", "data_validation"},
		{"여기에 체크박스를 넣어줘", "data_validation"},
		{"음수는 못 넣게 해줘", "data_validation"},
		{"매출 상위 10%에 색 칠해줘", "conditional_format"},
		{"목표 미달이면 빨갛게 표시해줘", "conditional_format"},
		{"조건부 서식으로 중복을 강조해줘", "conditional_format"},
	} {
		routed := routeIntent(item.message)
		if routed.Mode != ModeAgent {
			t.Errorf("%q 가 %s 로 갔다 — 도구를 쓸 수 없는 모드다", item.message, routed.Mode)
			continue
		}
		if routed.Skill != item.skill {
			t.Errorf("%q skill=%q, 기대=%q", item.message, routed.Skill, item.skill)
		}
	}
}

// 넓히면서 멀쩡하던 길을 끊으면 안 된다. 특히 "정렬" 은 한국어에서 줄
// 세우기와 맞춤 두 가지 뜻이라 서식 요청이 에이전트로 새기 쉽다.
func TestWideningTheRouterKeepsTheOldRoutes(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		message string
		mode    string
	}{
		{"제목을 가운데 정렬해줘", ModeFormat},
		{"머리글을 왼쪽 정렬로 바꿔줘", ModeFormat},
		{"이 범위를 보기 좋게 정리해줘", ModeFormat},
		{"이 범위를 분석해줘", ModeSummarize},
		{"특별 예산을 요약해줘", ModeSummarize},
		{"B열에 합계 수식을 넣어줘", ModeFormula},
		{"선택 범위로 막대 차트를 만들어줘", ModeChart},
		{"막대 차트를 선 차트로 바꿔줘", ModeChart},
		{"중복된 행을 정리해줘", ModeClean},
		{"이상치를 찾아줘", ModeAnomaly},
		{"수식이 왜 이런 값을 내는지 설명해줘", ModeExplain},
		{"지난번 작업 취소해줘", ModeAgent},
	} {
		if routed := routeIntent(item.message); routed.Mode != item.mode {
			t.Errorf("%q -> %s, 기대=%s", item.message, routed.Mode, item.mode)
		}
	}
}
