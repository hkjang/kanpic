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
	defaultRange := []gatewayToolCall{{Name: "create_chart", Arguments: json.RawMessage(`{"type":"line"}`)}}
	tools, err = validateGatewayTools(PlanInput{SheetID: "sheet", Mode: ModeChart, IdempotencyKey: "default-range"}, selected, defaultRange, 10)
	if err != nil {
		t.Fatalf("default chart range error=%v", err)
	}
	var defaultArguments createChartArguments
	if json.Unmarshal(tools[0].Arguments, &defaultArguments) != nil || defaultArguments.SourceRange != "A1:B2" {
		t.Fatalf("default chart range=%#v", defaultArguments)
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
	// Models often echo the current source range alongside the real type
	// change. The unchanged field must not make an otherwise safe update fail
	// merely because the current selection is narrower than that source.
	narrow, _ := cellrange.Parse("A1:A1")
	tools, err = validateGatewayTools(input, narrow, []gatewayToolCall{{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"chart-1","type":"line","source_range":"A1:B4"}`)}}, 10)
	if err != nil || len(tools) != 1 {
		t.Fatalf("echoed chart source rejected: tools=%#v err=%v", tools, err)
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
	for _, expected := range []string{`"conversation_history"`, "막대 차트를 만들어줘", `"conversation_work_memory"`, `"status": "applied"`, `"required_chart_tool": "update_chart"`, `"workbook_objects"`, `"recommended_chart_target"`, `"chart_id": "chart-1"`, `"reason": "latest_applied_chart"`, `"revision": 1`} {
		if !strings.Contains(preview.UserContent, expected) {
			t.Fatalf("prompt does not contain %q: %s", expected, preview.UserContent)
		}
	}
}

func TestRecommendedChartTargetNeverGuessesAmongCurrentCharts(t *testing.T) {
	t.Parallel()
	charts := []workbook.Chart{{ID: "chart-1", Title: "매출", Revision: 1}, {ID: "chart-2", Title: "이익", Revision: 2}}
	input := PlanInput{Mode: ModeChart, Request: "그 차트를 선으로 바꿔줘", Charts: charts}
	if target := recommendedChartTarget(input); target != nil {
		t.Fatalf("ambiguous target=%#v", target)
	}
	input.Memory = []AgentWorkMemory{{Status: StatusApplied, Tools: []AgentMemoryTool{{Name: "update_chart", Status: StatusCompleted, Result: json.RawMessage(`{"after":{"chart_id":"chart-2"}}`)}}}}
	if target := recommendedChartTarget(input); target == nil || target.ChartID != "chart-2" || target.Revision != 2 {
		t.Fatalf("memory target=%#v", target)
	}
	input.Request = "이익 차트를 선으로 바꿔줘"
	input.Memory[0].Tools[0].Result = json.RawMessage(`{"after":{"chart_id":"chart-1"}}`)
	if target := recommendedChartTarget(input); target == nil || target.ChartID != "chart-2" || target.Reason != "explicit_current_chart" {
		t.Fatalf("explicit target=%#v", target)
	}
	input.Request = "그 차트를 선으로 바꿔줘"
	input.Memory[0].Tools = append(input.Memory[0].Tools, AgentMemoryTool{Name: "update_chart", Status: StatusCompleted, Result: json.RawMessage(`{"after":{"chart_id":"chart-2"}}`)})
	if target := recommendedChartTarget(input); target != nil {
		t.Fatalf("multi-chart memory target=%#v", target)
	}
	input.Memory[0].Tools = input.Memory[0].Tools[:1]
	input.Memory[0].Status = StatusUndone
	if target := recommendedChartTarget(input); target != nil {
		t.Fatalf("undone target=%#v", target)
	}
}

func TestValidateChartIntentAndResolvedTarget(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:B4")
	charts := []workbook.Chart{
		{ID: "chart-1", WorkbookID: "book", SourceSheetID: "sheet", Title: "매출", Type: "bar", SourceRange: "A1:B4", Revision: 1},
		{ID: "chart-2", WorkbookID: "book", SourceSheetID: "sheet", Title: "이익", Type: "bar", SourceRange: "A1:B4", Revision: 2},
	}
	updateInput := PlanInput{WorkbookID: "book", SheetID: "sheet", Mode: ModeChart, Skill: "chart_update", Request: "이익 차트를 선으로 바꿔줘", Charts: charts}
	if _, err := validateGatewayTools(updateInput, selected, []gatewayToolCall{{Name: "create_chart", Arguments: json.RawMessage(`{"type":"line","source_range":"A1:B4"}`)}}, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("update intent accepted create: %v", err)
	}
	wrongTarget := []gatewayToolCall{{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"chart-1","type":"line"}`)}}
	if _, err := validateGatewayTools(updateInput, selected, wrongTarget, 10); !errors.Is(err, ErrGateway) || !strings.Contains(err.Error(), "chart-2") {
		t.Fatalf("update intent accepted wrong target: %v", err)
	}
	correctTarget := []gatewayToolCall{{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"chart-2","type":"line"}`)}}
	if _, err := validateGatewayTools(updateInput, selected, correctTarget, 10); err != nil {
		t.Fatalf("update intent rejected target: %v", err)
	}
	createInput := updateInput
	createInput.Skill, createInput.Request = "chart_generation", "새 차트를 만들어줘"
	if _, err := validateGatewayTools(createInput, selected, correctTarget, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("create intent accepted update: %v", err)
	}
	agentInput := updateInput
	agentInput.Mode, agentInput.Skill, agentInput.Request = ModeAgent, "workbook_orchestration", "보고서를 만들고 이익 차트를 선으로 바꿔줘"
	if _, err := validateGatewayTools(agentInput, selected, wrongTarget, 10); !errors.Is(err, ErrGateway) || !strings.Contains(err.Error(), "chart-2") {
		t.Fatalf("agent update accepted wrong target: %v", err)
	}
	if _, err := validateGatewayTools(agentInput, selected, correctTarget, 10); err != nil {
		t.Fatalf("agent update rejected target: %v", err)
	}
}

func TestRequestedChartTypeUsesTheConversionDestination(t *testing.T) {
	t.Parallel()
	if actual := requestedChartType("막대 차트를 선 차트로 바꿔줘"); actual != "line" {
		t.Fatalf("requested type=%q", actual)
	}
	if !chartTypeOnlyUpdate("선으로 바꿔줘") || chartTypeOnlyUpdate("선 차트로 바꾸고 제목도 수정해줘") {
		t.Fatal("chart type-only classification is incorrect")
	}
}

func TestValidateChartPlansMatchRepositoryConstraints(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:A10001")
	input := PlanInput{WorkbookID: "book", SheetID: "sheet", Mode: ModeAgent, IdempotencyKey: "constraints", Charts: []workbook.Chart{}}
	for name, arguments := range map[string]string{
		"source cells": `{"type":"bar","source_range":"A1:A10001"}`,
		"legend":       `{"type":"bar","source_range":"A1:A2","legend_position":"middle"}`,
		"title":        `{"type":"bar","source_range":"A1:A2","title":"` + strings.Repeat("가", 201) + `"}`,
	} {
		if _, err := validateGatewayTools(input, selected, []gatewayToolCall{{Name: "create_chart", Arguments: json.RawMessage(arguments)}}, 10); !errors.Is(err, ErrGateway) {
			t.Fatalf("%s constraint error=%v", name, err)
		}
	}
	full := input
	full.Charts = make([]workbook.Chart, workbook.MaxChartsPerWorkbook)
	if _, err := validateGatewayTools(full, selected, []gatewayToolCall{{Name: "create_chart", Arguments: json.RawMessage(`{"type":"bar","source_range":"A1:A2"}`)}}, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("chart count constraint error=%v", err)
	}
	broken := input
	broken.Charts = []workbook.Chart{{ID: "broken", WorkbookID: "book", Type: "bar", SourceRange: "#REF!", Revision: 1}}
	if _, err := validateGatewayTools(broken, selected, []gatewayToolCall{{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"broken","type":"line"}`)}}, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("broken chart update error=%v", err)
	}
	current := workbook.Chart{ID: "chart-1", WorkbookID: "book", SourceSheetID: "sheet", Type: "bar", SourceRange: "A1:A2", Revision: 1}
	duplicate := input
	duplicate.Request = "차트를 다른 형식으로 바꿔줘"
	duplicate.Charts = []workbook.Chart{current}
	calls := []gatewayToolCall{
		{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"chart-1","type":"line"}`)},
		{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"chart-1","type":"area"}`)},
	}
	if _, err := validateGatewayTools(duplicate, selected, calls, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("duplicate update error=%v", err)
	}
	combined := []gatewayToolCall{
		{Name: "update_chart", Arguments: json.RawMessage(`{"chart_id":"chart-1","type":"line"}`)},
		{Name: "create_chart", Arguments: json.RawMessage(`{"type":"bar","source_range":"A1:A2"}`)},
	}
	if _, err := validateGatewayTools(duplicate, selected, combined, 10); !errors.Is(err, ErrGateway) {
		t.Fatalf("multi-tool chart mutation error=%v", err)
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
		{name: "complete draft before orphan close", content: "{\"summary\":\"draft\",\"explanation\":\"discard me\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}\n</think>\n{\"summary\":\"ok\",\"explanation\":\"done\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}"},
		{name: "draft object before final plan", content: "Draft: {\"type\":\"bar\",\"source_range\":\"A1:B2\"}\nFinal: {\"summary\":\"ok\",\"explanation\":\"done\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}"},
		{name: "unclosed reasoning with draft object", content: "<think>Draft: {\"type\":\"bar\"}\n{\"summary\":\"ok\",\"explanation\":\"done\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}"},
		{name: "literal think close in JSON string", content: `{"summary":"ok","explanation":"문자열 </think> 보존","findings":[],"changes":[],"tool_calls":[]}`},
		{name: "literal think pair in JSON string", content: `{"summary":"ok","explanation":"문자열 <think>태그</think> 보존","findings":[],"changes":[],"tool_calls":[]}`},
		{name: "literal think pair in fenced JSON", content: "```json\n{\"summary\":\"ok\",\"explanation\":\"문자열 <think>태그</think> 보존\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}\n```"},
		{name: "empty tool metadata before plan", content: `metadata {"tool_calls":[]} final {"summary":"ok","explanation":"done","findings":[],"changes":[],"tool_calls":[]}`},
		{name: "named metadata before plan", content: `metadata {"name":"매출"} final {"summary":"ok","explanation":"done","findings":[],"changes":[],"tool_calls":[]}`},
		{name: "plan envelope", content: `{"plan":{"summary":"ok","explanation":"done","findings":[],"changes":[],"tool_calls":[]}}`},
		{name: "recursive string envelope", content: `{"response":"{\"data\":{\"plan\":{\"summary\":\"ok\",\"explanation\":\"done\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}}}"}`},
		{name: "singleton array", content: `[{"summary":"ok","explanation":"done","findings":[],"changes":[],"tool_calls":[]}]`},
		{name: "camel and function tool", tool: true, content: `{"summary":"chart","explanation":"done","findings":[],"changes":[],"toolCalls":[{"function":{"name":"create_chart","arguments":"{\"type\":\"bar\",\"source_range\":\"A1:B2\"}"}}]}`},
		{name: "direct fenced tool", tool: true, content: "{\"name\":\"create_chart\",\"input\":\"계획:\\n```json\\n{\\\"chartType\\\":\\\"bar\\\",\\\"sourceRange\\\":\\\"A1:B2\\\"}\\n```\"}"},
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

func TestDecodeGatewayPlanRejectsTwoCompletePlans(t *testing.T) {
	t.Parallel()
	content := `{"summary":"first","explanation":"one","findings":[],"changes":[],"tool_calls":[]} {"summary":"second","explanation":"two","findings":[],"changes":[],"tool_calls":[]}`
	if plan, err := decodeGatewayPlan(content); err == nil {
		t.Fatalf("ambiguous plan was accepted: %#v", plan)
	}
}

func TestDecodeGatewayPlanPrefersFinalPlanAfterOrphanThinkClose(t *testing.T) {
	t.Parallel()
	content := `{"summary":"draft","explanation":"discard","findings":[],"changes":[],"tool_calls":[]}</think>{"summary":"final","explanation":"keep","findings":[],"changes":[],"tool_calls":[]}`
	plan, err := decodeGatewayPlan(content)
	if err != nil || plan.Summary != "final" {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
}

func TestDecodeGatewayPlanPreservesReasoningTagsInsideWrappedJSON(t *testing.T) {
	t.Parallel()
	content := "Result:\n```json\n{\"summary\":\"ok\",\"explanation\":\"keep <think>literal</think> and </think>\",\"findings\":[],\"changes\":[],\"tool_calls\":[]}\n```"
	plan, err := decodeGatewayPlan(content)
	if err != nil || plan.Explanation != "keep <think>literal</think> and </think>" {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
}

func TestNormalizeToolArgumentsDoesNotChooseAmongMultipleObjects(t *testing.T) {
	t.Parallel()
	var call gatewayToolCall
	err := json.Unmarshal([]byte(`{"name":"update_chart","input":"draft {\"chart_id\":\"chart-1\",\"type\":\"line\"} final {\"chart_id\":\"chart-2\",\"type\":\"line\"}"}`), &call)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(call.Arguments) {
		t.Fatalf("ambiguous arguments were normalized: %s", call.Arguments)
	}
	err = json.Unmarshal([]byte(`{"name":"update_chart","input":"draft {\"chart_id\":\"chart-1\",\"type\":\"line\"} final {\"chart_id\":\"chart-2\""}`), &call)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(call.Arguments) {
		t.Fatalf("truncated final arguments selected the draft: %s", call.Arguments)
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

// 도구 하나를 더할 때 손봐야 하는 자리가 여럿인데, 빠뜨려도 조용한 자리가
// 있다. 계획 목록은 예전에 모르는 도구를 모두 "차트 생성" 이라고 적었고,
// 기억 투영은 빠뜨리면 아무 말 없이 비어 있는다. 목록을 훑어서 잡는다.
func TestEverySupportedToolIsWiredEverywhereItMustBe(t *testing.T) {
	t.Parallel()
	// 실행이 만드는 결과 모양은 도구마다 다르므로, 여기서는 이름만 있으면
	// 되는 자리 — 설명, scope, 기억 — 를 본다.
	samples := map[string]json.RawMessage{
		"create_chart":              json.RawMessage(`{"type":"bar","source_range":"A1:B2"}`),
		"update_chart":              json.RawMessage(`{"chart_id":"chart-1","type":"line"}`),
		"create_report_sheet":       json.RawMessage(`{"name":"요약","cells":[{"row":1,"column":1,"value":"비밀"}]}`),
		"create_conditional_format": json.RawMessage(`{"range":"A1:B2","rule_type":"rank"}`),
		"create_pivot":              json.RawMessage(`{"source_range":"A1:B2","values":[{"column":2,"aggregation":"sum"}]}`),
		"create_data_validation":    json.RawMessage(`{"range":"A1:A2","rule_type":"list","options":[{"value":"비밀"}]}`),
		"create_filter_view":        json.RawMessage(`{"range":"A1:B2","criteria":[{"column":2,"operator":"is_blank"}]}`),
		"sort_range":                json.RawMessage(`{"range":"A1:B2","keys":[{"column":2,"direction":"asc"}]}`),
		"create_table":              json.RawMessage(`{"name":"매출표","range":"A1:B2"}`),
	}
	descriptions := map[string]bool{}
	for _, name := range workbookTools {
		arguments, sampled := samples[name]
		if !sampled {
			t.Fatalf("%s 의 예시 인수가 없다 — 도구를 더했으면 이 검사도 늘려야 한다", name)
		}
		// 계획 목록: 도구마다 다른 말을 하고, 아무 말도 지어내지 않는다.
		description := planStepDescription(name)
		if description == "" || descriptions[description] {
			t.Errorf("%s 의 계획 설명이 없거나 다른 도구와 같다: %q", name, description)
		}
		descriptions[description] = true
		if !strings.Contains(name, "chart") && strings.Contains(description, "차트") && name != "create_report_sheet" {
			t.Errorf("%s 가 차트를 만든다고 적혀 있다: %q", name, description)
		}
		// scope: 계획을 승인할 때 REST·MCP 와 같은 권한을 요구해야 한다.
		if len(RequiredApprovalScopes(Action{Mode: ModeAgent, ToolCalls: []ToolCall{{Name: name, Arguments: arguments}}})) == 0 {
			t.Errorf("%s 가 아무 scope 도 요구하지 않는다", name)
		}
		// 기억: 후속 요청이 "방금 만든 것" 을 가리키려면 무언가는 남아야 한다.
		projected := projectMemoryTool(ToolCall{Name: name, Status: "completed", Arguments: arguments})
		if len(projected.Arguments) == 0 {
			t.Errorf("%s 가 작업 기억에 아무것도 남기지 않는다", name)
		}
		// 그러면서 셀 값은 남기지 않는다.
		if strings.Contains(string(projected.Arguments), "비밀") {
			t.Errorf("%s 의 작업 기억에 셀 값이 새어 나갔다: %s", name, projected.Arguments)
		}
	}
	// 목록에 없는 것은 그대로 막힌다.
	for _, name := range []string{"delete_sheet", "sort_range ", ""} {
		if name != "sort_range " && supportedGatewayTool(name) {
			t.Errorf("허용 목록에 없는 %q 가 통과했다", name)
		}
	}
	// 공백은 다듬어서 본다.
	if !supportedGatewayTool(" sort_range ") {
		t.Error("공백이 붙은 이름이 막혔다")
	}
}
