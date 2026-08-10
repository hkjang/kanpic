package ai

import (
	"context"
	"encoding/json"
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
	// A window smaller than the floor still leaves a usable budget.
	if budget := planTokenBudget(config, 7_800, 8_192); budget != minOutputTokens {
		t.Fatalf("tight window budget = %d", budget)
	}
	// A very large window is capped so one plan cannot run away.
	if budget := planTokenBudget(config, 1_000, 1_000_000); budget != maxOutputTokens {
		t.Fatalf("huge window budget = %d", budget)
	}
	// Without a published window the budget stays conservative.
	if budget := planTokenBudget(config, 4_000, 0); budget != 8_192 {
		t.Fatalf("unknown window budget = %d", budget)
	}
	// An administrator cap always wins.
	if budget := planTokenBudget(Config{MaxChanges: 100, MaxOutputTokens: 2_048}, 1_000, 128_000); budget != 2_048 {
		t.Fatalf("configured budget = %d", budget)
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
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"완성\",\"explanation\":\"\",\"changes\":[],\"findings\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":80}}`))
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
	if usage.Attempts != 2 || usage.CompletionTokens != 80 {
		t.Fatalf("usage=%#v", usage)
	}
}

// When the budget already fills the published window there is nothing left to
// grow into, so the caller is told what to do instead of retrying forever.
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
	if calls != 1 || usage.CompletionTokens != 31000 {
		t.Fatalf("calls=%d usage=%#v", calls, usage)
	}
}

// A gateway that is briefly unavailable is retried; a rejected request is not.
func TestRequestGatewayPlanRetriesOnlyTransientFailures(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\",\"changes\":[],\"findings\":[]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	selected, _ := cellrange.Parse("A1:A1")
	config := Config{GatewayURL: server.URL, Model: "m", MaxChanges: 10}
	if _, usage, err := requestGatewayPlan(context.Background(), server.Client(), config, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{}); err != nil || usage.Attempts != 2 {
		t.Fatalf("transient retry: usage=%#v, %v", usage, err)
	}

	rejects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer rejects.Close()
	if _, usage, err := requestGatewayPlan(context.Background(), rejects.Client(), Config{GatewayURL: rejects.URL, Model: "m"}, PlanInput{Mode: ModeSummarize, Range: "A1", Request: "요약"}, selected, nil, ModelLimits{}); err == nil || usage.Attempts != 1 {
		t.Fatalf("rejected request should not retry: usage=%#v, %v", usage, err)
	}
}
