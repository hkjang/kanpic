package ai

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"kanpic/internal/workbook"
)

var (
	ErrDisabled  = errors.New("AI is disabled")
	ErrInvalid   = errors.New("invalid AI action")
	ErrNotFound  = errors.New("AI action not found")
	ErrRevision  = errors.New("AI action revision conflict")
	ErrForbidden = errors.New("AI action belongs to another user")
	ErrGateway   = errors.New("AI gateway request failed")
)

const (
	ModeFormula   = "formula"
	ModeExplain   = "explain"
	ModeFix       = "fix"
	ModeSummarize = "summarize"
	ModeAnomaly   = "anomaly"
	ModeClean     = "clean"
	ModeFormat    = "format"
	ModeChart     = "chart"
	ModeAgent     = "agent"

	StatusPlanned   = "planned"
	StatusCompleted = "completed"
	StatusApplying  = "applying"
	StatusApplied   = "applied"
	StatusUndoing   = "undoing"
	StatusUndone    = "undone"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type CellSnapshot struct {
	Value       json.RawMessage `json:"value,omitempty"`
	Formula     string          `json:"formula,omitempty"`
	Style       json.RawMessage `json:"style,omitempty"`
	SpillSource string          `json:"spill_source,omitempty"`
}

type ProposedChange struct {
	Row     int          `json:"row"`
	Column  int          `json:"column"`
	Address string       `json:"address"`
	Before  CellSnapshot `json:"before"`
	After   CellSnapshot `json:"after"`
}

type Finding struct {
	Row         int          `json:"row,omitempty"`
	Column      int          `json:"column,omitempty"`
	Address     string       `json:"address,omitempty"`
	Severity    string       `json:"severity"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Cell        CellSnapshot `json:"cell,omitempty"`
}

type Event struct {
	ID        int64           `json:"id"`
	ActorID   string          `json:"actor_id"`
	EventType string          `json:"event_type"`
	Model     string          `json:"model,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type Action struct {
	ID                     string                   `json:"id"`
	WorkbookID             string                   `json:"workbook_id"`
	SheetID                string                   `json:"sheet_id"`
	ActorID                string                   `json:"actor_id"`
	ClientID               string                   `json:"client_id,omitempty"`
	IdempotencyKey         string                   `json:"-"`
	Mode                   string                   `json:"mode"`
	Range                  string                   `json:"range"`
	Request                string                   `json:"request"`
	Status                 string                   `json:"status"`
	BaseVersion            int64                    `json:"base_version"`
	Model                  string                   `json:"model"`
	Summary                string                   `json:"summary"`
	Explanation            string                   `json:"explanation,omitempty"`
	Changes                []ProposedChange         `json:"changes"`
	Findings               []Finding                `json:"findings"`
	InputCellCount         int                      `json:"input_cell_count"`
	Revision               int64                    `json:"revision"`
	OperationID            string                   `json:"operation_id,omitempty"`
	Operation              *workbook.MutationResult `json:"operation,omitempty"`
	UndoOperationID        string                   `json:"undo_operation_id,omitempty"`
	UndoOperation          *workbook.MutationResult `json:"undo_operation,omitempty"`
	ErrorMessage           string                   `json:"error_message,omitempty"`
	ApprovedAt             *time.Time               `json:"approved_at,omitempty"`
	UndoneAt               *time.Time               `json:"undone_at,omitempty"`
	CreatedAt              time.Time                `json:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at"`
	Events                 []Event                  `json:"events,omitempty"`
	Duplicate              bool                     `json:"duplicate,omitempty"`
	Usage                  *Usage                   `json:"usage,omitempty"`
	ConversationID         string                   `json:"conversation_id,omitempty"`
	Risk                   string                   `json:"risk"`
	Plan                   []PlanStep               `json:"plan"`
	ToolCalls              []ToolCall               `json:"tool_calls"`
	Validation             ValidationResult         `json:"validation"`
	approvalIdempotencyKey string
	undoIdempotencyKey     string
}

type PlanInput struct {
	WorkbookID     string                 `json:"workbook_id"`
	SheetID        string                 `json:"sheet_id"`
	Range          string                 `json:"range"`
	Request        string                 `json:"request"`
	Mode           string                 `json:"mode"`
	BaseVersion    int64                  `json:"base_version"`
	IdempotencyKey string                 `json:"idempotency_key"`
	ClientID       string                 `json:"client_id,omitempty"`
	ActorID        string                 `json:"-"`
	ConversationID string                 `json:"conversation_id,omitempty"`
	Context        *workbook.AgentContext `json:"-"`
	Conversation   []ConversationMessage  `json:"-"`
	Memory         []AgentWorkMemory      `json:"-"`
	Charts         []workbook.Chart       `json:"-"`
	Skill          string                 `json:"-"`
}

type ApprovalInput struct {
	ActorID          string `json:"-"`
	ClientID         string `json:"client_id,omitempty"`
	IdempotencyKey   string `json:"idempotency_key"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ExecutionResult struct {
	Action    Action                  `json:"action"`
	Operation workbook.MutationResult `json:"operation"`
	Changes   []workbook.CellInput    `json:"changes,omitempty"`
}

type Config struct {
	Enabled       bool          `json:"enabled"`
	GatewayURL    string        `json:"-"`
	Model         string        `json:"model"`
	APIKey        string        `json:"-"`
	CAPEM         string        `json:"-"`
	Timeout       time.Duration `json:"-"`
	MaxInputCells int           `json:"max_input_cells"`
	MaxChanges    int           `json:"max_changes"`
	// MaxOutputTokens caps the reply. Zero means the budget is derived from the
	// model's own context length so a plan is never cut short needlessly.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

// Usage reports what one gateway call actually cost, so the wait and the size
// of a request stop being a mystery.
type Usage struct {
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	MaxTokens        int    `json:"max_tokens,omitempty"`
	ContextWindow    int    `json:"context_window,omitempty"`
	Attempts         int    `json:"attempts,omitempty"`
	Model            string `json:"model,omitempty"`
}

type Orchestrator interface {
	PublicConfig(context.Context) (Config, error)
	Plan(context.Context, PlanInput) (Action, error)
	Preview(context.Context, PlanInput) (PromptPreview, error)
	Get(context.Context, string, string) (Action, error)
	List(context.Context, string, string, int) ([]Action, error)
	Approve(context.Context, string, ApprovalInput) (ExecutionResult, error)
	Undo(context.Context, string, ApprovalInput) (ExecutionResult, error)
	History(context.Context, HistoryFilter) (HistoryPage, error)
	AdminGet(context.Context, string) (Action, error)
	PurgeHistory(context.Context, time.Time, string) (int64, error)
	RetentionDays(context.Context) int
}

// WorkbookAgent is the richer, conversation-oriented API implemented by the
// production service. Keeping it separate from Orchestrator preserves the
// existing embeddable AI action interface for integrations and tests.
type WorkbookAgent interface {
	SendMessage(context.Context, AgentMessageInput) (AgentRun, error)
	GetRun(context.Context, string, string) (AgentRun, error)
	GetRunPlan(context.Context, string, string) (AgentPlan, error)
	ListRuns(context.Context, string, string, int) ([]AgentRun, error)
	ListConversations(context.Context, string, string, int) ([]AgentConversation, error)
	RunForChangeSet(context.Context, string, string) (AgentRun, error)
	ApproveRun(context.Context, string, ApprovalInput) (AgentExecutionResult, error)
	CancelRun(context.Context, string, ApprovalInput) (AgentRun, error)
	RollbackChangeSet(context.Context, string, ApprovalInput) (AgentExecutionResult, error)
}

func IsReadOnlyMode(mode string) bool {
	return mode == ModeExplain || mode == ModeSummarize || mode == ModeAnomaly
}

func RequiredApprovalScope(mode string) string {
	if mode == ModeClean || mode == ModeFormat || mode == ModeAgent {
		return "range.write"
	}
	if mode == ModeChart {
		return "chart.write"
	}
	return "formula.write"
}

func RequiredApprovalScopes(action Action) []string {
	seen := map[string]bool{}
	add := func(scope string) {
		if scope != "" {
			seen[scope] = true
		}
	}
	add(RequiredApprovalScope(action.Mode))
	for _, tool := range action.ToolCalls {
		// 도구가 만드는 것은 REST·MCP 에서 각자의 scope 로 지키는 물건들이다.
		// 에이전트라고 해서 더 적은 권한으로 만들 수 있으면, 에이전트가
		// 권한을 넘는 지름길이 된다.
		switch tool.Name {
		case "create_chart", "update_chart":
			add("chart.write")
		case "create_report_sheet":
			add("range.write")
			// 보고서 안의 차트도 차트다.
			var arguments createReportSheetArguments
			if json.Unmarshal(tool.Arguments, &arguments) == nil && arguments.Chart != nil {
				add("chart.write")
			}
		case "create_conditional_format":
			add("format.write")
		case "create_pivot":
			add("pivot.write")
		case "create_data_validation":
			add("range.write")
		}
	}
	// 여기 없는 scope 는 위에서 넣어도 조용히 사라진다. 도구를 늘릴 때는
	// 두 곳을 함께 손봐야 한다.
	ordered := []string{"formula.write", "range.write", "format.write", "chart.write", "pivot.write"}
	result := make([]string, 0, len(seen))
	for _, scope := range ordered {
		if seen[scope] {
			result = append(result, scope)
		}
	}
	// 목록에 없는 scope 를 요구했다면 조용히 빠뜨리는 대신 뒤에 붙인다.
	// 검사에서 빠진 scope 는 없는 것과 같고, 그것이 가장 나쁜 실패다.
	if len(result) != len(seen) {
		extra := make([]string, 0, len(seen)-len(result))
		for scope := range seen {
			if !slices.Contains(result, scope) {
				extra = append(extra, scope)
			}
		}
		slices.Sort(extra)
		result = append(result, extra...)
	}
	return result
}
