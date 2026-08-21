package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

func TestPlanTokenBudgetSpendsTheContextWindow(t *testing.T) {
	t.Parallel()
	config := Config{MaxChanges: 100}
	// A published window is spent on the reply instead of a fixed guess.
	if budget := planTokenBudget(config, 4_000, 32_768); budget != 32_768-4_000-promptSafetyGap {
		t.Fatalf("budget with a window = %d", budget)
	}
	// A prompt that already consumes the published window does not invent
	// output room; requestGatewayPlan turns this into an actionable preflight.
	if budget := planTokenBudget(config, 7_800, 8_192); budget != 1 {
		t.Fatalf("tight window budget = %d", budget)
	}
	// A very large window is capped so one plan cannot run away.
	if budget := planTokenBudget(config, 1_000, 1_000_000); budget != maxOutputTokens {
		t.Fatalf("huge window budget = %d", budget)
	}
	// Without a published window the first request fits a conservative 8K
	// context assumption; truncation can still grow it on larger models.
	if budget := planTokenBudget(config, 4_000, 0); budget != 8_192-4_000-promptSafetyGap {
		t.Fatalf("unknown window budget = %d", budget)
	}
	// An administrator cap always wins.
	if budget := planTokenBudget(Config{MaxChanges: 100, MaxOutputTokens: 2_048}, 1_000, 128_000); budget != 2_048 {
		t.Fatalf("configured budget = %d", budget)
	}
	if budget := planTokenBudget(Config{MaxChanges: 100, MaxOutputTokens: 512}, 1_000, 128_000); budget != 512 {
		t.Fatalf("small configured budget = %d", budget)
	}
}

func TestFetchModelLimitsReadsTheContextLength(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/models") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"corp-llm-8b","max_model_len":32768}]}`))
	}))
	defer server.Close()

	limits, err := fetchModelLimits(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "corp-llm-8b"})
	if err != nil || limits.ContextWindow != 32768 {
		t.Fatalf("limits=%#v, %v", limits, err)
	}
	// With no model configured the gateway's first model is adopted.
	limits, err = fetchModelLimits(context.Background(), server.Client(), Config{GatewayURL: server.URL})
	if err != nil || limits.Model != "corp-llm-8b" {
		t.Fatalf("discovered model=%#v, %v", limits, err)
	}
}

// A gateway that does not publish a context length gets a conservative first
// budget, so a truncated reply is retried with a bigger one instead of being
// reported as invalid JSON.
func TestRequestGatewayPlanRetriesATruncatedReply(t *testing.T) {
	t.Parallel()
	budgets := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		budgets = append(budgets, body.MaxTokens)
		if len(budgets) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"잘린"},"finish_reason":"length"}],"usage":{"prompt_tokens":1200,"completion_tokens":1024}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"완성\",\"explanation\":\"선택 범위를 요약했습니다.\",\"changes\":[],\"findings\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":80}}`))
	}))
	defer server.Close()

	selected, _ := cellrange.Parse("A1:B2")
	config := Config{GatewayURL: server.URL, Model: "corp-llm-8b", MaxChanges: 100}
	plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), config, PlanInput{Mode: ModeSummarize, Range: "A1:B2", Request: "요약"}, selected, []workbook.Cell{{Row: 1, Column: 1, Value: json.RawMessage(`5`)}}, ModelLimits{Model: "corp-llm-8b"})
	if err != nil {
		t.Fatalf("plan error=%v", err)
	}
	if plan.Summary != "완성" {
		t.Fatalf("plan=%#v", plan)
	}
	if len(budgets) != 2 || budgets[1] <= budgets[0] {
		t.Fatalf("budgets=%v, the retry should ask for more room", budgets)
	}
	if usage.Attempts != 2 || usage.PromptTokens != 2400 || usage.CompletionTokens != 1104 {
		t.Fatalf("usage=%#v", usage)
	}
}

func TestRequestGatewayPlanAcceptsACompletePlanMarkedAsLength(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"완성\",\"explanation\":\"완전한 계획입니다.\",\"changes\":[],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"length"}],"usage":{"prompt_tokens":20,"completion_tokens":30}}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil || calls != 1 || usage.Attempts != 1 || plan.Summary != "완성" {
		t.Fatalf("calls=%d plan=%#v usage=%#v err=%v", calls, plan, usage, err)
	}
}

func TestRequestGatewayPlanDoesNotExecuteDraftBeforeTruncatedFinalPlan(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			content := `{"summary":"초안","explanation":"실행 금지","changes":[],"findings":[],"tool_calls":[]} {"summary":"최종`
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "length"}}})
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"완성\",\"explanation\":\"완료\",\"changes\":[],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil || calls != 2 || usage.Attempts != 2 || plan.Summary != "완성" {
		t.Fatalf("calls=%d plan=%#v usage=%#v err=%v", calls, plan, usage, err)
	}
}

func TestRequestGatewayPlanAcceptsNativeChartToolCalls(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": nil,
					"tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{
						"name": "create_chart", "arguments": `{"chartType":"bar","sourceRange":"A1:B2"}`,
					}}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"input_tokens": 40, "output_tokens": 20},
		})
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:B2")
	plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{SheetID: "sheet-1", Mode: ModeChart, Range: "A1:B2", Request: "막대 차트"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil || len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Name != "create_chart" || usage.PromptTokens != 40 || usage.CompletionTokens != 20 {
		t.Fatalf("plan=%#v usage=%#v err=%v", plan, usage, err)
	}
	var arguments createChartArguments
	if err := json.Unmarshal(plan.ToolCalls[0].Arguments, &arguments); err != nil || arguments.Type != "bar" || arguments.SourceRange != "A1:B2" {
		t.Fatalf("arguments=%#v err=%v", arguments, err)
	}
}

func TestRequestGatewayPlanIgnoresReasoningDraftWhenNativeToolCallIsFinal(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content":   nil,
					"reasoning": `draft {"name":"update_chart","arguments":{"chart_id":"chart-old","type":"line"}}`,
					"tool_calls": []any{map[string]any{"function": map[string]any{
						"name": "create_chart", "arguments": `{"type":"bar","source_range":"A1:B2"}`,
					}}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:B2")
	plan, _, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{SheetID: "sheet-1", Mode: ModeChart, Range: "A1:B2", Request: "막대 차트"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil || len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Name != "create_chart" {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
}

func TestCallGatewayDoesNotMaskAmbiguousContentWithNativeToolCalls(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{
				"content":    `{"summary":"first","explanation":"one","findings":[],"changes":[],"tool_calls":[]} {"summary":"second","explanation":"two","findings":[],"changes":[],"tool_calls":[]}`,
				"tool_calls": []any{map[string]any{"function": map[string]any{"name": "create_chart", "arguments": `{"type":"bar","source_range":"A1:B2"}`}}},
			}, "finish_reason": "tool_calls"}},
		})
	}))
	defer server.Close()
	_, _, err := callGateway(context.Background(), server.Client(), Config{Model: "m"}, server.URL, PromptPreview{Model: "m"}, 1024, "", true)
	if err == nil || !isInvalidPlanResponse(err) {
		t.Fatalf("ambiguous native response error=%v", err)
	}
}

func TestRequestGatewayPlanShrinksContextOverflowWithoutDisablingJSONMode(t *testing.T) {
	t.Parallel()
	budgets := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&request)
		var budget int
		_ = json.Unmarshal(request["max_tokens"], &budget)
		budgets = append(budgets, budget)
		if len(budgets) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded","message":"maximum context length exceeded"}}`))
			return
		}
		if len(request["response_format"]) == 0 {
			t.Error("context overflow incorrectly disabled JSON mode")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"요약\",\"explanation\":\"완료\",\"changes\":[],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	if _, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 100}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"}); err != nil || usage.Attempts != 2 || len(budgets) != 2 || budgets[1] >= budgets[0] {
		t.Fatalf("budgets=%v usage=%#v err=%v", budgets, usage, err)
	}
}

func TestRequestGatewayPlanRejectsKnownOversizedPromptBeforeCalling(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	_, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m", ContextWindow: 128})
	if !errors.Is(err, ErrInvalid) || calls != 0 || usage.Attempts != 0 {
		t.Fatalf("calls=%d usage=%#v err=%v", calls, usage, err)
	}
}

func TestRequestGatewayPlanDoesNotRetryAnOversizedInput(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"prompt is too long for this model"}}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	_, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"})
	if !errors.Is(err, ErrInvalid) || calls != 1 || usage.Attempts != 1 {
		t.Fatalf("calls=%d usage=%#v err=%v", calls, usage, err)
	}
}

func TestRequestGatewayPlanCanShrinkBelowTheOrdinaryOutputFloor(t *testing.T) {
	t.Parallel()
	budgets := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&request)
		var budget int
		_ = json.Unmarshal(request["max_tokens"], &budget)
		budgets = append(budgets, budget)
		if len(budgets) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"요약\",\"explanation\":\"완료\",\"changes\":[],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	_, _, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10, MaxOutputTokens: 1024}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil || len(budgets) != 2 || budgets[0] != 1024 || budgets[1] != 512 {
		t.Fatalf("budgets=%v err=%v", budgets, err)
	}
}

func TestRequestGatewayPlanKeepsSeparateCompatibilityAndRepairBudgets(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&request)
		switch calls {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.WriteHeader(http.StatusBadRequest)
		case 3:
			if len(request["response_format"]) != 0 {
				t.Error("response_format fallback was not retained")
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not json"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`))
		default:
			if len(request["response_format"]) != 0 {
				t.Error("repaired call restored response_format")
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"요약\",\"explanation\":\"완료\",\"changes\":[],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`))
		}
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil || calls != 4 || usage.Attempts != 4 || usage.PromptTokens != 21 || usage.CompletionTokens != 10 || plan.Summary != "요약" {
		t.Fatalf("calls=%d plan=%#v usage=%#v err=%v", calls, plan, usage, err)
	}
}

func TestRequestGatewayPlanTreatsCompleteLengthPlanAsSemanticFailure(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"수정\",\"explanation\":\"완료\",\"changes\":[],\"findings\":[],\"tool_calls\":[{\"name\":\"update_chart\",\"arguments\":{\"chart_id\":\"missing\",\"type\":\"line\"}}]}"},"finish_reason":"length"}]}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:B2")
	input := PlanInput{WorkbookID: "book", SheetID: "sheet", Mode: ModeChart, Range: "A1:B2", Request: "차트를 선으로 바꿔줘", Charts: []workbook.Chart{{ID: "chart-1", WorkbookID: "book", SourceSheetID: "sheet", Type: "bar", SourceRange: "A1:B2", Revision: 1}}}
	_, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, input, selected, nil, ModelLimits{Model: "m"})
	if err == nil || strings.Contains(err.Error(), "잘렸습니다") || !strings.Contains(err.Error(), "chart_id") || calls != maxGatewayAttempts || usage.Attempts != calls {
		t.Fatalf("calls=%d usage=%#v err=%v", calls, usage, err)
	}
}

// Once the actual prompt usage fills the published window there is nothing
// left to grow into, so the caller stops after at most one recalibration.
func TestRequestGatewayPlanReportsAnUnavoidableTruncation(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"잘린"},"finish_reason":"length"}],"usage":{"prompt_tokens":1200,"completion_tokens":31000}}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:A1")
	_, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 100}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m", ContextWindow: 32768})
	if err == nil || !strings.Contains(err.Error(), "잘렸습니다") {
		t.Fatalf("truncation error=%v", err)
	}
	if calls < 1 || calls > 2 || usage.Attempts != calls || usage.CompletionTokens != 31000*calls {
		t.Fatalf("calls=%d usage=%#v", calls, usage)
	}
}

func TestRequestGatewayPlanReportsThreeGrowingTruncations(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"잘린"},"finish_reason":"length"}],"usage":{"prompt_tokens":100,"completion_tokens":1000}}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	_, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "3회 연속 잘렸습니다") {
		t.Fatalf("truncation error=%v", err)
	}
	if calls != maxGatewayAttempts || usage.Attempts != maxGatewayAttempts {
		t.Fatalf("calls=%d usage=%#v", calls, usage)
	}
}

// A gateway that is briefly unavailable is retried. A JSON-mode rejection gets
// one compatibility fallback, then an ordinary 400 response stops immediately.
func TestRequestGatewayPlanRetriesOnlyTransientFailures(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\",\"explanation\":\"요약 완료\",\"changes\":[],\"findings\":[]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:A1")
	config := Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}
	if _, usage, err := requestGatewayPlan(context.Background(), server.Client(), config, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{}); err != nil || usage.Attempts != 2 {
		t.Fatalf("transient retry: usage=%#v, %v", usage, err)
	}

	rejects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer rejects.Close()
	if _, usage, err := requestGatewayPlan(context.Background(), rejects.Client(), Config{GatewayURL: rejects.URL, Model: "m"}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{}); err == nil || usage.Attempts != 2 {
		t.Fatalf("a rejected JSON mode request gets one compatibility fallback: usage=%#v, %v", usage, err)
	}
}

func TestRequestGatewayPlanRepairsMalformedAndSemanticallyInvalidReplies(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:B1")
	cells := []workbook.Cell{{Row: 1, Column: 1, Value: json.RawMessage(`5`)}}
	t.Run("semantic validation feedback", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var request struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			if calls == 1 {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"수식 계획\",\"explanation\":\"\",\"changes\":[],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"stop"}]}`))
				return
			}
			if len(request.Messages) != 3 || !strings.Contains(request.Messages[2].Content, "must propose") {
				t.Errorf("repair messages=%#v", request.Messages)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"수식 계획\",\"explanation\":\"B1에 수식을 제안합니다.\",\"changes\":[{\"row\":1,\"column\":2,\"formula\":\"=A1*2\"}],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"stop"}]}`))
		}))
		defer server.Close()
		plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeFormula, Range: "A1:B1", Request: "B1에 두 배 수식", IdempotencyKey: "repair"}, selected, cells, ModelLimits{Model: "m"})
		if err != nil || usage.Attempts != 2 || len(plan.Changes) != 1 {
			t.Fatalf("plan=%#v usage=%#v err=%v", plan, usage, err)
		}
	})

	t.Run("malformed content and array content", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"I can create that plan."},"finish_reason":"stop"}]}`))
				return
			}
			content := "<think>done</think>\n```json\n{\"summary\":\"수식 계획\",\"explanation\":\"B1 변경\",\"changes\":[{\"row\":1,\"column\":2,\"formula\":\"=A1*2\"}],\"findings\":[],\"tool_calls\":[]}\n```"
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": content}}}, "finish_reason": "stop"}}})
		}))
		defer server.Close()
		plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeFormula, Range: "A1:B1", Request: "두 배", IdempotencyKey: "array"}, selected, cells, ModelLimits{Model: "m"})
		if err != nil || usage.Attempts != 2 || len(plan.Changes) != 1 {
			t.Fatalf("plan=%#v usage=%#v err=%v", plan, usage, err)
		}
	})
}

func TestRequestGatewayPlanFallsBackWhenResponseFormatIsUnsupported(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&request)
		if calls == 1 {
			if len(request["response_format"]) == 0 {
				t.Error("first request did not ask for JSON mode")
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(request["response_format"]) != 0 {
			t.Error("fallback kept unsupported response_format")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"요약\",\"explanation\":\"한 셀입니다.\",\"changes\":[],\"findings\":[],\"tool_calls\":[]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1")
	plan, usage, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil || calls != 2 || usage.Attempts != 2 || plan.Summary != "요약" {
		t.Fatalf("calls=%d plan=%#v usage=%#v err=%v", calls, plan, usage, err)
	}
}
