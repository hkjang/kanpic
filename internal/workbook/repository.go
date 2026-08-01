package workbook

import (
	"context"
	"errors"

	"kanpic/pkg/cellrange"
)

const (
	MaxBatchCells = 1_000
	MaxPasteCells = 10_000
)

var (
	ErrNotFound        = errors.New("resource not found")
	ErrInvalid         = errors.New("invalid input")
	ErrVersionAhead    = errors.New("base version is newer than the server version")
	ErrVersionConflict = errors.New("base version does not match the server version")
	ErrDuplicateName   = errors.New("name already exists")
	ErrValidation      = errors.New("data validation failed")
	ErrRevision        = errors.New("revision conflict")
)

type Repository interface {
	CreateWorkbook(context.Context, CreateWorkbookInput) (Workbook, error)
	ListWorkbooks(context.Context, string) ([]Workbook, error)
	GetWorkbook(context.Context, string) (Workbook, error)
	DuplicateWorkbook(context.Context, string, DuplicateWorkbookInput) (Workbook, error)
	UpdateWorkbook(context.Context, string, UpdateWorkbookInput) (Workbook, error)
	DeleteWorkbook(context.Context, string) error

	CreateSheet(context.Context, string, CreateSheetInput) (Sheet, error)
	DuplicateSheet(context.Context, string, DuplicateSheetInput) (Sheet, error)
	UpdateSheet(context.Context, string, UpdateSheetInput) (Sheet, error)
	DeleteSheet(context.Context, string) error
	CreateFilterView(context.Context, string, string, CreateFilterViewInput) (FilterView, error)
	ListFilterViews(context.Context, string, string) ([]FilterView, error)
	GetFilterView(context.Context, string, string) (FilterView, error)
	UpdateFilterView(context.Context, string, string, UpdateFilterViewInput) (FilterView, error)
	DeleteFilterView(context.Context, string, string) error
	CreateDataValidation(context.Context, string, string, CreateDataValidationInput) (DataValidation, error)
	ListDataValidations(context.Context, string) ([]DataValidation, error)
	GetDataValidation(context.Context, string) (DataValidation, error)
	UpdateDataValidation(context.Context, string, string, UpdateDataValidationInput) (DataValidation, error)
	DeleteDataValidation(context.Context, string, string, *int64) error
	CreateConditionalFormat(context.Context, string, string, CreateConditionalFormatInput) (ConditionalFormat, error)
	ListConditionalFormats(context.Context, string) ([]ConditionalFormat, error)
	GetConditionalFormat(context.Context, string) (ConditionalFormat, error)
	EvaluateConditionalFormats(context.Context, string, cellrange.Range) (ConditionalFormatEvaluation, error)
	UpdateConditionalFormat(context.Context, string, string, UpdateConditionalFormatInput) (ConditionalFormat, error)
	DeleteConditionalFormat(context.Context, string, string, *int64) error
	CreateCommentThread(context.Context, string, string, CreateCommentThreadInput) (CommentThread, error)
	ListCommentThreads(context.Context, string, string, bool) ([]CommentThread, error)
	GetCommentThread(context.Context, string) (CommentThread, error)
	AddCommentReply(context.Context, string, string, CreateCommentReplyInput) (CommentThread, error)
	UpdateCommentMessage(context.Context, string, string, UpdateCommentMessageInput) (CommentThread, error)
	DeleteCommentMessage(context.Context, string, string, int64) (CommentThread, error)
	UpdateCommentThread(context.Context, string, string, UpdateCommentThreadInput) (CommentThread, error)
	DeleteCommentThread(context.Context, string, string) error
	ListMentionNotifications(context.Context, []string, bool, int) ([]MentionNotification, error)
	MarkMentionNotificationRead(context.Context, string, []string) (MentionNotification, error)
	CreateChart(context.Context, string, string, CreateChartInput) (Chart, error)
	ListCharts(context.Context, string, string) ([]Chart, error)
	GetChart(context.Context, string) (Chart, error)
	GetChartData(context.Context, string) (ChartData, error)
	UpdateChart(context.Context, string, string, UpdateChartInput) (Chart, error)
	DeleteChart(context.Context, string, string, *int64) error
	CreatePivot(context.Context, string, string, CreatePivotInput) (Pivot, error)
	ListPivots(context.Context, string, string) ([]Pivot, error)
	GetPivot(context.Context, string) (Pivot, error)
	GetPivotData(context.Context, string) (PivotData, error)
	RefreshPivot(context.Context, string, string) (PivotData, error)
	PivotDrilldown(context.Context, string, PivotDrilldownInput) (PivotDrilldownResult, error)
	UpdatePivot(context.Context, string, string, UpdatePivotInput) (Pivot, error)
	DeletePivot(context.Context, string, string, *int64) error
	CreateNamedRange(context.Context, string, string, CreateNamedRangeInput) (NamedRange, error)
	ListNamedRanges(context.Context, string) ([]NamedRange, error)
	GetNamedRange(context.Context, string) (NamedRange, error)
	UpdateNamedRange(context.Context, string, string, UpdateNamedRangeInput) (NamedRange, error)
	DeleteNamedRange(context.Context, string, string, *int64) error
	ApplyStructure(context.Context, StructuralMutation) (MutationResult, error)
	ApplySheetLayout(context.Context, SheetLayoutMutation) (SheetLayoutResult, error)

	ApplyCells(context.Context, CellMutation) (MutationResult, error)
	UndoOperation(context.Context, UndoOperationInput) (MutationResult, error)
	ListCellConflicts(context.Context, string, bool) ([]CellConflict, error)
	GetCellConflict(context.Context, string) (CellConflict, error)
	ResolveCellConflict(context.Context, string, ResolveCellConflictInput) (CellConflictResolutionResult, error)
	ReadRange(context.Context, string, cellrange.Range) ([]Cell, error)
	SearchWorkbook(context.Context, string, SearchWorkbookInput) (WorkbookSearchResult, error)

	CreateVersion(context.Context, string, string, string) (Version, error)
	ListVersions(context.Context, string) ([]Version, error)
	RestoreVersion(context.Context, string, string) (MutationResult, error)

	ImportWorkbook(context.Context, ImportWorkbookInput) (Workbook, error)
	ReadAllCells(context.Context, string) ([]Cell, error)
}
