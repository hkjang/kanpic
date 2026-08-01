package ai

import (
	"encoding/json"
	"errors"
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

func TestStripJSONFence(t *testing.T) {
	t.Parallel()
	if actual := stripJSONFence("```json\n{\"summary\":\"ok\"}\n```"); actual != `{"summary":"ok"}` {
		t.Fatalf("stripJSONFence=%q", actual)
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
