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
	OperationID       string             `json:"operation_id"`
	WorkbookID        string             `json:"workbook_id"`
	SheetID           string             `json:"sheet_id"`
	BaseVersion       int64              `json:"base_version"`
	ServerVersion     int64              `json:"server_version"`
	AppliedCells      int                `json:"applied_cells"`
	RecalculatedCells []CellCoordinate   `json:"recalculated_cells"`
	FormulaErrors     []CellFormulaError `json:"formula_errors"`
	Duplicate         bool               `json:"duplicate"`
	Conflicts         []CellConflict     `json:"conflicts"`
	CreatedAt         time.Time          `json:"created_at"`
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
