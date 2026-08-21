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

	// Sharing state and, for list and read responses, the effective access of
	// the requesting principal.
	LinkAccess    string    `json:"link_access,omitempty"`
	LinkRole      ShareRole `json:"link_role,omitempty"`
	SharingLocked bool      `json:"sharing_locked,omitempty"`
	ViewerCanCopy bool      `json:"viewer_can_copy,omitempty"`
	AccessRole    ShareRole `json:"access_role,omitempty"`
	AccessSource  string    `json:"access_source,omitempty"`
	SharedCount   int       `json:"shared_count,omitempty"`

	// Trash metadata, populated only for deleted workbooks.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	DeletedBy string     `json:"deleted_by,omitempty"`
}

type Sheet struct {
	ID         string      `json:"id"`
	WorkbookID string      `json:"workbook_id"`
	Name       string      `json:"name"`
	Position   int         `json:"position"`
	Color      string      `json:"color,omitempty"`
	Hidden     bool        `json:"hidden"`
	Layout     SheetLayout `json:"layout"`
	CreatedAt  time.Time   `json:"created_at"`
}

// DimensionSize stores only dimensions that differ from the browser defaults.
// Index is one-based, matching spreadsheet addresses.
type DimensionSize struct {
	Index int     `json:"index"`
	Size  float64 `json:"size"`
}

// DimensionRange is an inclusive, one-based hidden row or column interval.
type DimensionRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SheetLayout is kept inside sheets.properties JSONB so older databases can
// adopt layout support without a schema migration. Revision protects layout
// edits independently from unrelated cell operations.
type SheetLayout struct {
	Revision      int64            `json:"revision"`
	RowHeights    []DimensionSize  `json:"row_heights,omitempty"`
	ColumnWidths  []DimensionSize  `json:"column_widths,omitempty"`
	HiddenRows    []DimensionRange `json:"hidden_rows,omitempty"`
	HiddenColumns []DimensionRange `json:"hidden_columns,omitempty"`
	RowGroups     []DimensionGroup `json:"row_groups,omitempty"`
	ColumnGroups  []DimensionGroup `json:"column_groups,omitempty"`
	FrozenRows    int              `json:"frozen_rows"`
	FrozenColumns int              `json:"frozen_columns"`
	Slicers       []Slicer         `json:"slicers,omitempty"`
}

// Slicer is a filter control pinned to the sheet. It edits one column of one
// filter view, so the rows it hides are hidden by the filter everybody already
// shares rather than by a second, parallel filtering mechanism.
type Slicer struct {
	ID           string        `json:"id"`
	FilterViewID string        `json:"filter_view_id"`
	Column       int           `json:"column"`
	Title        string        `json:"title,omitempty"`
	Position     ChartPosition `json:"position"`
}

// DimensionGroup folds a run of rows or columns away behind one control, which
// is how a long model keeps its detail without showing all of it at once.
// Groups may nest; Depth is derived from how many groups enclose this one.
type DimensionGroup struct {
	Start     int  `json:"start"`
	End       int  `json:"end"`
	Collapsed bool `json:"collapsed"`
	Depth     int  `json:"depth"`
}

type SheetLayoutMutation struct {
	SheetID          string  `json:"sheet_id"`
	ActorID          string  `json:"actor_id"`
	ClientID         string  `json:"client_id"`
	IdempotencyKey   string  `json:"idempotency_key"`
	ExpectedRevision int64   `json:"expected_revision"`
	Action           string  `json:"action"`
	Axis             string  `json:"axis,omitempty"`
	Start            int     `json:"start,omitempty"`
	Count            int     `json:"count,omitempty"`
	Size             float64 `json:"size,omitempty"`
	FrozenRows       int     `json:"frozen_rows,omitempty"`
	FrozenColumns    int     `json:"frozen_columns,omitempty"`
	Slicer           *Slicer `json:"slicer,omitempty"`
}

type SheetLayoutResult struct {
	OperationID   string      `json:"operation_id"`
	WorkbookID    string      `json:"workbook_id"`
	SheetID       string      `json:"sheet_id"`
	BaseVersion   int64       `json:"base_version"`
	ServerVersion int64       `json:"server_version"`
	Layout        SheetLayout `json:"layout"`
	Duplicate     bool        `json:"duplicate"`
	CreatedAt     time.Time   `json:"created_at"`
}

type Cell struct {
	SheetID     string          `json:"sheet_id"`
	Row         int             `json:"row"`
	Column      int             `json:"column"`
	Value       json.RawMessage `json:"value,omitempty"`
	Formula     string          `json:"formula,omitempty"`
	Style       json.RawMessage `json:"style,omitempty"`
	SpillSource string          `json:"spill_source,omitempty"`
	// Note is the small annotation a reader sees on hover. It belongs to the
	// cell rather than to a comment thread, so it travels with the cell when
	// rows move and never starts a conversation.
	Note      string    `json:"note,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CellInput struct {
	SheetID     string          `json:"-"`
	Row         int             `json:"row"`
	Column      int             `json:"column"`
	Value       json.RawMessage `json:"value,omitempty"`
	Formula     string          `json:"formula,omitempty"`
	Style       json.RawMessage `json:"style,omitempty"`
	Note        string          `json:"note,omitempty"`
	SpillSource string          `json:"-"`
}

type CellMutation struct {
	SheetID        string          `json:"sheet_id"`
	ActorID        string          `json:"actor_id"`
	ClientID       string          `json:"client_id"`
	BaseVersion    int64           `json:"base_version"`
	IdempotencyKey string          `json:"idempotency_key"`
	Cells          []CellInput     `json:"cells"`
	StylePatch     json.RawMessage `json:"-"`
	// NotePatch sets the note on the listed cells and leaves everything else
	// alone, the way StylePatch does for formatting.
	NotePatch           *string         `json:"-"`
	Border              *BorderCommand  `json:"-"`
	Expected            map[string]Cell `json:"-"`
	OperationType       string          `json:"-"`
	UndoOfOperationID   string          `json:"-"`
	RequireExactVersion bool            `json:"-"`
}

type UndoOperationInput struct {
	OperationID    string `json:"operation_id"`
	ActorID        string `json:"actor_id"`
	ClientID       string `json:"client_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// CellConflictSnapshot is the complete user-editable state of one cell at a
// point in the conflict timeline. Keeping formula and style beside the value
// is important: a formula-only or formatting-only edit must be reviewable and
// restorable in exactly the same way as a literal value edit.
type CellConflictSnapshot struct {
	Value       json.RawMessage `json:"value,omitempty"`
	Formula     string          `json:"formula,omitempty"`
	Style       json.RawMessage `json:"style,omitempty"`
	SpillSource string          `json:"spill_source,omitempty"`
}

type CellConflict struct {
	ID                      string               `json:"id,omitempty"`
	WorkbookID              string               `json:"workbook_id,omitempty"`
	SheetID                 string               `json:"sheet_id,omitempty"`
	OperationID             string               `json:"operation_id,omitempty"`
	Row                     int                  `json:"row"`
	Column                  int                  `json:"column"`
	BaseVersion             int64                `json:"base_version,omitempty"`
	ChangedAtVersion        int64                `json:"changed_at_version"`
	ServerVersion           int64                `json:"server_version,omitempty"`
	ActorID                 string               `json:"actor_id,omitempty"`
	ClientID                string               `json:"client_id,omitempty"`
	ConflictingActorID      string               `json:"conflicting_actor_id,omitempty"`
	BaseCell                CellConflictSnapshot `json:"base_cell"`
	ConflictingCell         CellConflictSnapshot `json:"conflicting_cell"`
	SubmittedCell           CellConflictSnapshot `json:"submitted_cell"`
	AppliedCell             CellConflictSnapshot `json:"applied_cell"`
	CurrentCell             CellConflictSnapshot `json:"current_cell"`
	PreviousValue           json.RawMessage      `json:"previous_value,omitempty"`
	SubmittedValue          json.RawMessage      `json:"submitted_value,omitempty"`
	Status                  string               `json:"status,omitempty"`
	Resolution              string               `json:"resolution,omitempty"`
	Revision                int64                `json:"revision,omitempty"`
	ResolvedBy              string               `json:"resolved_by,omitempty"`
	ResolutionOperationID   string               `json:"resolution_operation_id,omitempty"`
	ResolutionServerVersion int64                `json:"resolution_server_version,omitempty"`
	ResolvedAt              *time.Time           `json:"resolved_at,omitempty"`
	CreatedAt               time.Time            `json:"created_at,omitempty"`
	UpdatedAt               time.Time            `json:"updated_at,omitempty"`
}

type ResolveCellConflictInput struct {
	ActorID          string `json:"-"`
	ClientID         string `json:"client_id,omitempty"`
	IdempotencyKey   string `json:"idempotency_key"`
	ExpectedRevision int64  `json:"expected_revision"`
	Resolution       string `json:"resolution"`
}

type CellConflictResolutionResult struct {
	Conflict  CellConflict   `json:"conflict"`
	Operation MutationResult `json:"operation"`
}

type CellCoordinate struct {
	SheetID string `json:"sheet_id,omitempty"`
	Row     int    `json:"row"`
	Column  int    `json:"column"`
}

type CellFormulaError struct {
	SheetID string `json:"sheet_id,omitempty"`
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
	BackupVersionID    string                `json:"backup_version_id,omitempty"`
	StructuralAxis     string                `json:"structural_axis,omitempty"`
	StructuralAction   string                `json:"structural_action,omitempty"`
	StructuralIndex    int                   `json:"structural_index,omitempty"`
	StructuralCount    int                   `json:"structural_count,omitempty"`
	CreatedAt          time.Time             `json:"created_at"`
}

type StructuralMutation struct {
	SheetID        string `json:"sheet_id"`
	ActorID        string `json:"actor_id"`
	ClientID       string `json:"client_id"`
	BaseVersion    int64  `json:"base_version"`
	IdempotencyKey string `json:"idempotency_key"`
	Axis           string `json:"axis"`
	Action         string `json:"action"`
	Index          int    `json:"index"`
	Count          int    `json:"count"`
	Destination    int    `json:"destination"`
}

// NamedRange is a workbook-level reusable name whose target belongs to one of
// the workbook's sheets. Revision protects concurrent definition edits while
// WorkbookVersion lets realtime clients refresh formula results atomically.
type NamedRange struct {
	ID              string    `json:"id"`
	WorkbookID      string    `json:"workbook_id"`
	WorkbookVersion int64     `json:"workbook_version"`
	SheetID         string    `json:"sheet_id"`
	CreateKey       string    `json:"-"`
	Name            string    `json:"name"`
	Range           string    `json:"range"`
	Revision        int64     `json:"revision"`
	CreatedBy       string    `json:"created_by"`
	UpdatedBy       string    `json:"updated_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateNamedRangeInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	SheetID        string `json:"sheet_id"`
	Range          string `json:"range"`
}

type UpdateNamedRangeInput struct {
	Name             *string `json:"name,omitempty"`
	SheetID          *string `json:"sheet_id,omitempty"`
	Range            *string `json:"range,omitempty"`
	ExpectedRevision *int64  `json:"expected_revision,omitempty"`
}

// ProtectedRange restricts who may write to part of a sheet. Sharing decides
// who can open a workbook; this decides who can change the cells a model
// depends on.
type ProtectedRange struct {
	ID          string    `json:"id"`
	SheetID     string    `json:"sheet_id"`
	CreateKey   string    `json:"-"`
	Range       string    `json:"range"`
	Scope       string    `json:"scope"`
	Exceptions  []string  `json:"exceptions,omitempty"`
	Description string    `json:"description,omitempty"`
	Editors     []string  `json:"editors"`
	WarningOnly bool      `json:"warning_only"`
	Revision    int64     `json:"revision"`
	CreatedBy   string    `json:"created_by"`
	UpdatedBy   string    `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateProtectedRangeInput struct {
	IdempotencyKey string   `json:"idempotency_key"`
	SheetID        string   `json:"sheet_id"`
	Range          string   `json:"range"`
	Scope          string   `json:"scope,omitempty"`
	Exceptions     []string `json:"exceptions,omitempty"`
	Description    string   `json:"description"`
	Editors        []string `json:"editors"`
	WarningOnly    bool     `json:"warning_only"`
}

type UpdateProtectedRangeInput struct {
	ExpectedRevision *int64    `json:"expected_revision,omitempty"`
	Range            *string   `json:"range,omitempty"`
	Scope            *string   `json:"scope,omitempty"`
	Exceptions       *[]string `json:"exceptions,omitempty"`
	Description      *string   `json:"description,omitempty"`
	Editors          *[]string `json:"editors,omitempty"`
	WarningOnly      *bool     `json:"warning_only,omitempty"`
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
