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

// 에이전트가 쓸 수 있는 것이 차트와 보고서 시트뿐이었다. "상위 10%에 색
// 칠해줘" 처럼 조건을 말하는 요청은 셀마다 배경색을 박아 넣는 수밖에 없었고,
// 그러면 자료가 바뀌어도 색은 그대로 남는다. 조건부 서식은 값에 따라 다시
// 칠하므로 그런 요청에는 이쪽이 맞다.
func TestGatewayAcceptsConditionalFormatTool(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": nil,
					"tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{
						"name":      "create_conditional_format",
						"arguments": `{"range":"A1:B2","rule_type":"rank","operator":"top_percent","value":10,"style":{"background":"#ffe08a"}}`,
					}}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:B2")
	plan, _, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "A1:B2", Request: "상위 10%에 색 칠해줘"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Name != "create_conditional_format" {
		t.Fatalf("plan=%#v", plan.ToolCalls)
	}
	var arguments createConditionalFormatArguments
	if err := json.Unmarshal(plan.ToolCalls[0].Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	if arguments.Range != "A1:B2" || arguments.RuleType != "rank" || arguments.Operator != "top_percent" {
		t.Fatalf("arguments=%#v", arguments)
	}
	// 서식은 그대로 실어 보내야 한다. 여기서 잃으면 규칙은 생기는데 색이 없다.
	if len(arguments.Style) == 0 {
		t.Error("style 이 비었다")
	}

	// 모르는 도구는 그대로 막는다.
	if supportedGatewayTool("delete_sheet") {
		t.Error("허용 목록에 없는 도구가 통과했다")
	}
}

// 규칙이 칠하는 곳은 사람이 고른 범위 안이어야 한다. 고르지도 않은 곳을
// 물들이는 것은 시킨 일이 아니고, 승인 화면에서도 눈에 잘 띄지 않는다.
func TestConditionalFormatStaysInsideTheSelection(t *testing.T) {
	t.Parallel()
	selected := PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "A1:B10"}
	if _, err := validateCreateConditionalFormat(selected, json.RawMessage(`{"range":"A1:D50","rule_type":"greater_than","value":10}`)); err == nil {
		t.Error("선택 밖 범위가 통과했다")
	}
	inside, err := validateCreateConditionalFormat(selected, json.RawMessage(`{"range":"A2:B5","rule_type":"greater_than","value":10}`))
	if err != nil || inside.Range != "A2:B5" {
		t.Fatalf("inside=%#v err=%v", inside, err)
	}
	// 범위를 적지 않으면 고른 범위를 그대로 쓴다.
	blank, err := validateCreateConditionalFormat(selected, json.RawMessage(`{"rule_type":"data_bar","bar_color":"#4c9aff"}`))
	if err != nil || blank.Range != "A1:B10" {
		t.Fatalf("blank=%#v err=%v", blank, err)
	}
	// 종류를 적지 않으면 무엇을 칠할지 알 수 없다.
	if _, err := validateCreateConditionalFormat(selected, json.RawMessage(`{"range":"A1:B2"}`)); err == nil {
		t.Error("rule_type 없이 통과했다")
	}
	// 에이전트 모드가 아닐 때는 아예 쓸 수 없다.
	if supportedGatewayTool("create_conditional_format") != true {
		t.Error("도구가 허용 목록에 없다")
	}
}

// 요약은 사람이 손으로 하기에 가장 지루한 일인데, 에이전트가 할 수 있는
// 것에 빠져 있었다. "부서별 매출 합계" 같은 요청은 원본을 건드리지 않고
// 피벗으로 답하는 것이 맞다 — 셀에 SUMIF 를 박아 넣으면 자료가 늘 때마다
// 수식을 다시 손봐야 한다.
func TestGatewayAcceptsPivotTool(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": nil,
					"tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{
						"name":      "create_pivot",
						"arguments": `{"source_range":"A1:C20","name":"부서별 매출","rows":[{"column":1,"name":"부서"}],"values":[{"column":3,"aggregation":"sum"}]}`,
					}}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:C20")
	plan, _, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "A1:C20", Request: "부서별 매출 합계"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Name != "create_pivot" {
		t.Fatalf("plan=%#v", plan.ToolCalls)
	}
	var arguments createPivotArguments
	if err := json.Unmarshal(plan.ToolCalls[0].Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	// 행과 값이 그대로 실려야 한다. 여기서 잃으면 피벗은 생기는데 빈 표다.
	if len(arguments.Rows) != 1 || arguments.Rows[0].Column != 1 || len(arguments.Values) != 1 || arguments.Values[0].Aggregation != "sum" {
		t.Fatalf("arguments=%#v", arguments)
	}
	if arguments.Name != "부서별 매출" {
		t.Errorf("name=%q", arguments.Name)
	}
	if !supportedGatewayTool("create_pivot") {
		t.Error("도구가 허용 목록에 없다")
	}
}

// 피벗은 원본을 읽기만 하지만, 읽는 곳은 사람이 고른 범위 안이어야 한다.
// 열 번호도 원본 안의 자리여야 한다 — 밖을 가리키면 승인 화면에는 그럴듯한
// 계획이 뜨고 실행할 때가 되어서야 깨진다.
func TestPivotArgumentsStayInsideTheSource(t *testing.T) {
	t.Parallel()
	selected := PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "A1:C20"}
	values := `"values":[{"column":3,"aggregation":"sum"}]`
	if _, err := validateCreatePivot(selected, json.RawMessage(`{"source_range":"A1:F40",`+values+`}`)); err == nil {
		t.Error("선택 밖 원본이 통과했다")
	}
	// 원본을 적지 않으면 고른 범위를 그대로 쓴다.
	blank, err := validateCreatePivot(selected, json.RawMessage(`{`+values+`}`))
	if err != nil || blank.SourceRange != "A1:C20" {
		t.Fatalf("blank=%#v err=%v", blank, err)
	}
	// 셀 자리가 아니라 원본 안의 몇 번째 열인지를 적는다. 4 는 세 열짜리 밖이다.
	if _, err := validateCreatePivot(selected, json.RawMessage(`{"values":[{"column":4,"aggregation":"sum"}]}`)); err == nil {
		t.Error("원본 밖 값 열이 통과했다")
	}
	if _, err := validateCreatePivot(selected, json.RawMessage(`{"rows":[{"column":9}],`+values+`}`)); err == nil {
		t.Error("원본 밖 행 열이 통과했다")
	}
	// 셀 수 없는 방법은 실행할 때가 아니라 계획할 때 막아야 한다.
	if _, err := validateCreatePivot(selected, json.RawMessage(`{"values":[{"column":3,"aggregation":"평균"}]}`)); err == nil {
		t.Error("모르는 집계 방법이 통과했다")
	}
	if !workbook.SupportedPivotAggregation("COUNTA ") || workbook.SupportedPivotAggregation("mode") {
		t.Error("집계 목록이 실제 계산과 어긋난다")
	}
	// 값이 없으면 무엇을 요약할지 알 수 없다.
	if _, err := validateCreatePivot(selected, json.RawMessage(`{"rows":[{"column":1}]}`)); err == nil {
		t.Error("값 없이 통과했다")
	}
}

// "이 열은 목록에서만 고르게 해줘" 는 서식으로는 못 한다. 색을 칠해 봐야
// 잘못된 값은 그대로 들어간다. 입력 규칙을 만들어야 애초에 막힌다.
func TestGatewayAcceptsDataValidationTool(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": nil,
					"tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{
						"name":      "create_data_validation",
						"arguments": `{"range":"B2:B20","rule_type":"list","options":[{"value":"진행"},{"value":"완료"}],"reject_input":true}`,
					}}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("B1:B20")
	plan, _, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "B1:B20", Request: "상태는 진행/완료만 고르게 해줘"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Name != "create_data_validation" {
		t.Fatalf("plan=%#v", plan.ToolCalls)
	}
	var arguments createDataValidationArguments
	if err := json.Unmarshal(plan.ToolCalls[0].Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	// 고를 값을 잃으면 규칙은 생기는데 목록이 비어 아무것도 못 넣는다.
	if len(arguments.Options) != 2 || arguments.RejectInput == nil || !*arguments.RejectInput {
		t.Fatalf("arguments=%#v", arguments)
	}
	if !supportedGatewayTool("create_data_validation") {
		t.Error("도구가 허용 목록에 없다")
	}
}

// 규칙마다 있어야 하는 것이 다르다. 빠진 채로 승인 화면에 오르면 사람은
// 그럴듯한 계획을 승인하고 실행할 때가 되어서야 깨진다.
func TestDataValidationNeedsWhatItsKindRequires(t *testing.T) {
	t.Parallel()
	selected := PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "B1:B20"}
	cases := []struct {
		name     string
		raw      string
		accepted bool
	}{
		{"목록에 고를 값이 없다", `{"rule_type":"list"}`, false},
		{"목록", `{"rule_type":"list","options":[{"value":"진행"}]}`, true},
		{"범위 목록에 출처가 없다", `{"rule_type":"list_range"}`, false},
		{"범위 목록의 출처는 고른 범위 밖이어도 된다", `{"rule_type":"list_range","source_range":"코드!A1:A9"}`, true},
		{"숫자 조건에 견줄 값이 없다", `{"rule_type":"number","operator":"greater_than"}`, false},
		{"숫자 조건", `{"rule_type":"number","operator":"greater_than","value":0}`, true},
		{"수식 조건에 수식이 없다", `{"rule_type":"custom_formula","formula":"B1>0"}`, false},
		{"수식 조건", `{"rule_type":"custom_formula","formula":"=B1>0"}`, true},
		{"체크박스는 더 필요한 것이 없다", `{"rule_type":"checkbox"}`, true},
		{"모르는 종류", `{"rule_type":"traffic_light"}`, false},
		{"고른 범위 밖", `{"range":"D1:D50","rule_type":"checkbox"}`, false},
	}
	for _, item := range cases {
		arguments, err := validateCreateDataValidation(selected, json.RawMessage(item.raw))
		if item.accepted && err != nil {
			t.Errorf("%s: %v", item.name, err)
		}
		if !item.accepted && err == nil {
			t.Errorf("%s: 통과했다", item.name)
		}
		// 범위를 적지 않으면 고른 범위를 그대로 쓴다.
		if item.accepted && err == nil && arguments.Range != "B1:B20" {
			t.Errorf("%s: range=%q", item.name, arguments.Range)
		}
	}
}

// "매출 100 이상만 보이게 해줘" 는 줄을 지우는 것이 아니다. 걸러내기는
// 숨길 뿐이므로 자료가 그대로 남는다.
func TestGatewayAcceptsFilterViewTool(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"content": nil,
					"tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{
						"name":      "create_filter_view",
						"arguments": `{"name":"매출 100 이상","range":"A1:B10","criteria":[{"column":2,"operator":"greater_or_equal","value":100}]}`,
					}}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:B10")
	plan, _, err := requestGatewayPlan(context.Background(), server.Client(), Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}, PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "A1:B10", Request: "매출 100 이상만 보이게 해줘"}, selected, nil, ModelLimits{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ToolCalls) != 1 || plan.ToolCalls[0].Name != "create_filter_view" {
		t.Fatalf("plan=%#v", plan.ToolCalls)
	}
	var arguments createFilterViewArguments
	if err := json.Unmarshal(plan.ToolCalls[0].Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	// 조건을 잃으면 아무것도 걸러내지 않는 필터가 생긴다.
	if len(arguments.Criteria) != 1 || arguments.Criteria[0].Column != 2 || string(arguments.Criteria[0].Value) != "100" {
		t.Fatalf("arguments=%#v", arguments)
	}
	// 숨기기만 하므로 자료를 지우는 것보다 위험이 낮다.
	planned, err := validateGatewayTools(PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "A1:B10"}, selected, plan.ToolCalls, 10)
	if err != nil || len(planned) != 1 || planned[0].Risk != RiskLow {
		t.Fatalf("planned=%#v err=%v", planned, err)
	}
	if !supportedGatewayTool("create_filter_view") {
		t.Error("도구가 허용 목록에 없다")
	}
}

// 여기의 열 번호는 피벗과 달리 시트의 열 번호다. 둘을 섞으면 엉뚱한 열을
// 걸러 놓고도 계획은 그럴듯해 보인다.
func TestFilterViewCriteriaMatchTheRepositoryRules(t *testing.T) {
	t.Parallel()
	selected := PlanInput{SheetID: "sheet-1", Mode: ModeAgent, Range: "B1:D10"}
	one := `"criteria":[{"column":2,"operator":"greater_or_equal","value":100}]`
	if _, err := validateCreateFilterView(selected, json.RawMessage(`{`+one+`}`)); err != nil {
		t.Fatalf("첫 열이 막혔다: %v", err)
	}
	// 범위가 B 부터인데 1(A) 을 가리키는 것은 범위 밖이다.
	if _, err := validateCreateFilterView(selected, json.RawMessage(`{"criteria":[{"column":1,"operator":"is_blank"}]}`)); err == nil {
		t.Error("범위 밖 열이 통과했다")
	}
	// 한 열에 조건 둘은 저장소가 거부한다. 계획할 때 걸러야 한다.
	if _, err := validateCreateFilterView(selected, json.RawMessage(`{"criteria":[{"column":2,"operator":"is_blank"},{"column":2,"operator":"is_not_blank"}]}`)); err == nil {
		t.Error("한 열에 조건 둘이 통과했다")
	}
	if _, err := validateCreateFilterView(selected, json.RawMessage(`{"criteria":[{"column":2,"operator":"비슷함"}]}`)); err == nil {
		t.Error("모르는 연산자가 통과했다")
	}
	if !workbook.SupportedFilterOperator(" NOT_CONTAINS ") || workbook.SupportedFilterOperator("between") {
		t.Error("연산자 목록이 실제 걸러내기와 어긋난다")
	}
	// 조건이 없으면 아무것도 걸러내지 않는 필터가 생긴다.
	if _, err := validateCreateFilterView(selected, json.RawMessage(`{"name":"보기"}`)); err == nil {
		t.Error("조건 없이 통과했다")
	}
	// 범위를 적지 않으면 고른 범위를 그대로 쓴다.
	blank, err := validateCreateFilterView(selected, json.RawMessage(`{`+one+`}`))
	if err != nil || blank.Range != "B1:D10" {
		t.Fatalf("blank=%#v err=%v", blank, err)
	}
}
