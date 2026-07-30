package workbook

import (
	"context"
	"errors"

	"kanpic/pkg/cellrange"
)

var (
	ErrNotFound      = errors.New("resource not found")
	ErrInvalid       = errors.New("invalid input")
	ErrVersionAhead  = errors.New("base version is newer than the server version")
	ErrDuplicateName = errors.New("name already exists")
)

type Repository interface {
	CreateWorkbook(context.Context, CreateWorkbookInput) (Workbook, error)
	ListWorkbooks(context.Context, string) ([]Workbook, error)
	GetWorkbook(context.Context, string) (Workbook, error)
	UpdateWorkbook(context.Context, string, UpdateWorkbookInput) (Workbook, error)
	DeleteWorkbook(context.Context, string) error

	CreateSheet(context.Context, string, CreateSheetInput) (Sheet, error)
	UpdateSheet(context.Context, string, UpdateSheetInput) (Sheet, error)
	DeleteSheet(context.Context, string) error

	ApplyCells(context.Context, CellMutation) (MutationResult, error)
	ReadRange(context.Context, string, cellrange.Range) ([]Cell, error)

	CreateVersion(context.Context, string, string, string) (Version, error)
	ListVersions(context.Context, string) ([]Version, error)
	RestoreVersion(context.Context, string, string) (MutationResult, error)
}
