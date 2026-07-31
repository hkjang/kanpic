package workbook

import (
	"encoding/json"
	"time"
)

type Workbook struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title"`
	OwnerID     string    `json:"owner_id"`
	Favorite    bool      `json:"favorite"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Sheets      []Sheet   `json:"sheets,omitempty"`
}

type Sheet struct {
	ID         string    `json:"id"`
	WorkbookID string    `json:"workbook_id"`
	Name       string    `json:"name"`
	Position   int       `json:"position"`
	Color      string    `json:"color,omitempty"`
	Hidden     bool      `json:"hidden"`
	CreatedAt  time.Time `json:"created_at"`
}

type Cell struct {
	SheetID   string          `json:"sheet_id"`
	Row       int             `json:"row"`
	Column    int             `json:"column"`
	Value     json.RawMessage `json:"value,omitempty"`
	Formula   string          `json:"formula,omitempty"`
	Style     json.RawMessage `json:"style,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type CellInput struct {
	Row     int             `json:"row"`
	Column  int             `json:"column"`
	Value   json.RawMessage `json:"value,omitempty"`
	Formula string          `json:"formula,omitempty"`
	Style   json.RawMessage `json:"style,omitempty"`
}

type CellMutation struct {
	SheetID           string          `json:"sheet_id"`
	ActorID           string          `json:"actor_id"`
	ClientID          string          `json:"client_id"`
	BaseVersion       int64           `json:"base_version"`
	IdempotencyKey    string          `json:"idempotency_key"`
	Cells             []CellInput     `json:"cells"`
	StylePatch        json.RawMessage `json:"-"`
	Expected          map[string]Cell `json:"-"`
	OperationType     string          `json:"-"`
	UndoOfOperationID string          `json:"-"`
}

type UndoOperationInput struct {
	OperationID    string `json:"operation_id"`
	ActorID        string `json:"actor_id"`
	ClientID       string `json:"client_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type CellConflict struct {
	Row              int             `json:"row"`
	Column           int             `json:"column"`
	ChangedAtVersion int64           `json:"changed_at_version"`
	PreviousValue    json.RawMessage `json:"previous_value,omitempty"`
	SubmittedValue   json.RawMessage `json:"submitted_value,omitempty"`
}

type CellCoordinate struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

type CellFormulaError struct {
	Row     int    `json:"row"`
	Column  int    `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type MutationResult struct {
	OperationID        string                `json:"operation_id"`
	WorkbookID         string                `json:"workbook_id"`
	SheetID            string                `json:"sheet_id"`
	BaseVersion        int64                 `json:"base_version"`
	ServerVersion      int64                 `json:"server_version"`
	AppliedCells       int                   `json:"applied_cells"`
	RecalculatedCells  []CellCoordinate      `json:"recalculated_cells"`
	FormulaErrors      []CellFormulaError    `json:"formula_errors"`
	ValidationWarnings []ValidationViolation `json:"validation_warnings"`
	Duplicate          bool                  `json:"duplicate"`
	Conflicts          []CellConflict        `json:"conflicts"`
	CreatedAt          time.Time             `json:"created_at"`
}

type ValidationOption struct {
	Value json.RawMessage `json:"value"`
	Label string          `json:"label,omitempty"`
	Color string          `json:"color,omitempty"`
}

// DataValidation is a shared, server-authoritative rule applied to one sheet
// range. A cell may be covered by only one rule so browser, REST and MCP
// clients always observe the same editing contract.
type DataValidation struct {
	ID              string             `json:"id"`
	WorkbookID      string             `json:"workbook_id"`
	WorkbookVersion int64              `json:"workbook_version"`
	SheetID         string             `json:"sheet_id"`
	CreateKey       string             `json:"-"`
	Range           string             `json:"range"`
	RuleType        string             `json:"rule_type"`
	Operator        string             `json:"operator"`
	Options         []ValidationOption `json:"options,omitempty"`
	Value           json.RawMessage    `json:"value,omitempty"`
	Value2          json.RawMessage    `json:"value2,omitempty"`
	Formula         string             `json:"formula,omitempty"`
	AllowBlank      bool               `json:"allow_blank"`
	RejectInput     bool               `json:"reject_input"`
	ShowDropdown    bool               `json:"show_dropdown"`
	DisplayStyle    string             `json:"display_style"`
	HelpText        string             `json:"help_text,omitempty"`
	Revision        int64              `json:"revision"`
	CreatedBy       string             `json:"created_by"`
	UpdatedBy       string             `json:"updated_by"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

type CreateDataValidationInput struct {
	IdempotencyKey string             `json:"idempotency_key"`
	Range          string             `json:"range"`
	RuleType       string             `json:"rule_type"`
	Operator       string             `json:"operator,omitempty"`
	Options        []ValidationOption `json:"options,omitempty"`
	Value          json.RawMessage    `json:"value,omitempty"`
	Value2         json.RawMessage    `json:"value2,omitempty"`
	Formula        string             `json:"formula,omitempty"`
	AllowBlank     *bool              `json:"allow_blank,omitempty"`
	RejectInput    *bool              `json:"reject_input,omitempty"`
	ShowDropdown   *bool              `json:"show_dropdown,omitempty"`
	DisplayStyle   string             `json:"display_style,omitempty"`
	HelpText       string             `json:"help_text,omitempty"`
}

type UpdateDataValidationInput struct {
	Range            *string             `json:"range,omitempty"`
	RuleType         *string             `json:"rule_type,omitempty"`
	Operator         *string             `json:"operator,omitempty"`
	Options          *[]ValidationOption `json:"options,omitempty"`
	Value            *json.RawMessage    `json:"value,omitempty"`
	Value2           *json.RawMessage    `json:"value2,omitempty"`
	Formula          *string             `json:"formula,omitempty"`
	AllowBlank       *bool               `json:"allow_blank,omitempty"`
	RejectInput      *bool               `json:"reject_input,omitempty"`
	ShowDropdown     *bool               `json:"show_dropdown,omitempty"`
	DisplayStyle     *string             `json:"display_style,omitempty"`
	HelpText         *string             `json:"help_text,omitempty"`
	ExpectedRevision *int64              `json:"expected_revision,omitempty"`
}

type ValidationViolation struct {
	ValidationID string `json:"validation_id"`
	Row          int    `json:"row"`
	Column       int    `json:"column"`
	Message      string `json:"message"`
}

type ValidationEvaluation struct {
	ValidationID string                `json:"validation_id"`
	Range        string                `json:"range"`
	CheckedCells int                   `json:"checked_cells"`
	ValidCells   int                   `json:"valid_cells"`
	InvalidCells []ValidationViolation `json:"invalid_cells"`
	Truncated    bool                  `json:"truncated"`
}

type Version struct {
	ID              string    `json:"id"`
	WorkbookID      string    `json:"workbook_id"`
	WorkbookVersion int64     `json:"workbook_version"`
	Name            string    `json:"name,omitempty"`
	ActorID         string    `json:"actor_id"`
	CreatedAt       time.Time `json:"created_at"`
}

type CreateWorkbookInput struct {
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	OwnerID     string `json:"owner_id"`
}

type DuplicateWorkbookInput struct {
	Title   string `json:"title,omitempty"`
	OwnerID string `json:"owner_id,omitempty"`
}

type UpdateWorkbookInput struct {
	Title    *string `json:"title,omitempty"`
	Favorite *bool   `json:"favorite,omitempty"`
}

type CreateSheetInput struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type UpdateSheetInput struct {
	Name     *string `json:"name,omitempty"`
	Position *int    `json:"position,omitempty"`
	Color    *string `json:"color,omitempty"`
	Hidden   *bool   `json:"hidden,omitempty"`
}

type DuplicateSheetInput struct {
	Name string `json:"name,omitempty"`
}

type ImportSheet struct {
	Name  string      `json:"name"`
	Color string      `json:"color,omitempty"`
	Cells []CellInput `json:"cells"`
}

type ImportWorkbookInput struct {
	WorkspaceID    string        `json:"workspace_id"`
	Title          string        `json:"title"`
	OwnerID        string        `json:"owner_id"`
	ActorID        string        `json:"actor_id"`
	IdempotencyKey string        `json:"idempotency_key"`
	FileName       string        `json:"file_name"`
	Format         string        `json:"format"`
	Sheets         []ImportSheet `json:"sheets"`
}

type FilterCriterion struct {
	Column        int               `json:"column"`
	Operator      string            `json:"operator"`
	Value         json.RawMessage   `json:"value,omitempty"`
	Values        []json.RawMessage `json:"values,omitempty"`
	Color         string            `json:"color,omitempty"`
	CaseSensitive bool              `json:"case_sensitive,omitempty"`
}

type FilterView struct {
	ID         string            `json:"id"`
	SheetID    string            `json:"sheet_id"`
	ActorID    string            `json:"actor_id"`
	CreateKey  string            `json:"-"`
	Name       string            `json:"name"`
	Range      string            `json:"range"`
	HeaderRows int               `json:"header_rows"`
	Criteria   []FilterCriterion `json:"criteria"`
	Active     bool              `json:"active"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

type CreateFilterViewInput struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Name           string            `json:"name"`
	Range          string            `json:"range"`
	HeaderRows     int               `json:"header_rows"`
	Criteria       []FilterCriterion `json:"criteria"`
	Active         bool              `json:"active"`
}

type UpdateFilterViewInput struct {
	Name       *string            `json:"name,omitempty"`
	Range      *string            `json:"range,omitempty"`
	HeaderRows *int               `json:"header_rows,omitempty"`
	Criteria   *[]FilterCriterion `json:"criteria,omitempty"`
	Active     *bool              `json:"active,omitempty"`
}

type FilterResult struct {
	FilterViewID string `json:"filter_view_id"`
	Range        string `json:"range"`
	HiddenRows   []int  `json:"hidden_rows"`
	VisibleCount int    `json:"visible_count"`
	HiddenCount  int    `json:"hidden_count"`
	TotalCount   int    `json:"total_count"`
}
