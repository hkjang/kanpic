package ai

import (
	"encoding/json"
	"time"

	"kanpic/internal/workbook"
)

const (
	AgentThinking        = "THINKING"
	AgentReadingWorkbook = "READING_WORKBOOK"
	AgentPlanning        = "PLANNING"
	AgentWaitingApproval = "WAITING_APPROVAL"
	AgentExecuting       = "EXECUTING"
	AgentValidating      = "VALIDATING"
	AgentCompleted       = "COMPLETED"
	AgentFailed          = "FAILED"
	AgentCancelled       = "CANCELLED"
)

const (
	RiskRead     = "READ"
	RiskLow      = "LOW"
	RiskMedium   = "MEDIUM"
	RiskHigh     = "HIGH"
	RiskCritical = "CRITICAL"
)

type AgentMessageInput struct {
	WorkbookID     string `json:"workbook_id"`
	SheetID        string `json:"sheet_id"`
	Selection      string `json:"selection"`
	Message        string `json:"message"`
	Mode           string `json:"mode,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	BaseVersion    int64  `json:"base_version"`
	IdempotencyKey string `json:"idempotency_key"`
	ClientID       string `json:"client_id,omitempty"`
	ActorID        string `json:"-"`
}

type ConversationMessage struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	AgentRunID     string    `json:"agent_run_id,omitempty"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type PlanStep struct {
	ID          string          `json:"id,omitempty"`
	Position    int             `json:"position"`
	ToolName    string          `json:"tool"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	Risk        string          `json:"risk"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type AgentPlan struct {
	ID        string     `json:"id,omitempty"`
	RunID     string     `json:"run_id"`
	Goal      string     `json:"goal"`
	Risk      string     `json:"risk"`
	Status    string     `json:"status"`
	Steps     []PlanStep `json:"steps"`
	CreatedAt time.Time  `json:"created_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at,omitempty"`
}

type ToolCall struct {
	ID             string          `json:"id,omitempty"`
	Name           string          `json:"name"`
	Arguments      json.RawMessage `json:"arguments"`
	Result         json.RawMessage `json:"result,omitempty"`
	Status         string          `json:"status"`
	Risk           string          `json:"risk"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	DurationMS     int64           `json:"duration_ms,omitempty"`
}

type ValidationCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Passed    bool              `json:"passed"`
	Checks    []ValidationCheck `json:"checks"`
	CheckedAt *time.Time        `json:"checked_at,omitempty"`
}

type AgentRun struct {
	ID             string                `json:"id"`
	ConversationID string                `json:"conversation_id"`
	ChangeSetID    string                `json:"change_set_id,omitempty"`
	WorkbookID     string                `json:"workbook_id"`
	SheetID        string                `json:"sheet_id"`
	Selection      string                `json:"selection"`
	Intent         string                `json:"intent"`
	State          string                `json:"state"`
	Goal           string                `json:"goal"`
	Risk           string                `json:"risk"`
	Context        workbook.AgentContext `json:"context"`
	Plan           AgentPlan             `json:"plan"`
	Action         Action                `json:"action"`
	Messages       []ConversationMessage `json:"messages"`
	Validation     ValidationResult      `json:"validation"`
	StartedAt      time.Time             `json:"started_at"`
	CompletedAt    *time.Time            `json:"completed_at,omitempty"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

type AgentExecutionResult struct {
	Run       AgentRun                 `json:"run"`
	Operation *workbook.MutationResult `json:"operation,omitempty"`
	Changes   []workbook.CellInput     `json:"changes,omitempty"`
}
