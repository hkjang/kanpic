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
	// PrintArea limits printing to one range. Empty means print everything
	// that has content, which is what people expect until they say otherwise.
	// Excel keeps this as the defined name _xlnm.Print_Area, so an imported
	// workbook brings its own along.
	PrintArea string `json:"print_area,omitempty"`
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
	// Range carries the print area for print_area_set.
	Range string `json:"range,omitempty"`
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
	// StructuralDestination is where a moved band landed. It is needed to
	// replay the move onto a write that was addressed before it happened.
	StructuralDestination int `json:"structural_destination,omitempty"`
	// RebasedCells counts the writes whose address had to be moved because
	// somebody changed the sheet's shape first, and DroppedCells names the
	// writes whose row or column no longer exists. Both are zero in the
	// ordinary case and are what lets the client say something happened.
	RebasedCells int              `json:"rebased_cells,omitempty"`
	DroppedCells []CellCoordinate `json:"dropped_cells,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
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
// NamedFunction 은 워크북에 저장해 두고 이름으로 부르는 수식이다. 팀에서
// 쓰는 셈을 한 번 정의해 두면 =마진율(매출, 원가) 처럼 쓸 수 있다.
type NamedFunction struct {
	ID              string    `json:"id"`
	WorkbookID      string    `json:"workbook_id"`
	WorkbookVersion int64     `json:"workbook_version"`
	CreateKey       string    `json:"-"`
	Name            string    `json:"name"`
	Parameters      []string  `json:"parameters"`
	Body            string    `json:"body"`
	Description     string    `json:"description,omitempty"`
	Revision        int64     `json:"revision"`
	CreatedBy       string    `json:"created_by"`
	UpdatedBy       string    `json:"updated_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateNamedFunctionInput struct {
	IdempotencyKey string   `json:"idempotency_key"`
	Name           string   `json:"name"`
	Parameters     []string `json:"parameters"`
	Body           string   `json:"body"`
	Description    string   `json:"description,omitempty"`
}

type UpdateNamedFunctionInput struct {
	Name             *string   `json:"name,omitempty"`
	Parameters       *[]string `json:"parameters,omitempty"`
	Body             *string   `json:"body,omitempty"`
	Description      *string   `json:"description,omitempty"`
	ExpectedRevision *int64    `json:"expected_revision,omitempty"`
}

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

// WorkbookQuery is what the workbook list screen asks for. Without it the list
// returned every workbook a person could open, which at a few thousand meant
// megabytes of JSON and a card for each one.
type WorkbookQuery struct {
	WorkspaceID string
	// Search matches anywhere in the title, case-insensitively. Paging without
	// a way to search would bury a workbook that is not on the first page.
	Search string
	// Filter is "", "favorite", "owned" or "shared". The screen used to apply
	// these in the browser, which only works while it holds every workbook.
	Filter string
	// Limit of zero means no limit, which is what callers that need the whole
	// list - cross-workbook formulas, the MCP tools - still ask for.
	Limit  int
	Offset int
}

type WorkbookPage struct {
	Items   []Workbook `json:"items"`
	Total   int        `json:"total"`
	HasMore bool       `json:"has_more"`
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
	// SourceRange points a list_range rule at the cells that hold its choices.
	SourceRange string `json:"source_range,omitempty"`
	// SourceOptions is what that range currently holds. It is derived on read
	// and never stored, so a dropdown always offers today's list rather than
	// the one that existed when the rule was written.
	SourceOptions []ValidationOption `json:"source_options,omitempty"`
	Value         json.RawMessage    `json:"value,omitempty"`
	Value2        json.RawMessage    `json:"value2,omitempty"`
	Formula       string             `json:"formula,omitempty"`
	AllowBlank    bool               `json:"allow_blank"`
	RejectInput   bool               `json:"reject_input"`
	ShowDropdown  bool               `json:"show_dropdown"`
	DisplayStyle  string             `json:"display_style"`
	HelpText      string             `json:"help_text,omitempty"`
	Revision      int64              `json:"revision"`
	CreatedBy     string             `json:"created_by"`
	UpdatedBy     string             `json:"updated_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type CreateDataValidationInput struct {
	IdempotencyKey string             `json:"idempotency_key"`
	Range          string             `json:"range"`
	RuleType       string             `json:"rule_type"`
	Operator       string             `json:"operator,omitempty"`
	Options        []ValidationOption `json:"options,omitempty"`
	SourceRange    string             `json:"source_range,omitempty"`
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
	SourceRange      *string             `json:"source_range,omitempty"`
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

// SheetDeletion reports what a sheet deletion left behind. Deleting a sheet
// throws away every cell in it and cannot be undone cell by cell, so the only
// way back is the snapshot taken just before.
type SheetDeletion struct {
	WorkbookID      string `json:"workbook_id"`
	SheetName       string `json:"sheet_name"`
	BackupVersionID string `json:"backup_version_id"`
	ServerVersion   int64  `json:"server_version"`
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
	// Validations are the input rules the file carried. They change what the
	// sheet accepts, so dropping them on import quietly widens what people can
	// type into a form somebody designed.
	Validations []ImportValidation `json:"validations,omitempty"`
	// ConditionalFormats are the highlighting rules the file carried. They are
	// how a sheet says which numbers matter, and a table without them looks
	// uniformly unremarkable.
	ConditionalFormats []ImportConditionalFormat `json:"conditional_formats,omitempty"`
	// Charts are the pictures the file drew from this sheet's data. A report
	// that arrives without them looks like a table nobody bothered to explain.
	Charts []ImportChart `json:"charts,omitempty"`
	// Layout carries what the file said about arrangement: hidden rows and
	// columns, sizes, frozen panes and outline groups. An import that drops it
	// flattens a workbook that was exported from here minutes earlier.
	Layout *SheetLayout `json:"layout,omitempty"`
}

// ImportChart is one chart read out of an imported file, reduced to what
// kanpic keeps: the kind of picture, its title, and the one range its series
// came from.
type ImportChart struct {
	Type        string `json:"type"`
	Title       string `json:"title,omitempty"`
	SourceRange string `json:"source_range"`
}

// ImportValidation is one input rule read out of an imported file, in the
// shape the validation rules are normalized from.
type ImportValidation struct {
	Range       string   `json:"range"`
	RuleType    string   `json:"rule_type"`
	Operator    string   `json:"operator,omitempty"`
	Options     []string `json:"options,omitempty"`
	SourceRange string   `json:"source_range,omitempty"`
	Formula     string   `json:"formula,omitempty"`
	Value       string   `json:"value,omitempty"`
	Value2      string   `json:"value2,omitempty"`
	AllowBlank  bool     `json:"allow_blank"`
	RejectInput bool     `json:"reject_input"`
	HelpText    string   `json:"help_text,omitempty"`
}

// ImportConditionalFormat is one highlighting rule read out of a file.
type ImportConditionalFormat struct {
	Range       string          `json:"range"`
	RuleType    string          `json:"rule_type"`
	Operator    string          `json:"operator,omitempty"`
	Formula     string          `json:"formula,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	Value2      json.RawMessage `json:"value2,omitempty"`
	Style       json.RawMessage `json:"style,omitempty"`
	MinColor    string          `json:"min_color,omitempty"`
	MidColor    string          `json:"mid_color,omitempty"`
	MaxColor    string          `json:"max_color,omitempty"`
	BarColor    string          `json:"bar_color,omitempty"`
	IconStyle   string          `json:"icon_style,omitempty"`
	IconReverse bool            `json:"icon_reverse,omitempty"`
	StopIfTrue  bool            `json:"stop_if_true,omitempty"`
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
	// NamedRanges are workbook-scoped, so they arrive beside the sheets rather
	// than inside one. They have to exist before the first recalculation or
	// every formula that uses a name lands as #NAME?.
	NamedRanges []ImportNamedRange `json:"named_ranges,omitempty"`
	// NamedFunctions arrive the same way and for the same reason: a formula
	// that calls one has to find it before the first recalculation.
	NamedFunctions []ImportNamedFunction `json:"named_functions,omitempty"`
}

// ImportNamedFunction is a named formula a file carried. Excel writes these as
// LAMBDA defined names, which have no sheet to belong to.
type ImportNamedFunction struct {
	Name       string   `json:"name"`
	Parameters []string `json:"parameters"`
	Body       string   `json:"body"`
}

// ImportNamedRange is a name a file carried. The file addresses its sheet by
// name, which is the only handle that exists before the import assigns IDs.
type ImportNamedRange struct {
	Name      string `json:"name"`
	SheetName string `json:"sheet_name"`
	Range     string `json:"range"`
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
