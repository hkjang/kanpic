package automation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"kanpic/internal/workbook"
)

var (
	ErrNotFound  = errors.New("automation not found")
	ErrInvalid   = errors.New("invalid automation")
	ErrRevision  = errors.New("automation revision conflict")
	ErrDisabled  = errors.New("automation is disabled")
	ErrRate      = errors.New("automation rate limit exceeded")
	ErrNoChanges = errors.New("automation has no effective changes")
	ErrRunFailed = errors.New("automation run previously failed")
)

const (
	TriggerManual     = "manual"
	TriggerCellChange = "cell_change"
	TriggerSchedule   = "schedule"
	TriggerWebhook    = "webhook"

	ActionSetValue   = "set_value"
	ActionSetFormula = "set_formula"
	ActionClear      = "clear"

	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusSkipped   = "skipped"
	StatusFailed    = "failed"
	StatusUndoing   = "undoing"
	StatusUndone    = "undone"
)

type TriggerDefinition struct {
	Type     string `json:"type"`
	SheetID  string `json:"sheet_id,omitempty"`
	Range    string `json:"range,omitempty"`
	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type ActionDefinition struct {
	Type    string          `json:"type"`
	SheetID string          `json:"sheet_id"`
	Range   string          `json:"range"`
	Value   json.RawMessage `json:"value,omitempty"`
	Formula string          `json:"formula,omitempty"`
}

type Automation struct {
	ID             string            `json:"id"`
	WorkbookID     string            `json:"workbook_id"`
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Trigger        TriggerDefinition `json:"trigger"`
	Action         ActionDefinition  `json:"action"`
	Revision       int64             `json:"revision"`
	CreatedBy      string            `json:"created_by"`
	UpdatedBy      string            `json:"updated_by"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	NextRunAt      *time.Time        `json:"next_run_at,omitempty"`
	LastFailure    *RunFailure       `json:"last_failure,omitempty"`
	Duplicate      bool              `json:"duplicate,omitempty"`
	idempotencyKey string
}

// RunFailure is the latest run of an automation when that run failed. A
// trigger fires without anyone watching, so the panel needs the failure on the
// definition itself rather than only inside each automation's run history.
type RunFailure struct {
	RunID       string    `json:"run_id"`
	TriggerType string    `json:"trigger_type"`
	Message     string    `json:"message"`
	At          time.Time `json:"at"`
}

// Overview is what the automation panel reads. ExecutionEnabled reports the
// admin-level switch: with it off, definitions still save and preview but no
// trigger ever runs, and without this the panel cannot say so.
type Overview struct {
	Items            []Automation `json:"items"`
	ExecutionEnabled bool         `json:"execution_enabled"`
}

type CreateInput struct {
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Trigger        TriggerDefinition `json:"trigger"`
	Action         ActionDefinition  `json:"action"`
	IdempotencyKey string            `json:"idempotency_key"`
}

type UpdateInput struct {
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	Trigger          TriggerDefinition `json:"trigger"`
	Action           ActionDefinition  `json:"action"`
	ExpectedRevision int64             `json:"expected_revision"`
}

type RunInput struct {
	ActorID             string     `json:"-"`
	ClientID            string     `json:"client_id,omitempty"`
	IdempotencyKey      string     `json:"idempotency_key"`
	ExpectedRevision    int64      `json:"expected_revision,omitempty"`
	ExpectedBaseVersion int64      `json:"expected_base_version,omitempty"`
	TriggerType         string     `json:"-"`
	TriggerOperationID  string     `json:"-"`
	ScheduledFor        *time.Time `json:"-"`
	TriggerKeyID        string     `json:"-"`
	PayloadDigest       string     `json:"-"`
	PayloadBytes        int        `json:"-"`
}

type CellSnapshot struct {
	Value       json.RawMessage `json:"value,omitempty"`
	Formula     string          `json:"formula,omitempty"`
	Style       json.RawMessage `json:"style,omitempty"`
	SpillSource string          `json:"spill_source,omitempty"`
}

type PreviewChange struct {
	Row     int          `json:"row"`
	Column  int          `json:"column"`
	Address string       `json:"address"`
	Before  CellSnapshot `json:"before"`
	After   CellSnapshot `json:"after"`
}

type Preview struct {
	AutomationID       string           `json:"automation_id"`
	AutomationName     string           `json:"automation_name"`
	AutomationRevision int64            `json:"automation_revision"`
	WorkbookID         string           `json:"workbook_id"`
	BaseVersion        int64            `json:"base_version"`
	Action             ActionDefinition `json:"action"`
	Changes            []PreviewChange  `json:"changes"`
}

type Run struct {
	ID                 string                   `json:"id"`
	AutomationID       string                   `json:"automation_id"`
	WorkbookID         string                   `json:"workbook_id"`
	ActorID            string                   `json:"actor_id"`
	TriggerType        string                   `json:"trigger_type"`
	TriggerOperationID string                   `json:"trigger_operation_id,omitempty"`
	ScheduledFor       *time.Time               `json:"scheduled_for,omitempty"`
	TriggerKeyID       string                   `json:"trigger_key_id,omitempty"`
	PayloadDigest      string                   `json:"payload_digest,omitempty"`
	PayloadBytes       int                      `json:"payload_bytes,omitempty"`
	Status             string                   `json:"status"`
	BaseVersion        int64                    `json:"base_version"`
	Action             ActionDefinition         `json:"action"`
	OperationID        string                   `json:"operation_id,omitempty"`
	Operation          *workbook.MutationResult `json:"operation,omitempty"`
	UndoOperationID    string                   `json:"undo_operation_id,omitempty"`
	UndoOperation      *workbook.MutationResult `json:"undo_operation,omitempty"`
	ErrorMessage       string                   `json:"error_message,omitempty"`
	StartedAt          time.Time                `json:"started_at"`
	CompletedAt        *time.Time               `json:"completed_at,omitempty"`
	UpdatedAt          time.Time                `json:"updated_at"`
	Duplicate          bool                     `json:"duplicate,omitempty"`
	cells              []workbook.CellInput
	expected           map[string]workbook.Cell
	idempotencyKey     string
	undoKey            string
	excludedFromRate   bool
	automationRevision int64
}

type ExecutionResult struct {
	Run       Run                     `json:"run"`
	Operation workbook.MutationResult `json:"operation"`
	Changes   []workbook.CellInput    `json:"changes,omitempty"`
}

type ServiceAPI interface {
	Create(context.Context, string, string, CreateInput) (Automation, error)
	List(context.Context, string) ([]Automation, error)
	Overview(context.Context, string) (Overview, error)
	Get(context.Context, string) (Automation, error)
	Update(context.Context, string, string, UpdateInput) (Automation, error)
	Delete(context.Context, string, string, int64) error
	Preview(context.Context, string) (Preview, error)
	Run(context.Context, string, RunInput) (ExecutionResult, error)
	ListRuns(context.Context, string, int) ([]Run, error)
	GetRunWorkbookID(context.Context, string) (string, error)
	Undo(context.Context, string, RunInput) (ExecutionResult, error)
	TriggerCellChange(context.Context, workbook.MutationResult, []workbook.CellInput, string) ([]ExecutionResult, error)
}

func RequiredActionScope(actionType string) string {
	if actionType == ActionSetFormula {
		return "formula.write"
	}
	return "range.write"
}
