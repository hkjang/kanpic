package ai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

func TestCompletionEndpointSupportsRootV1AndExactPaths(t *testing.T) {
	t.Parallel()
	for input, expected := range map[string]string{
		"https://llm.internal":                     "https://llm.internal/v1/chat/completions",
		"https://llm.internal/v1":                  "https://llm.internal/v1/chat/completions",
		"https://llm.internal/v1/chat/completions": "https://llm.internal/v1/chat/completions",
	} {
		actual, err := completionEndpoint(input)
		if err != nil || actual != expected {
			t.Fatalf("completionEndpoint(%q)=%q, %v", input, actual, err)
		}
	}
}

func TestGatewayPlanValidationRejectsUnsafeChanges(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:B2")
	cells := []workbook.Cell{{Row: 1, Column: 1, Value: json.RawMessage(`5`), Style: json.RawMessage(`{"bold":true}`)}}
	valid, findings, err := validateGatewayPlan(ModeFormula, selected, cells, gatewayPlan{Summary: "double", Changes: []gatewayChange{{Row: 1, Column: 2, Formula: "=A1*2"}}}, 10)
	if err != nil || len(valid) != 1 || len(findings) != 0 || valid[0].Address != "B1" || valid[0].After.Formula != "=A1*2" {
		t.Fatalf("valid plan=%#v findings=%#v, %v", valid, findings, err)
	}
	_, _, err = validateGatewayPlan(ModeFormula, selected, cells, gatewayPlan{Summary: "escape", Changes: []gatewayChange{{Row: 3, Column: 1, Formula: "=A1"}}}, 10)
	if !errors.Is(err, ErrGateway) {
		t.Fatalf("outside range error=%v", err)
	}
	_, _, err = validateGatewayPlan(ModeFormula, selected, cells, gatewayPlan{Summary: "not formula", Changes: []gatewayChange{{Row: 1, Column: 2, Formula: "curl secret"}}}, 10)
	if !errors.Is(err, ErrGateway) {
		t.Fatalf("non-formula error=%v", err)
	}
	_, _, err = validateGatewayPlan(ModeExplain, selected, cells, gatewayPlan{Summary: "explain", Explanation: "A1", Changes: []gatewayChange{{Row: 1, Column: 2, Formula: "=A1"}}}, 10)
	if !errors.Is(err, ErrGateway) {
		t.Fatalf("explain mutation error=%v", err)
	}
}

func TestGatewayPlanValidationSupportsReadOnlyInsightsAndLiteralCleaning(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:B2")
	cells := []workbook.Cell{{Row: 1, Column: 1, Value: json.RawMessage(`"  Seoul "`), Style: json.RawMessage(`{"bold":true}`)}, {Row: 1, Column: 2, Value: json.RawMessage(`99`)}}

	changes, findings, err := validateGatewayPlan(ModeClean, selected, cells, gatewayPlan{Summary: "normalize", Changes: []gatewayChange{{Row: 1, Column: 1, Value: json.RawMessage(`"Seoul"`)}, {Row: 1, Column: 2, Clear: true}}}, 10)
	if err != nil || len(changes) != 2 || len(findings) != 0 || string(changes[0].After.Value) != `"Seoul"` || string(changes[0].After.Style) != `{"bold":true}` || len(changes[1].After.Value) != 0 {
		t.Fatalf("clean changes=%#v findings=%#v err=%v", changes, findings, err)
	}
	for _, unsafe := range []gatewayChange{
		{Row: 1, Column: 1, Formula: "=TRIM(A1)"},
		{Row: 1, Column: 1, Value: json.RawMessage(`{"city":"Seoul"}`)},
		{Row: 1, Column: 1, Value: json.RawMessage(`null`)},
		{Row: 1, Column: 1, Value: json.RawMessage(`"Seoul"`), Clear: true},
	} {
		if _, _, err := validateGatewayPlan(ModeClean, selected, cells, gatewayPlan{Summary: "unsafe", Changes: []gatewayChange{unsafe}}, 10); !errors.Is(err, ErrGateway) {
			t.Fatalf("unsafe clean change=%#v err=%v", unsafe, err)
		}
	}

	_, summaryFindings, err := validateGatewayPlan(ModeSummarize, selected, cells, gatewayPlan{Summary: "summary", Explanation: "two cells", Findings: []gatewayFinding{{Severity: "info", Title: "Total", Description: "Two populated cells"}}}, 10)
	if err != nil || len(summaryFindings) != 1 || summaryFindings[0].Address != "" {
		t.Fatalf("summary findings=%#v err=%v", summaryFindings, err)
	}
	_, anomalyFindings, err := validateGatewayPlan(ModeAnomaly, selected, cells, gatewayPlan{Summary: "anomaly", Explanation: "B1 is high", Findings: []gatewayFinding{{Row: 1, Column: 2, Severity: "warning", Title: "High value", Description: "Above peers"}}}, 10)
	if err != nil || len(anomalyFindings) != 1 || anomalyFindings[0].Address != "B1" || string(anomalyFindings[0].Cell.Value) != "99" {
		t.Fatalf("anomaly findings=%#v err=%v", anomalyFindings, err)
	}
	if _, _, err := validateGatewayPlan(ModeAnomaly, selected, cells, gatewayPlan{Summary: "escape", Explanation: "outside", Findings: []gatewayFinding{{Row: 3, Column: 1, Severity: "critical", Title: "Outside", Description: "invalid"}}}, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("outside finding error=%v", err)
	}
}

func TestValidateWorkbookAgentFormattingAndChartTools(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:B2")
	cells := []workbook.Cell{{Row: 1, Column: 1, Value: json.RawMessage(`"매출"`)}}
	changes, findings, err := validateGatewayPlan(ModeFormat, selected, cells, gatewayPlan{Summary: "format", Changes: []gatewayChange{{Row: 1, Column: 1, Style: json.RawMessage(`{"bold":true,"background":"#dbeafe"}`)}}}, 10)
	if err != nil || len(changes) != 1 || len(findings) != 0 || string(changes[0].After.Style) == "" {
		t.Fatalf("format plan = %#v, %#v, %v", changes, findings, err)
	}
	chartArgs := json.RawMessage(`{"type":"bar","title":"매출","source_range":"A1:B2"}`)
	chartPlan := gatewayPlan{Summary: "chart", ToolCalls: []gatewayToolCall{{Name: "create_chart", Arguments: chartArgs}}}
	changes, findings, err = validateGatewayPlan(ModeChart, selected, cells, chartPlan, 10)
	if err != nil || len(changes) != 0 || len(findings) != 0 {
		t.Fatalf("chart plan = %#v, %#v, %v", changes, findings, err)
	}
	tools, err := validateGatewayTools(PlanInput{SheetID: "sheet", Mode: ModeChart, IdempotencyKey: "chart"}, selected, chartPlan.ToolCalls, 10)
	if err != nil || len(tools) != 1 || tools[0].Name != "create_chart" || tools[0].Risk != RiskMedium {
		t.Fatalf("chart tools = %#v, %v", tools, err)
	}
	outside := []gatewayToolCall{{Name: "create_chart", Arguments: json.RawMessage(`{"type":"line","source_range":"A1:C3"}`)}}
	if _, err := validateGatewayTools(PlanInput{SheetID: "sheet", Mode: ModeChart, IdempotencyKey: "outside"}, selected, outside, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("outside chart error = %v", err)
	}
	report := []gatewayToolCall{{Name: "create_report_sheet", Arguments: json.RawMessage(`{"name":"경영 보고","cells":[{"row":1,"column":1,"value":"월"},{"row":1,"column":2,"value":"매출"},{"row":2,"column":1,"value":"1월"},{"row":2,"column":2,"formula":"='Data'!B2"}],"chart":{"type":"bar","source_range":"A1:B2"}}`)}}
	contextView := &workbook.AgentContext{Sheets: []workbook.AgentSheet{{Name: "Data"}}}
	reportTools, err := validateGatewayTools(PlanInput{SheetID: "sheet", Mode: ModeAgent, IdempotencyKey: "report", Context: contextView}, selected, report, 10)
	if err != nil || len(reportTools) != 1 || reportTools[0].Name != "create_report_sheet" || reportTools[0].Risk != RiskHigh {
		t.Fatalf("report tools = %#v, %v", reportTools, err)
	}
	unsafeReport := []gatewayToolCall{{Name: "create_report_sheet", Arguments: json.RawMessage(`{"name":"경영 보고","cells":[{"row":1,"column":1,"value":"월"}],"chart":{"type":"bar","source_range":"A1:B2"}}`)}}
	if _, err := validateGatewayTools(PlanInput{SheetID: "sheet", Mode: ModeAgent, IdempotencyKey: "unsafe-report"}, selected, unsafeReport, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("unsafe report error = %v", err)
	}
}

func TestValidateWorkbookAgentCanUpdateOnlyAChartFromCurrentInventory(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:B4")
	chart := workbook.Chart{ID: "chart-1", WorkbookID: "book-1", SheetID: "sheet-1", SourceSheetID: "sheet-1", Type: "bar", SourceRange: "A1:B4", Revision: 3}
	input := PlanInput{WorkbookID: "book-1", SheetID: "sheet-1", Mode: ModeChart, IdempotencyKey: "update", Charts: []workbook.Chart{chart}}
	tools, err := validateGatewayTools(input, selected, []gatewayToolCall{{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"chart-1","type":"line"}`)}}, 10)
	if err != nil || len(tools) != 1 || tools[0].Name != "update_chart" {
		t.Fatalf("update chart tools=%#v err=%v", tools, err)
	}
	var arguments updateChartArguments
	if err := json.Unmarshal(tools[0].Arguments, &arguments); err != nil || arguments.ExpectedRevision != 3 || arguments.Type == nil || *arguments.Type != "line" {
		t.Fatalf("normalized update arguments=%#v err=%v", arguments, err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"chart_id":"invented","type":"line"}`),
		json.RawMessage(`{"chart_id":"chart-1"}`),
		json.RawMessage(`{"chart_id":"chart-1","type":"bar"}`),
		json.RawMessage(`{"chart_id":"chart-1","source_range":"A1:C4"}`),
	} {
		if _, err := validateGatewayTools(input, selected, []gatewayToolCall{{Name: "update_chart", Arguments: raw}}, 10); !errors.Is(err, ErrGateway) {
			t.Fatalf("unsafe update_chart %s error=%v", raw, err)
		}
	}
}

func TestBuildPromptIncludesBoundedConversationAndCurrentChartObjects(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:B2")
	preview := BuildPrompt(Config{GatewayURL: "https://llm.internal", Model: "test", MaxOutputTokens: 512}, PlanInput{
		Mode: ModeChart, Range: "A1:B2", Request: "선 차트로 바꿔줘",
		Conversation: []ConversationMessage{{Role: "user", Content: "막대 차트를 만들어줘"}, {Role: "assistant", Content: "막대 차트 생성 계획입니다."}},
		Memory:       []AgentWorkMemory{{RunID: "run-1", Mode: ModeChart, Status: StatusApplied, Summary: "막대 차트 생성", Selection: "A1:B2", Tools: []AgentMemoryTool{{Name: "create_chart", Status: "completed", Result: json.RawMessage(`{"chart_id":"chart-1","type":"bar","revision":1}`)}}}},
		Charts:       []workbook.Chart{{ID: "chart-1", Type: "bar", SourceRange: "A1:B2", Revision: 1}},
	}, selected, nil, ModelLimits{Model: "test", ContextWindow: 4096})
	for _, expected := range []string{`"conversation_history"`, "막대 차트를 만들어줘", `"conversation_work_memory"`, `"status": "applied"`, `"workbook_objects"`, `"chart-1"`, `"revision": 1`} {
		if !strings.Contains(preview.UserContent, expected) {
			t.Fatalf("prompt does not contain %q: %s", expected, preview.UserContent)
		}
	}
}

func TestConversationWorkMemoryProjectsOutCellValues(t *testing.T) {
	t.Parallel()
	tool := projectMemoryTool(ToolCall{
		Name:      "create_report_sheet",
		Status:    "completed",
		Arguments: json.RawMessage(`{"name":"요약","cells":[{"row":1,"column":1,"value":"민감한 값"}],"chart":{"type":"bar","source_range":"A1:B2"}}`),
		Result:    json.RawMessage(`{"sheet":{"id":"sheet-report","name":"요약"},"cell_operation":{"applied_cells":1},"chart":{"id":"chart-report","type":"bar","source_range":"A1:B2","revision":1}}`),
	})
	if strings.Contains(string(tool.Arguments), "민감한 값") || strings.Contains(string(tool.Arguments), `"cells"`) {
		t.Fatalf("work memory leaked report cells: %s", tool.Arguments)
	}
	for _, expected := range []string{`"cell_count":1`, `"name":"요약"`, `"chart_id":"chart-report"`} {
		combined := string(tool.Arguments) + string(tool.Result)
		if !strings.Contains(combined, expected) {
			t.Fatalf("projected memory does not contain %q: %#v", expected, tool)
		}
	}
	if memoryChangeKind(CellSnapshot{Formula: "=A1*2"}) != "formula" || memoryChangeKind(CellSnapshot{}) != "clear" {
		t.Fatalf("memory change classification is incorrect")
	}
}

func TestStripJSONFence(t *testing.T) {
	t.Parallel()
	if actual := stripJSONFence("```json\n{\"summary\":\"ok\"}\n```"); actual != `{"summary":"ok"}` {
		t.Fatalf("stripJSONFence=%q", actual)
	}
}

func TestDecodeGatewayPlanAcceptsCommonCompatibleResponseShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		tool    bool
	}{
		{name: "reasoning and prose fence", content: "<think>internal reasoning</think>\nHere is the plan:\n```JSON\n{\"summary\":\"ok\",\"explanation\":\"done\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}\n```"},
		{name: "orphan reasoning close", content: "First inspect the draft {\"draft\":true}.\n</THINK>\n{\"summary\":\"ok\",\"explanation\":\"done\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}"},
		{name: "plan envelope", content: `{"plan":{"summary":"ok","explanation":"done","findings":[],"changes":[],"tool_calls":[]}}`},
		{name: "camel and function tool", tool: true, content: `{"summary":"chart","explanation":"done","findings":[],"changes":[],"toolCalls":[{"function":{"name":"create_chart","arguments":"{\"type\":\"bar\",\"source_range\":\"A1:B2\"}"}}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := decodeGatewayPlan(test.content)
			if err != nil || plan.Summary == "" {
				t.Fatalf("plan=%#v err=%v", plan, err)
			}
			if test.tool && (len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Name != "create_chart" || !json.Valid(plan.ToolCalls[0].Arguments)) {
				t.Fatalf("normalized tools=%#v", plan.ToolCalls)
			}
		})
	}
}

func TestAIInputNormalizationAndUnicodeTruncation(t *testing.T) {
	t.Parallel()
	input := normalizePlanInput(PlanInput{WorkbookID: " book ", SheetID: " sheet ", ActorID: " actor ", ClientID: " browser ", IdempotencyKey: " key ", Mode: " formula ", Range: " A1:B1 ", Request: " 두 배 수식 "})
	if input.WorkbookID != "book" || input.SheetID != "sheet" || input.ActorID != "actor" || input.ClientID != "browser" || input.IdempotencyKey != "key" || input.Mode != ModeFormula || input.Range != "A1:B1" || input.Request != "두 배 수식" {
		t.Fatalf("normalized input=%#v", input)
	}
	if actual := trimLength("  가나다  ", 2); actual != "가나" {
		t.Fatalf("unicode truncation=%q", actual)
	}
}
