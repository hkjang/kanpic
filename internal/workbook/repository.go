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
	CreateNamedRange(context.Context, string, string, CreateNamedRangeInput) (NamedRange, error)
	ListNamedRanges(context.Context, string) ([]NamedRange, error)
	GetNamedRange(context.Context, string) (NamedRange, error)
	UpdateNamedRange(context.Context, string, string, UpdateNamedRangeInput) (NamedRange, error)
	DeleteNamedRange(context.Context, string, string, *int64) error
	ApplyStructure(context.Context, StructuralMutation) (MutationResult, error)
	ApplySheetLayout(context.Context, SheetLayoutMutation) (SheetLayoutResult, error)

	ApplyCells(context.Context, CellMutation) (MutationResult, error)
	UndoOperation(context.Context, UndoOperationInput) (MutationResult, error)
	ReadRange(context.Context, string, cellrange.Range) ([]Cell, error)

	CreateVersion(context.Context, string, string, string) (Version, error)
	ListVersions(context.Context, string) ([]Version, error)
	RestoreVersion(context.Context, string, string) (MutationResult, error)

	ImportWorkbook(context.Context, ImportWorkbookInput) (Workbook, error)
	ReadAllCells(context.Context, string) ([]Cell, error)
}
