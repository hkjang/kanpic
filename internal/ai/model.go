package ai

import (
	"context"
	"encoding/json"
	"errors"
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

	StatusPlanned   = "planned"
	StatusCompleted = "completed"
	StatusApplying  = "applying"
	StatusApplied   = "applied"
	StatusUndoing   = "undoing"
	StatusUndone    = "undone"
	StatusFailed    = "failed"
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
	approvalIdempotencyKey string
	undoIdempotencyKey     string
}

type PlanInput struct {
	WorkbookID     string `json:"workbook_id"`
	SheetID        string `json:"sheet_id"`
	Range          string `json:"range"`
	Request        string `json:"request"`
	Mode           string `json:"mode"`
	BaseVersion    int64  `json:"base_version"`
	IdempotencyKey string `json:"idempotency_key"`
	ClientID       string `json:"client_id,omitempty"`
	ActorID        string `json:"-"`
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
}

type Orchestrator interface {
	PublicConfig(context.Context) (Config, error)
	Plan(context.Context, PlanInput) (Action, error)
	Get(context.Context, string, string) (Action, error)
	List(context.Context, string, string, int) ([]Action, error)
	Approve(context.Context, string, ApprovalInput) (ExecutionResult, error)
	Undo(context.Context, string, ApprovalInput) (ExecutionResult, error)
}

func IsReadOnlyMode(mode string) bool {
	return mode == ModeExplain || mode == ModeSummarize || mode == ModeAnomaly
}

func RequiredApprovalScope(mode string) string {
	if mode == ModeClean {
		return "range.write"
	}
	return "formula.write"
}
