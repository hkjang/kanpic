//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	kanpicai "kanpic/internal/ai"
	"kanpic/internal/apikey"
	"kanpic/internal/auth"
	"kanpic/internal/automation"
	"kanpic/internal/database"
	"kanpic/internal/importexport"
	"kanpic/internal/settings"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

type staticAISettings map[string]any

func (values staticAISettings) Values(context.Context) (map[string]any, error) {
	return values, nil
}

type barrierRepository struct {
	workbook.Repository
	reached chan struct{}
	release chan struct{}
}

func (r *barrierRepository) ReadRange(ctx context.Context, sheetID string, selected cellrange.Range) ([]workbook.Cell, error) {
	cells, err := r.Repository.ReadRange(ctx, sheetID, selected)
	if err != nil {
		return nil, err
	}
	r.reached <- struct{}{}
	select {
	case <-r.release:
		return cells, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type ambiguousApplyRepository struct {
	workbook.Repository
	mu       sync.Mutex
	failOnce bool
}

func (r *ambiguousApplyRepository) ApplyCells(ctx context.Context, mutation workbook.CellMutation) (workbook.MutationResult, error) {
	r.mu.Lock()
	fail := r.failOnce
	r.failOnce = false
	r.mu.Unlock()
	result, err := r.Repository.ApplyCells(ctx, mutation)
	if err != nil {
		return workbook.MutationResult{}, err
	}
	if fail {
		return workbook.MutationResult{}, context.DeadlineExceeded
	}
	return result, nil
}

type firstApplyBarrierRepository struct {
	workbook.Repository
	once    sync.Once
	reached chan struct{}
	release chan struct{}
}

func (r *firstApplyBarrierRepository) ApplyCells(ctx context.Context, mutation workbook.CellMutation) (workbook.MutationResult, error) {
	blocked := false
	r.once.Do(func() {
		blocked = true
		close(r.reached)
	})
	if blocked {
		select {
		case <-r.release:
		case <-ctx.Done():
			return workbook.MutationResult{}, ctx.Err()
		}
	}
	return r.Repository.ApplyCells(ctx, mutation)
}

func TestPostgresDurabilityFlow(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "integration durability", WorkspaceID: "integration", OwnerID: "test-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), wb.ID, "integration-cleanup")
	sheetID := wb.Sheets[0].ID
	first, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "test-user", ClientID: "integration", BaseVersion: 1, IdempotencyKey: "first", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`10`)}}})
	if err != nil || first.ServerVersion != 2 {
		t.Fatalf("first mutation: %#v %v", first, err)
	}
	duplicate, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "test-user", ClientID: "integration", BaseVersion: 1, IdempotencyKey: "first", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`99`)}}})
	if err != nil || !duplicate.Duplicate || duplicate.ServerVersion != 2 {
		t.Fatalf("duplicate: %#v %v", duplicate, err)
	}
	version, err := repository.CreateVersion(ctx, wb.ID, "before conflict", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "other-user", ClientID: "other", BaseVersion: 1, IdempotencyKey: "stale", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`20`)}}})
	if err != nil || len(conflict.Conflicts) != 1 {
		t.Fatalf("conflict: %#v %v", conflict, err)
	}
	restored, err := repository.RestoreVersion(ctx, version.ID, "test-user")
	if err != nil || restored.ServerVersion != 4 {
		t.Fatalf("restore: %#v %v", restored, err)
	}
	selected, _ := cellrange.Parse("A1")
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "10" {
		t.Fatalf("restored cells: %#v %v", cells, err)
	}
	formulaResult, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "test-user", ClientID: "integration", BaseVersion: 4, IdempotencyKey: "formula", Cells: []workbook.CellInput{{Row: 2, Column: 1, Formula: "=A1*2"}}})
	if err != nil || len(formulaResult.RecalculatedCells) != 1 {
		t.Fatalf("formula mutation: %#v %v", formulaResult, err)
	}
	recalculated, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "test-user", ClientID: "integration", BaseVersion: 5, IdempotencyKey: "formula-source", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`7`)}}})
	if err != nil || recalculated.AppliedCells != 1 || len(recalculated.RecalculatedCells) != 1 {
		t.Fatalf("dependent recalculation: %#v %v", recalculated, err)
	}
	selected, _ = cellrange.Parse("A2")
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "14" {
		t.Fatalf("recalculated formula: %#v %v", cells, err)
	}
	undone, err := repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: recalculated.OperationID, ActorID: "test-user", ClientID: "integration", IdempotencyKey: "undo-formula-source"})
	if err != nil || undone.ServerVersion != 7 || undone.AppliedCells != 1 || len(undone.RecalculatedCells) != 1 {
		t.Fatalf("PostgreSQL undo: %#v %v", undone, err)
	}
	selected, _ = cellrange.Parse("A1:A2")
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 2 || string(cells[0].Value) != "10" || string(cells[1].Value) != "20" {
		t.Fatalf("undone values: %#v %v", cells, err)
	}
	redone, err := repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: undone.OperationID, ActorID: "test-user", ClientID: "integration", IdempotencyKey: "redo-formula-source"})
	if err != nil || redone.ServerVersion != 8 || redone.AppliedCells != 1 {
		t.Fatalf("PostgreSQL redo: %#v %v", redone, err)
	}
	_, err = repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "other-user", ClientID: "other", BaseVersion: 8, IdempotencyKey: "after-redo", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`9`)}}})
	if err != nil {
		t.Fatal(err)
	}
	selective, err := repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: recalculated.OperationID, ActorID: "test-user", ClientID: "integration", IdempotencyKey: "selective-conflict"})
	if err != nil || selective.AppliedCells != 0 || len(selective.Conflicts) != 1 || selective.ServerVersion != 10 {
		t.Fatalf("PostgreSQL selective undo: %#v %v", selective, err)
	}
	selected, _ = cellrange.Parse("A1")
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "9" {
		t.Fatalf("selective undo overwrote later value: %#v %v", cells, err)
	}
	largePaste := make([]workbook.CellInput, workbook.MaxBatchCells+1)
	for index := range largePaste {
		largePaste[index] = workbook.CellInput{Row: index + 1, Column: 2, Value: json.RawMessage(`1`)}
	}
	pasted, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "test-user", ClientID: "integration", BaseVersion: 10, IdempotencyKey: "large-paste", OperationType: "cells.paste", Cells: largePaste})
	if err != nil || pasted.AppliedCells != workbook.MaxBatchCells+1 || pasted.ServerVersion != 11 {
		t.Fatalf("atomic large paste: %#v %v", pasted, err)
	}
	selected, _ = cellrange.Parse("B1001")
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "1" {
		t.Fatalf("last large paste cell: %#v %v", cells, err)
	}
}

func TestPostgresCellConflictPersistsAndResolutionIsVersioned(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres conflicts", WorkspaceID: "integration", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	_, err = repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "alice", ClientID: "a", BaseVersion: 1, IdempotencyKey: "pg-conflict-first", Cells: []workbook.CellInput{{Row: 3, Column: 2, Value: json.RawMessage(`"first"`), Style: json.RawMessage(`{"bold":true}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "bob", ClientID: "b", BaseVersion: 1, IdempotencyKey: "pg-conflict-stale", Cells: []workbook.CellInput{{Row: 3, Column: 2, Value: json.RawMessage(`"second"`), Style: json.RawMessage(`{"italic":true}`)}}})
	if err != nil || len(stale.Conflicts) != 1 {
		t.Fatalf("stale mutation: %#v %v", stale, err)
	}
	conflict := stale.Conflicts[0]
	if conflict.ID == "" || conflict.ConflictingActorID != "alice" || string(conflict.ConflictingCell.Value) != `"first"` || string(conflict.AppliedCell.Value) != `"second"` {
		t.Fatalf("persisted conflict payload: %#v", conflict)
	}
	// A fresh repository instance proves the comparison record is not an
	// in-memory WebSocket artifact.
	restarted := workbook.NewPostgresRepository(pool)
	items, err := restarted.ListCellConflicts(ctx, book.ID, false)
	if err != nil || len(items) != 1 || items[0].ID != conflict.ID || string(items[0].CurrentCell.Value) != `"second"` {
		t.Fatalf("reloaded conflicts: %#v %v", items, err)
	}
	resolved, err := restarted.ResolveCellConflict(ctx, conflict.ID, workbook.ResolveCellConflictInput{ActorID: "bob", ClientID: "b", IdempotencyKey: "pg-conflict-restore", ExpectedRevision: 1, Resolution: workbook.ConflictRestorePrevious})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Conflict.Status != workbook.ConflictStatusResolved || resolved.Conflict.ResolutionOperationID != resolved.Operation.OperationID || resolved.Operation.ServerVersion != 4 {
		t.Fatalf("resolved conflict: %#v", resolved)
	}
	selected, _ := cellrange.Parse("B3")
	cells, err := restarted.ReadRange(ctx, sheetID, selected)
	var restoredStyle map[string]any
	if len(cells) == 1 {
		_ = json.Unmarshal(cells[0].Style, &restoredStyle)
	}
	if err != nil || len(cells) != 1 || string(cells[0].Value) != `"first"` || restoredStyle["bold"] != true {
		t.Fatalf("restored previous server cell: %#v %v", cells, err)
	}
	open, err := restarted.ListCellConflicts(ctx, book.ID, false)
	if err != nil || len(open) != 0 {
		t.Fatalf("open conflicts after resolution: %#v %v", open, err)
	}
	history, err := restarted.ListCellConflicts(ctx, book.ID, true)
	if err != nil || len(history) != 1 || history[0].Revision != 2 || history[0].ResolutionServerVersion != 4 {
		t.Fatalf("resolved conflict history: %#v %v", history, err)
	}

	// Simulate a process interruption after the versioned restore operation
	// committed but before the conflict row was marked resolved. Retrying the
	// same idempotency key must finish the record instead of rejecting because
	// the current cell now differs from the originally applied stale value.
	_, err = restarted.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "alice", ClientID: "a", BaseVersion: 4, IdempotencyKey: "pg-recovery-first", Cells: []workbook.CellInput{{Row: 5, Column: 4, Value: json.RawMessage(`10`)}}})
	if err != nil {
		t.Fatal(err)
	}
	secondConflict, err := restarted.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "bob", ClientID: "b", BaseVersion: 4, IdempotencyKey: "pg-recovery-stale", Cells: []workbook.CellInput{{Row: 5, Column: 4, Value: json.RawMessage(`20`)}}})
	if err != nil || len(secondConflict.Conflicts) != 1 {
		t.Fatalf("second conflict: %#v %v", secondConflict, err)
	}
	crashWindowOperation, err := restarted.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "bob", ClientID: "b", BaseVersion: 6, IdempotencyKey: "pg-recovery-resolve", Cells: []workbook.CellInput{{Row: 5, Column: 4, Value: json.RawMessage(`10`)}}, OperationType: "conflict.resolve.restore_previous"})
	if err != nil || crashWindowOperation.ServerVersion != 7 {
		t.Fatalf("simulated pre-status resolution: %#v %v", crashWindowOperation, err)
	}
	recovered, err := restarted.ResolveCellConflict(ctx, secondConflict.Conflicts[0].ID, workbook.ResolveCellConflictInput{ActorID: "bob", ClientID: "b", IdempotencyKey: "pg-recovery-resolve", ExpectedRevision: 1, Resolution: workbook.ConflictRestorePrevious})
	if err != nil || !recovered.Operation.Duplicate || recovered.Conflict.Status != workbook.ConflictStatusResolved || recovered.Conflict.ResolutionOperationID != crashWindowOperation.OperationID {
		t.Fatalf("idempotent crash-window recovery: %#v %v", recovered, err)
	}
}

func TestPostgresWorkbookSearchUsesCellBlocks(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres search", WorkspaceID: "integration", OwnerID: "search-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	second, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "검색 결과"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: book.Sheets[0].ID, ActorID: "search-user", BaseVersion: 2, IdempotencyKey: "pg-search-one", Cells: []workbook.CellInput{{Row: 10, Column: 2, Value: json.RawMessage(`"Alpha 매출"`)}, {Row: 11, Column: 2, Value: json.RawMessage(`42`)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: second.ID, ActorID: "search-user", BaseVersion: 3, IdempotencyKey: "pg-search-two", Cells: []workbook.CellInput{{Row: 4, Column: 1, Formula: `=CONCAT("ALPHA", " 합계")`}}}); err != nil {
		t.Fatal(err)
	}
	first, err := repository.SearchWorkbook(ctx, book.ID, workbook.SearchWorkbookInput{Query: "alpha", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].Address != "B10" || first.NextOffset == nil {
		t.Fatalf("PostgreSQL first search = %#v, %v", first, err)
	}
	secondPage, err := repository.SearchWorkbook(ctx, book.ID, workbook.SearchWorkbookInput{Query: "alpha", Limit: 1, Offset: *first.NextOffset})
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].SheetID != second.ID || secondPage.Items[0].Address != "A4" || len(secondPage.Items[0].MatchedFields) != 2 || secondPage.Items[0].MatchedFields[1] != "formula" || secondPage.NextOffset != nil {
		t.Fatalf("PostgreSQL second search = %#v, %v", secondPage, err)
	}
	number, err := repository.SearchWorkbook(ctx, book.ID, workbook.SearchWorkbookInput{Query: "42"})
	if err != nil || len(number.Items) != 1 || number.Items[0].Address != "B11" {
		t.Fatalf("PostgreSQL numeric search = %#v, %v", number, err)
	}
}

func TestPostgresChartsPersistRefreshAndRestore(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres charts", WorkspaceID: "integration", OwnerID: "chart-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: "chart-user", BaseVersion: book.Version, IdempotencyKey: "pg-chart-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"월"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"1월"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`10`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"2월"`)}, {Row: 3, Column: 2, Formula: `=B2*2`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	chart, err := repository.CreateChart(ctx, book.ID, "chart-user", workbook.CreateChartInput{IdempotencyKey: "pg-chart", SheetID: sheet.ID, SourceSheetID: sheet.ID, Type: "line", Title: "실적", SourceRange: "A1:B3"})
	if err != nil || chart.WorkbookVersion != seed.ServerVersion+1 {
		t.Fatalf("PostgreSQL chart create = %#v, %v", chart, err)
	}
	duplicate, err := repository.CreateChart(ctx, book.ID, "chart-user", workbook.CreateChartInput{IdempotencyKey: "pg-chart", SheetID: sheet.ID, SourceSheetID: sheet.ID, Type: "pie", SourceRange: "A1:B2"})
	if err != nil || duplicate.ID != chart.ID || duplicate.Type != "line" {
		t.Fatalf("PostgreSQL chart idempotency = %#v, %v", duplicate, err)
	}
	data, err := repository.GetChartData(ctx, chart.ID)
	if err != nil || len(data.Series) != 1 || len(data.Series[0].Points) != 2 || data.Series[0].Points[1].Value == nil || *data.Series[0].Points[1].Value != 20 {
		t.Fatalf("PostgreSQL chart data = %#v, %v", data, err)
	}
	inserted, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: sheet.ID, ActorID: "chart-user", BaseVersion: chart.WorkbookVersion, IdempotencyKey: "pg-chart-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := repository.GetChart(ctx, chart.ID)
	if err != nil || moved.SourceRange != "A1:B4" || moved.Revision != 2 {
		t.Fatalf("PostgreSQL moved chart = %#v, %v", moved, err)
	}
	version, err := repository.CreateVersion(ctx, book.ID, "chart snapshot", "chart-user")
	if err != nil {
		t.Fatal(err)
	}
	title := "changed"
	changed, err := repository.UpdateChart(ctx, chart.ID, "chart-user", workbook.UpdateChartInput{Title: &title, ExpectedRevision: &moved.Revision})
	if err != nil || changed.WorkbookVersion != inserted.ServerVersion+1 {
		t.Fatalf("PostgreSQL chart update = %#v, %v", changed, err)
	}
	if _, err := repository.RestoreVersion(ctx, version.ID, "chart-user"); err != nil {
		t.Fatal(err)
	}
	restored, err := repository.GetChart(ctx, chart.ID)
	if err != nil || restored.Title != "실적" || restored.SourceRange != "A1:B4" || restored.Revision != 2 {
		t.Fatalf("PostgreSQL restored chart = %#v, %v", restored, err)
	}
	copiedBook, err := repository.DuplicateWorkbook(ctx, book.ID, workbook.DuplicateWorkbookInput{OwnerID: "copy-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), copiedBook.ID, "integration-cleanup")
	copiedCharts, err := repository.ListCharts(ctx, copiedBook.ID, "")
	if err != nil || len(copiedCharts) != 1 || copiedCharts[0].SheetID != copiedCharts[0].SourceSheetID || copiedCharts[0].SheetID == sheet.ID {
		t.Fatalf("PostgreSQL copied charts = %#v, %v", copiedCharts, err)
	}
}

func TestPostgresPivotsPersistRefreshDrilldownAndRestore(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres pivots", WorkspaceID: "integration", OwnerID: "pivot-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	placement := book.Sheets[0]
	source, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "Data"})
	if err != nil {
		t.Fatal(err)
	}
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: source.ID, ActorID: "pivot-user", BaseVersion: 2, IdempotencyKey: "pg-pivot-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"지역"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"월"`)}, {Row: 1, Column: 3, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"동부"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`"2026-01-01"`)}, {Row: 2, Column: 3, Value: json.RawMessage(`100`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"동부"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`"2026-02-01"`)}, {Row: 3, Column: 3, Value: json.RawMessage(`200`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"서부"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`"2026-01-01"`)}, {Row: 4, Column: 3, Value: json.RawMessage(`300`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pivot, err := repository.CreatePivot(ctx, book.ID, "pivot-user", workbook.CreatePivotInput{
		IdempotencyKey: "pg-pivot", SheetID: placement.ID, SourceSheetID: source.ID, Name: "지역별 월 매출", SourceRange: "A1:C4", RefreshMode: "manual",
		Rows: []workbook.PivotDimension{{Column: 1, Name: "지역"}}, Columns: []workbook.PivotDimension{{Column: 2, Name: "월", Group: "month"}}, Values: []workbook.PivotValueField{{Column: 3, Name: "매출", Aggregation: "sum"}},
	})
	if err != nil || pivot.WorkbookVersion != seed.ServerVersion+1 {
		t.Fatalf("PostgreSQL pivot create = %#v, %v", pivot, err)
	}
	duplicate, err := repository.CreatePivot(ctx, book.ID, "pivot-user", workbook.CreatePivotInput{IdempotencyKey: "pg-pivot", SheetID: placement.ID, SourceSheetID: source.ID, Name: "duplicate", SourceRange: "A1:C2", Values: []workbook.PivotValueField{{Column: 3, Aggregation: "count"}}})
	if err != nil || duplicate.ID != pivot.ID || duplicate.Name != pivot.Name {
		t.Fatalf("PostgreSQL pivot idempotency = %#v, %v", duplicate, err)
	}
	data, err := repository.GetPivotData(ctx, pivot.ID)
	if err != nil || !data.Cached || data.SourceRowCount != 3 || len(data.Rows) != 2 || len(data.Columns) != 2 {
		t.Fatalf("PostgreSQL pivot data = %#v, %v", data, err)
	}
	drill, err := repository.PivotDrilldown(ctx, pivot.ID, workbook.PivotDrilldownInput{RowKey: data.Rows[0].Key, ColumnKey: data.Columns[0].Key, Limit: 10})
	if err != nil || drill.Total != 1 || len(drill.Rows) != 1 || drill.Rows[0].SourceRow != 2 {
		t.Fatalf("PostgreSQL pivot drilldown = %#v, %v", drill, err)
	}
	latest, _ := repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: source.ID, ActorID: "pivot-user", BaseVersion: latest.Version, IdempotencyKey: "pg-pivot-change", Cells: []workbook.CellInput{{Row: 2, Column: 3, Value: json.RawMessage(`150`)}}}); err != nil {
		t.Fatal(err)
	}
	stale, err := repository.GetPivotData(ctx, pivot.ID)
	if err != nil || stale.Rows[0].Values[0] != float64(100) {
		t.Fatalf("PostgreSQL manual pivot cache = %#v, %v", stale, err)
	}
	refreshed, err := repository.RefreshPivot(ctx, pivot.ID, "pivot-user")
	if err != nil || refreshed.Rows[0].Values[0] != float64(150) {
		t.Fatalf("PostgreSQL pivot refresh = %#v, %v", refreshed, err)
	}
	inserted, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: source.ID, ActorID: "pivot-user", BaseVersion: refreshed.WorkbookVersion, IdempotencyKey: "pg-pivot-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := repository.GetPivot(ctx, pivot.ID)
	if err != nil || moved.SourceRange != "A1:C5" || moved.Revision != 2 || moved.LastRefreshedAt != nil {
		t.Fatalf("PostgreSQL moved pivot = %#v, %v", moved, err)
	}
	version, err := repository.CreateVersion(ctx, book.ID, "pivot snapshot", "pivot-user")
	if err != nil {
		t.Fatal(err)
	}
	name := "changed"
	if _, err := repository.UpdatePivot(ctx, pivot.ID, "pivot-user", workbook.UpdatePivotInput{Name: &name, ExpectedRevision: &moved.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RestoreVersion(ctx, version.ID, "pivot-user"); err != nil {
		t.Fatal(err)
	}
	restored, err := repository.GetPivot(ctx, pivot.ID)
	if err != nil || restored.Name != "지역별 월 매출" || restored.SourceRange != "A1:C5" || restored.Revision != moved.Revision {
		t.Fatalf("PostgreSQL restored pivot = %#v, %v", restored, err)
	}
	copiedBook, err := repository.DuplicateWorkbook(ctx, book.ID, workbook.DuplicateWorkbookInput{OwnerID: "copy-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), copiedBook.ID, "integration-cleanup")
	copiedPivots, err := repository.ListPivots(ctx, copiedBook.ID, "")
	if err != nil || len(copiedPivots) != 1 || copiedPivots[0].WorkbookID != copiedBook.ID || copiedPivots[0].SheetID == placement.ID || copiedPivots[0].SourceSheetID == source.ID {
		t.Fatalf("PostgreSQL copied pivots = %#v, %v", copiedPivots, err)
	}
	_ = inserted
}

func TestPostgresCommentsPersistMentionsAndFollowStructure(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres comments", WorkspaceID: "integration", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	bobRecipient := fmt.Sprintf("bob-%s@example.com", book.ID)
	danaRecipient := fmt.Sprintf("dana-%s@example.com", book.ID)
	erinRecipient := fmt.Sprintf("erin-%s@example.com", book.ID)
	sheet := book.Sheets[0]
	thread, err := repository.CreateCommentThread(ctx, book.ID, "alice", workbook.CreateCommentThreadInput{IdempotencyKey: "pg-comment", SheetID: sheet.ID, Range: "B2:C3", Content: "@" + bobRecipient + " 확인 부탁드립니다"})
	if err != nil || len(thread.Messages) != 1 || thread.Messages[0].AuthorID != "alice" || thread.Messages[0].Revision != 1 {
		t.Fatalf("PostgreSQL comment create = %#v, %v", thread, err)
	}
	duplicate, err := repository.CreateCommentThread(ctx, book.ID, "alice", workbook.CreateCommentThreadInput{IdempotencyKey: "pg-comment", SheetID: sheet.ID, Range: "A1", Content: "ignored"})
	if err != nil || duplicate.ID != thread.ID || duplicate.Messages[0].Content != thread.Messages[0].Content {
		t.Fatalf("PostgreSQL comment idempotency = %#v, %v", duplicate, err)
	}
	replied, err := repository.AddCommentReply(ctx, thread.ID, "charlie", workbook.CreateCommentReplyInput{IdempotencyKey: "pg-reply", Content: "처리 중입니다 @" + danaRecipient})
	if err != nil || replied.Revision != 2 || len(replied.Messages) != 2 || replied.Messages[1].AuthorID != "charlie" {
		t.Fatalf("PostgreSQL reply = %#v, %v", replied, err)
	}
	listed, err := repository.ListCommentThreads(ctx, book.ID, sheet.ID, false)
	if err != nil || len(listed) != 1 || len(listed[0].Messages) != 2 {
		t.Fatalf("PostgreSQL comment list = %#v, %v", listed, err)
	}
	bob, err := repository.ListMentionNotifications(ctx, []string{bobRecipient}, true, 50)
	if err != nil || len(bob) != 1 || bob[0].ThreadID != thread.ID || bob[0].Range != "B2:C3" {
		t.Fatalf("PostgreSQL mention list = %#v, %v", bob, err)
	}
	read, err := repository.MarkMentionNotificationRead(ctx, bob[0].ID, []string{bobRecipient})
	if err != nil || read.ReadAt == nil || read.SheetName != sheet.Name {
		t.Fatalf("PostgreSQL mention read = %#v, %v", read, err)
	}
	updated, err := repository.UpdateCommentMessage(ctx, thread.Messages[0].ID, "alice", workbook.UpdateCommentMessageInput{Content: "담당자 변경 @" + erinRecipient, ExpectedRevision: thread.Messages[0].Revision})
	if err != nil || updated.Revision != 3 || updated.Messages[0].Revision != 2 {
		t.Fatalf("PostgreSQL message update = %#v, %v", updated, err)
	}
	if old, err := repository.ListMentionNotifications(ctx, []string{bobRecipient}, false, 50); err != nil || len(old) != 0 {
		t.Fatalf("PostgreSQL stale mention = %#v, %v", old, err)
	}
	resolvedValue := true
	resolved, err := repository.UpdateCommentThread(ctx, thread.ID, "alice", workbook.UpdateCommentThreadInput{Resolved: &resolvedValue, ExpectedRevision: updated.Revision})
	if err != nil || !resolved.Resolved || resolved.ResolvedAt == nil || resolved.Revision != 4 {
		t.Fatalf("PostgreSQL resolve = %#v, %v", resolved, err)
	}
	if active, err := repository.ListCommentThreads(ctx, book.ID, "", false); err != nil || len(active) != 0 {
		t.Fatalf("PostgreSQL active comments = %#v, %v", active, err)
	}
	inserted, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "pg-comment-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := repository.GetCommentThread(ctx, thread.ID)
	if err != nil || moved.Range != "B3:C4" || moved.Revision != 5 {
		t.Fatalf("PostgreSQL moved comment = %#v, %v", moved, err)
	}
	erin, err := repository.ListMentionNotifications(ctx, []string{erinRecipient}, false, 50)
	if err != nil || len(erin) != 1 || erin[0].Range != "B3:C4" {
		t.Fatalf("PostgreSQL moved mention = %#v, %v", erin, err)
	}
	if _, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: inserted.ServerVersion, IdempotencyKey: "pg-comment-delete", Axis: "row", Action: "delete", Index: 3, Count: 2}); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.GetCommentThread(ctx, thread.ID)
	if err != nil || removed.Range != "#REF!" || removed.Revision != 6 {
		t.Fatalf("PostgreSQL deleted comment anchor = %#v, %v", removed, err)
	}
	if err := repository.DeleteWorkbook(ctx, book.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if hidden, err := repository.ListMentionNotifications(ctx, []string{erinRecipient}, false, 50); err != nil || len(hidden) != 0 {
		t.Fatalf("deleted workbook notifications remain visible = %#v, %v", hidden, err)
	}
	if _, err := repository.GetCommentThread(ctx, thread.ID); !errors.Is(err, workbook.ErrNotFound) {
		t.Fatalf("deleted workbook comment remains accessible: %v", err)
	}
}

func TestPostgresRangeFormattingPreservesContentAndUndo(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres formatting", WorkspaceID: "integration", OwnerID: "format-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "format-user", BaseVersion: 1, IdempotencyKey: "postgres-format-seed", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`5`)}, {Row: 2, Column: 1, Formula: "=A1*2"}}}); err != nil {
		t.Fatal(err)
	}
	formatted, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "format-user", BaseVersion: 2, IdempotencyKey: "postgres-format-range", OperationType: "range.format", StylePatch: json.RawMessage(`{"bold":true,"background":"#fef3c7","horizontal_align":"center"}`), Cells: []workbook.CellInput{{Row: 1, Column: 1}, {Row: 2, Column: 1}, {Row: 3, Column: 1}}})
	if err != nil || formatted.ServerVersion != 3 || formatted.AppliedCells != 3 || len(formatted.RecalculatedCells) != 0 {
		t.Fatalf("format result: %#v, %v", formatted, err)
	}
	duplicate, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "format-user", BaseVersion: 2, IdempotencyKey: "postgres-format-range", OperationType: "range.format", StylePatch: json.RawMessage(`{"bold":false}`), Cells: []workbook.CellInput{{Row: 1, Column: 1}}})
	if err != nil || !duplicate.Duplicate || duplicate.ServerVersion != 3 {
		t.Fatalf("format duplicate: %#v, %v", duplicate, err)
	}
	selected, _ := cellrange.Parse("A1:A3")
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 3 || string(cells[0].Value) != "5" || cells[1].Formula != "=A1*2" || string(cells[1].Value) != "10" {
		t.Fatalf("formatted content: %#v, %v", cells, err)
	}
	for _, cell := range cells {
		var style map[string]any
		if json.Unmarshal(cell.Style, &style) != nil || style["bold"] != true || style["background"] != "#fef3c7" || style["horizontal_align"] != "center" {
			t.Fatalf("formatted style: %s", cell.Style)
		}
	}
	undone, err := repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: formatted.OperationID, ActorID: "format-user", IdempotencyKey: "postgres-format-undo"})
	if err != nil || undone.ServerVersion != 4 || undone.AppliedCells != 3 {
		t.Fatalf("format undo: %#v, %v", undone, err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 2 || len(cells[0].Style) != 0 || len(cells[1].Style) != 0 || string(cells[0].Value) != "5" || cells[1].Formula != "=A1*2" {
		t.Fatalf("content after format undo: %#v, %v", cells, err)
	}
	border := &workbook.BorderCommand{Preset: "outer", Style: "double", Color: "#0f766e", StartRow: 1, StartColumn: 2, EndRow: 2, EndColumn: 3}
	bordered, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "format-user", BaseVersion: 4, IdempotencyKey: "postgres-border-range", OperationType: "range.format", Border: border, Cells: []workbook.CellInput{{Row: 1, Column: 2}, {Row: 1, Column: 3}, {Row: 2, Column: 2}, {Row: 2, Column: 3}}})
	if err != nil || bordered.ServerVersion != 5 || bordered.AppliedCells != 4 {
		t.Fatalf("border result: %#v, %v", bordered, err)
	}
	borderRange, _ := cellrange.Parse("B1:C2")
	borderCells, err := repository.ReadRange(ctx, sheetID, borderRange)
	if err != nil || len(borderCells) != 4 {
		t.Fatalf("border cells: %#v, %v", borderCells, err)
	}
	for _, cell := range borderCells {
		var style struct {
			Borders map[string]workbook.BorderSide `json:"borders"`
		}
		if json.Unmarshal(cell.Style, &style) != nil || len(style.Borders) != 2 {
			t.Fatalf("border style %d:%d: %s", cell.Row, cell.Column, cell.Style)
		}
	}
}

func TestPostgresMergedRangePersistsAndUndoRestoresContent(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres merge", WorkspaceID: "integration", OwnerID: "merge-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "merge-user", BaseVersion: 1, IdempotencyKey: "postgres-merge-seed", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`"title"`), Style: json.RawMessage(`{"bold":true}`)}, {Row: 2, Column: 2, Value: json.RawMessage(`9`)}}}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A1:B2")
	existing, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := workbook.BuildMergeCells(existing, selected, true)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "merge-user", BaseVersion: 2, IdempotencyKey: "postgres-merge", OperationType: "range.merge", Cells: inputs})
	if err != nil || merged.ServerVersion != 3 || merged.AppliedCells != 4 {
		t.Fatalf("merge result: %#v, %v", merged, err)
	}
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 4 || string(cells[0].Value) != `"title"` || string(cells[3].Value) != "9" {
		t.Fatalf("persisted merged cells: %#v, %v", cells, err)
	}
	for _, cell := range cells {
		if metadata, exists, err := workbook.CellMerge(cell); err != nil || !exists || metadata.EndRow != 2 || metadata.EndColumn != 2 {
			t.Fatalf("persisted merge metadata: cell=%#v metadata=%#v exists=%v err=%v", cell, metadata, exists, err)
		}
	}
	undone, err := repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: merged.OperationID, ActorID: "merge-user", IdempotencyKey: "postgres-merge-undo"})
	if err != nil || undone.ServerVersion != 4 || undone.AppliedCells != 4 {
		t.Fatalf("merge undo: %#v, %v", undone, err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 2 || string(cells[0].Value) != `"title"` || string(cells[1].Value) != "9" {
		t.Fatalf("content after merge undo: %#v, %v", cells, err)
	}
	for _, cell := range cells {
		if _, exists, err := workbook.CellMerge(cell); err != nil || exists {
			t.Fatalf("merge metadata remained after undo: cell=%#v exists=%v err=%v", cell, exists, err)
		}
	}
}

func TestPostgresRangeSortMovesFormulasStylesAndUndoRestoresRows(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres range sort", WorkspaceID: "integration", OwnerID: "sort-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	seed := []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"Name"`)},
		{Row: 1, Column: 2, Value: json.RawMessage(`"Quantity"`)},
		{Row: 1, Column: 3, Value: json.RawMessage(`"Total"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"beta"`), Style: json.RawMessage(`{"bold":true}`)},
		{Row: 2, Column: 2, Value: json.RawMessage(`2`)},
		{Row: 2, Column: 3, Formula: "=B2*2"},
		{Row: 3, Column: 1, Value: json.RawMessage(`"Alpha"`)},
		{Row: 3, Column: 2, Value: json.RawMessage(`10`)},
		{Row: 3, Column: 3, Formula: "=B3*2"},
		{Row: 4, Column: 1, Value: json.RawMessage(`"alpha"`)},
		{Row: 4, Column: 2, Value: json.RawMessage(`5`)},
		{Row: 4, Column: 3, Formula: "=B4*2"},
	}
	seeded, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "sort-user", BaseVersion: 1, IdempotencyKey: "postgres-sort-seed", Cells: seed})
	if err != nil || seeded.ServerVersion != 2 || seeded.AppliedCells != len(seed) {
		t.Fatalf("sort seed: %#v, %v", seeded, err)
	}
	selected, _ := cellrange.Parse("A1:C4")
	existing, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := workbook.BuildSortCells(existing, selected, workbook.SortOptions{
		HeaderRows: 1,
		Keys: []workbook.SortKey{
			{Column: 1, Direction: "asc"},
			{Column: 2, Direction: "desc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sorted, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "sort-user", BaseVersion: 2, IdempotencyKey: "postgres-range-sort", OperationType: "range.sort", Cells: inputs})
	if err != nil || sorted.ServerVersion != 3 || sorted.AppliedCells != 9 {
		t.Fatalf("sort result: %#v, %v", sorted, err)
	}
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 12 {
		t.Fatalf("sorted range: %#v, %v", cells, err)
	}
	assertSortedRow := func(row int, name string, quantity, total int, formulaText string, bold bool) {
		t.Helper()
		byColumn := make(map[int]workbook.Cell, 3)
		for _, cell := range cells {
			if cell.Row == row {
				byColumn[cell.Column] = cell
			}
		}
		if string(byColumn[1].Value) != `"`+name+`"` || string(byColumn[2].Value) != fmt.Sprint(quantity) || string(byColumn[3].Value) != fmt.Sprint(total) || byColumn[3].Formula != formulaText {
			t.Fatalf("sorted row %d: %#v", row, byColumn)
		}
		var style map[string]any
		_ = json.Unmarshal(byColumn[1].Style, &style)
		if (style["bold"] == true) != bold {
			t.Fatalf("sorted row %d style: %s", row, byColumn[1].Style)
		}
	}
	assertSortedRow(2, "Alpha", 10, 20, "=B2*2", false)
	assertSortedRow(3, "alpha", 5, 10, "=B3*2", false)
	assertSortedRow(4, "beta", 2, 4, "=B4*2", true)

	undone, err := repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: sorted.OperationID, ActorID: "sort-user", IdempotencyKey: "postgres-range-sort-undo"})
	if err != nil || undone.ServerVersion != 4 || undone.AppliedCells != 9 {
		t.Fatalf("sort undo: %#v, %v", undone, err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 12 {
		t.Fatalf("range after sort undo: %#v, %v", cells, err)
	}
	assertSortedRow(2, "beta", 2, 4, "=B2*2", true)
	assertSortedRow(3, "Alpha", 10, 20, "=B3*2", false)
	assertSortedRow(4, "alpha", 5, 10, "=B4*2", false)
}

func TestPostgresFilterViewsPersistActorIsolationActivationAndLatestEvaluation(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres filters", WorkspaceID: "integration", OwnerID: "filter-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	seed := []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"Name"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"Amount"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"alpha"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`12`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"beta"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`7`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"gamma"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`20`)},
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "filter-user", BaseVersion: 1, IdempotencyKey: "postgres-filter-seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	first, err := repository.CreateFilterView(ctx, sheetID, "alice", workbook.CreateFilterViewInput{IdempotencyKey: "postgres-filter-first", Name: "amount", Range: "A1:B4", HeaderRows: 1, Active: true, Criteria: []workbook.FilterCriterion{{Column: 2, Operator: "greater_than", Value: json.RawMessage(`10`)}}})
	if err != nil || !first.Active {
		t.Fatalf("create postgres filter: %#v, %v", first, err)
	}
	duplicate, err := repository.CreateFilterView(ctx, sheetID, "alice", workbook.CreateFilterViewInput{IdempotencyKey: "postgres-filter-first", Name: "", Range: "invalid"})
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("postgres filter idempotency: %#v, %v", duplicate, err)
	}
	second, err := repository.CreateFilterView(ctx, sheetID, "alice", workbook.CreateFilterViewInput{IdempotencyKey: "postgres-filter-second", Name: "names", Range: "A1:B4", HeaderRows: 1, Active: true, Criteria: []workbook.FilterCriterion{{Column: 1, Operator: "contains", Value: json.RawMessage(`"a"`)}}})
	if err != nil || !second.Active {
		t.Fatalf("second postgres filter: %#v, %v", second, err)
	}
	items, err := repository.ListFilterViews(ctx, sheetID, "alice")
	if err != nil || len(items) != 2 || items[0].ID != second.ID || !items[0].Active || items[1].Active {
		t.Fatalf("postgres filter activation: %#v, %v", items, err)
	}
	if bob, err := repository.ListFilterViews(ctx, sheetID, "bob"); err != nil || len(bob) != 0 {
		t.Fatalf("postgres filter actor isolation: %#v, %v", bob, err)
	}
	active := true
	first, err = repository.UpdateFilterView(ctx, first.ID, "alice", workbook.UpdateFilterViewInput{Active: &active})
	if err != nil || !first.Active {
		t.Fatalf("reactivate postgres filter: %#v, %v", first, err)
	}
	selected, _ := cellrange.Parse(first.Range)
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workbook.EvaluateFilter(first, cells)
	if err != nil || !reflect.DeepEqual(result.HiddenRows, []int{3}) {
		t.Fatalf("postgres filter result: %#v, %v", result, err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "filter-user", BaseVersion: 2, IdempotencyKey: "postgres-filter-latest", Cells: []workbook.CellInput{{Row: 3, Column: 2, Value: json.RawMessage(`15`)}}}); err != nil {
		t.Fatal(err)
	}
	cells, _ = repository.ReadRange(ctx, sheetID, selected)
	result, err = workbook.EvaluateFilter(first, cells)
	if err != nil || len(result.HiddenRows) != 0 {
		t.Fatalf("postgres filter latest result: %#v, %v", result, err)
	}
	if err := repository.DeleteFilterView(ctx, first.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetFilterView(ctx, first.ID, "alice"); !errors.Is(err, workbook.ErrNotFound) {
		t.Fatalf("deleted postgres filter error = %v", err)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, createErr := repository.CreateFilterView(ctx, sheetID, "alice", workbook.CreateFilterViewInput{IdempotencyKey: fmt.Sprintf("postgres-filter-concurrent-%d", index), Name: fmt.Sprintf("concurrent-%d", index), Range: "A1:B4", HeaderRows: 1, Active: true, Criteria: []workbook.FilterCriterion{{Column: 2, Operator: "is_not_blank"}}})
			errorsFound <- createErr
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for createErr := range errorsFound {
		if createErr != nil {
			t.Fatalf("concurrent active filter: %v", createErr)
		}
	}
	items, err = repository.ListFilterViews(ctx, sheetID, "alice")
	activeCount := 0
	for _, item := range items {
		if item.Active {
			activeCount++
		}
	}
	if err != nil || activeCount != 1 {
		t.Fatalf("concurrent filter activation: active=%d items=%#v err=%v", activeCount, items, err)
	}
}

func TestPostgresDataValidationPersistsAndEnforcesEveryWritePath(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres validations", WorkspaceID: "integration", OwnerID: "validation-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	created, err := repository.CreateDataValidation(ctx, sheetID, "alice", workbook.CreateDataValidationInput{IdempotencyKey: "validation-create", Range: "A1:A3", RuleType: "list", Options: []workbook.ValidationOption{{Value: json.RawMessage(`"open"`), Color: "#dcfce7"}, {Value: json.RawMessage(`"closed"`), Color: "#fee2e2"}}})
	if err != nil || created.Revision != 1 || created.WorkbookVersion != 2 {
		t.Fatalf("create validation=%#v err=%v", created, err)
	}
	duplicate, err := repository.CreateDataValidation(ctx, sheetID, "bob", workbook.CreateDataValidationInput{IdempotencyKey: "validation-create", Range: "invalid"})
	if err != nil || duplicate.ID != created.ID || duplicate.WorkbookVersion != 2 {
		t.Fatalf("idempotent validation=%#v err=%v", duplicate, err)
	}
	if _, err := repository.CreateDataValidation(ctx, sheetID, "alice", workbook.CreateDataValidationInput{IdempotencyKey: "validation-overlap", Range: "A3:B4", RuleType: "number", Operator: "greater_than", Value: json.RawMessage(`0`)}); !errors.Is(err, workbook.ErrInvalid) {
		t.Fatalf("overlap error=%v", err)
	}
	items, err := repository.ListDataValidations(ctx, sheetID)
	if err != nil || len(items) != 1 || items[0].ID != created.ID || items[0].Options[0].Color != "#dcfce7" {
		t.Fatalf("list validations=%#v err=%v", items, err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "agent", BaseVersion: 2, IdempotencyKey: "validation-reject", OperationType: "cells.paste", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`"other"`)}}}); !errors.Is(err, workbook.ErrValidation) {
		t.Fatalf("invalid write error=%v", err)
	}
	if cells, err := repository.ReadAllCells(ctx, sheetID); err != nil || len(cells) != 0 {
		t.Fatalf("rejected write persisted %#v err=%v", cells, err)
	}
	reject := false
	expected := created.Revision
	updated, err := repository.UpdateDataValidation(ctx, created.ID, "bob", workbook.UpdateDataValidationInput{RejectInput: &reject, ExpectedRevision: &expected})
	if err != nil || updated.Revision != 2 || updated.WorkbookVersion != 3 || updated.UpdatedBy != "bob" {
		t.Fatalf("update validation=%#v err=%v", updated, err)
	}
	accepted, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "agent", BaseVersion: 3, IdempotencyKey: "validation-warning", OperationType: "cells.fill", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`"other"`)}}})
	if err != nil || accepted.ServerVersion != 4 || len(accepted.ValidationWarnings) != 1 {
		t.Fatalf("warning write=%#v err=%v", accepted, err)
	}
	retried, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "agent", BaseVersion: 3, IdempotencyKey: "validation-warning", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`"open"`)}}})
	if err != nil || !retried.Duplicate || len(retried.ValidationWarnings) != 1 {
		t.Fatalf("warning retry=%#v err=%v", retried, err)
	}
	selected, _ := cellrange.Parse(updated.Range)
	cells, _ := repository.ReadRange(ctx, sheetID, selected)
	evaluated, err := workbook.EvaluateDataValidation(updated, cells)
	if err != nil || len(evaluated.InvalidCells) != 1 || evaluated.InvalidCells[0].Row != 1 {
		t.Fatalf("validation evaluation=%#v err=%v", evaluated, err)
	}
	saved, err := repository.CreateVersion(ctx, book.ID, "validation snapshot", "alice")
	if err != nil {
		t.Fatal(err)
	}
	stale := int64(1)
	if err := repository.DeleteDataValidation(ctx, created.ID, "alice", &stale); !errors.Is(err, workbook.ErrRevision) {
		t.Fatalf("stale delete error=%v", err)
	}
	current := updated.Revision
	if err := repository.DeleteDataValidation(ctx, created.ID, "alice", &current); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetDataValidation(ctx, created.ID); !errors.Is(err, workbook.ErrNotFound) {
		t.Fatalf("deleted validation error=%v", err)
	}
	if _, err := repository.RestoreVersion(ctx, saved.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	restored, err := repository.GetDataValidation(ctx, created.ID)
	if err != nil || restored.Revision != updated.Revision || restored.Range != updated.Range || restored.Options[0].Color != "#dcfce7" {
		t.Fatalf("restored validation=%#v err=%v", restored, err)
	}
}

func TestPostgresConditionalFormatsPersistEvaluateTransformCopyAndRestore(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres conditional formats", WorkspaceID: "integration", OwnerID: "format-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seeded, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: "format-user", BaseVersion: book.Version, IdempotencyKey: "conditional-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`10`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`20`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`20`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", workbook.CreateConditionalFormatInput{IdempotencyKey: "conditional-create", Name: "duplicates", Range: "A1:A3", RuleType: "duplicate", Operator: "duplicate", Style: json.RawMessage(`{"background":"#fef3c7","bold":true}`), Priority: 1})
	if err != nil || created.Revision != 1 || created.WorkbookVersion != seeded.ServerVersion+1 {
		t.Fatalf("create conditional format=%#v err=%v", created, err)
	}
	duplicate, err := repository.CreateConditionalFormat(ctx, sheet.ID, "bob", workbook.CreateConditionalFormatInput{IdempotencyKey: "conditional-create", Range: "invalid", RuleType: "invalid"})
	if err != nil || duplicate.ID != created.ID || duplicate.WorkbookVersion != created.WorkbookVersion {
		t.Fatalf("idempotent conditional format=%#v err=%v", duplicate, err)
	}
	selected, _ := cellrange.Parse("A1:A3")
	evaluated, err := repository.EvaluateConditionalFormats(ctx, sheet.ID, selected)
	if err != nil || len(evaluated.Items) != 2 || evaluated.Items[0].Row != 2 || evaluated.Items[1].Row != 3 {
		t.Fatalf("conditional evaluation=%#v err=%v", evaluated, err)
	}
	// 아이콘 집합의 설정은 컬럼 두 개에 따로 들어간다. 구조체와 API 에는
	// 닿았지만 INSERT 문에서 빠지는 실수는 만든 응답만 보면 보이지 않으므로,
	// 저장소에서 다시 읽어 확인한다.
	icons, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", workbook.CreateConditionalFormatInput{
		IdempotencyKey: "conditional-icons", Name: "icons", Range: "B1:B3", RuleType: "icon_set", IconStyle: "5Arrows", IconReverse: true, Priority: 2})
	if err != nil {
		t.Fatalf("icon set create err=%v", err)
	}
	reread, err := repository.GetConditionalFormat(ctx, icons.ID)
	if err != nil || reread.IconStyle != "5Arrows" || !reread.IconReverse {
		t.Fatalf("icon set reread=%#v err=%v", reread, err)
	}
	flipped := false
	turned, err := repository.UpdateConditionalFormat(ctx, icons.ID, "bob", workbook.UpdateConditionalFormatInput{IconReverse: &flipped, ExpectedRevision: &reread.Revision})
	if err != nil {
		t.Fatalf("icon set update err=%v", err)
	}
	if stored, storeErr := repository.GetConditionalFormat(ctx, turned.ID); storeErr != nil || stored.IconReverse || stored.IconStyle != "5Arrows" {
		t.Fatalf("icon set after update=%#v err=%v", stored, storeErr)
	}
	if err := repository.DeleteConditionalFormat(ctx, icons.ID, "alice", nil); err != nil {
		t.Fatalf("icon set delete err=%v", err)
	}

	name := "repeated values"
	expected := created.Revision
	updated, err := repository.UpdateConditionalFormat(ctx, created.ID, "bob", workbook.UpdateConditionalFormatInput{Name: &name, ExpectedRevision: &expected})
	if err != nil || updated.Revision != 2 || updated.Name != name || updated.UpdatedBy != "bob" {
		t.Fatalf("conditional update=%#v err=%v", updated, err)
	}
	if _, err := repository.UpdateConditionalFormat(ctx, created.ID, "bob", workbook.UpdateConditionalFormatInput{Name: &name, ExpectedRevision: &expected}); !errors.Is(err, workbook.ErrRevision) {
		t.Fatalf("stale conditional update=%v", err)
	}
	saved, err := repository.CreateVersion(ctx, book.ID, "conditional snapshot", "alice")
	if err != nil {
		t.Fatal(err)
	}
	structured, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: updated.WorkbookVersion, IdempotencyKey: "conditional-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	transformed, err := repository.GetConditionalFormat(ctx, created.ID)
	if err != nil || transformed.Range != "A1:A4" || transformed.Revision != 3 || transformed.WorkbookVersion != structured.ServerVersion {
		t.Fatalf("transformed conditional format=%#v err=%v", transformed, err)
	}
	sheetCopy, err := repository.DuplicateSheet(ctx, sheet.ID, workbook.DuplicateSheetInput{Name: "formatted copy"})
	if err != nil {
		t.Fatal(err)
	}
	copiedRules, err := repository.ListConditionalFormats(ctx, sheetCopy.ID)
	if err != nil || len(copiedRules) != 1 || copiedRules[0].Range != "A1:A4" || copiedRules[0].ID == created.ID {
		t.Fatalf("copied sheet conditional formats=%#v err=%v", copiedRules, err)
	}
	workbookCopy, err := repository.DuplicateWorkbook(ctx, book.ID, workbook.DuplicateWorkbookInput{OwnerID: "copy-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), workbookCopy.ID, "integration-cleanup")
	workbookCopyRules, err := repository.ListConditionalFormats(ctx, workbookCopy.Sheets[0].ID)
	if err != nil || len(workbookCopyRules) != 1 || workbookCopyRules[0].Range != "A1:A4" || workbookCopyRules[0].WorkbookID != workbookCopy.ID {
		t.Fatalf("copied workbook conditional formats=%#v err=%v", workbookCopyRules, err)
	}
	current, err := repository.GetConditionalFormat(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteConditionalFormat(ctx, created.ID, "alice", &current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetConditionalFormat(ctx, created.ID); !errors.Is(err, workbook.ErrNotFound) {
		t.Fatalf("deleted conditional format error=%v", err)
	}
	if _, err := repository.RestoreVersion(ctx, saved.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	restored, err := repository.GetConditionalFormat(ctx, created.ID)
	if err != nil || restored.Range != "A1:A3" || restored.Revision != updated.Revision || restored.Name != name {
		t.Fatalf("restored conditional format=%#v err=%v", restored, err)
	}
}

func TestPostgresArrayFormulaSpillPersistsRestoresAndUndoes(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres arrays", OwnerID: "array-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	seed := []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"a"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`30`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"b"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`10`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"c"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`20`)},
	}
	if _, err = repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "array-user", BaseVersion: 1, IdempotencyKey: "postgres-array-seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	created, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "array-user", BaseVersion: 2, IdempotencyKey: "postgres-array-formula", Cells: []workbook.CellInput{{Row: 1, Column: 4, Formula: `=FILTER(A1:B3,B1:B3>=20)`}}})
	if err != nil || len(created.FormulaErrors) != 0 || len(created.RecalculatedCells) != 4 {
		t.Fatalf("created spill = %#v, %v", created, err)
	}
	selected, _ := cellrange.Parse("D1:E2")
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 4 || cells[1].SpillSource != "D1" || cells[2].SpillSource != "D1" || cells[3].SpillSource != "D1" {
		t.Fatalf("persisted spill = %#v, %v", cells, err)
	}
	version, err := repository.CreateVersion(ctx, book.ID, "array baseline", "array-user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "array-user", BaseVersion: 3, IdempotencyKey: "postgres-array-shrink", Cells: []workbook.CellInput{{Row: 1, Column: 2, Value: json.RawMessage(`5`)}}}); err != nil {
		t.Fatal(err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 2 || string(cells[0].Value) != `"c"` {
		t.Fatalf("shrunk spill = %#v, %v", cells, err)
	}
	if _, err = repository.RestoreVersion(ctx, version.ID, "array-user"); err != nil {
		t.Fatal(err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 4 || cells[3].SpillSource != "D1" || string(cells[0].Value) != `"a"` {
		t.Fatalf("restored spill = %#v, %v", cells, err)
	}
	if _, err = repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "array-user", BaseVersion: 5, IdempotencyKey: "postgres-array-child-edit", Cells: []workbook.CellInput{{Row: 2, Column: 4, Value: json.RawMessage(`"invalid"`)}}}); !errors.Is(err, workbook.ErrInvalid) {
		t.Fatalf("spill child edit error = %v", err)
	}
	if _, err = repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: created.OperationID, ActorID: "array-user", IdempotencyKey: "postgres-array-undo"}); err != nil {
		t.Fatal(err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 0 {
		t.Fatalf("spill after undo = %#v, %v", cells, err)
	}
}

func TestPostgresCrossSheetFormulaRecalculatesPersistsAndConflicts(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres cross sheet", WorkspaceID: "integration", OwnerID: "cross-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), wb.ID, "integration-cleanup")
	inputSheet := wb.Sheets[0]
	reportSheet, err := repository.CreateSheet(ctx, wb.ID, workbook.CreateSheetInput{Name: "Sales Report"})
	if err != nil {
		t.Fatal(err)
	}
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: inputSheet.ID, ActorID: "cross-user", ClientID: "input", BaseVersion: 2, IdempotencyKey: "postgres-cross-seed", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`10`)}}})
	if err != nil {
		t.Fatal(err)
	}
	formulaResult, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: reportSheet.ID, ActorID: "cross-user", ClientID: "report", BaseVersion: seed.ServerVersion, IdempotencyKey: "postgres-cross-formula", Cells: []workbook.CellInput{{Row: 1, Column: 2, Formula: `='Sheet1'!A1*2`}}})
	if err != nil || len(formulaResult.FormulaErrors) != 0 {
		t.Fatalf("cross formula=%#v error=%v", formulaResult, err)
	}
	renamed := "Raw Data"
	if _, err := repository.UpdateSheet(ctx, inputSheet.ID, workbook.UpdateSheetInput{Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("B1")
	cells, err := repository.ReadRange(ctx, reportSheet.ID, selected)
	if err != nil || len(cells) != 1 || cells[0].Formula != `='Raw Data'!A1*2` {
		t.Fatalf("renamed cross formula cells=%#v error=%v", cells, err)
	}
	latest, err := repository.GetWorkbook(ctx, wb.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: inputSheet.ID, ActorID: "cross-user", ClientID: "input", BaseVersion: latest.Version, IdempotencyKey: "postgres-cross-update", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`25`)}}})
	if err != nil || len(updated.RecalculatedCells) != 1 || updated.RecalculatedCells[0].SheetID != reportSheet.ID {
		t.Fatalf("cross update=%#v error=%v", updated, err)
	}
	cells, err = repository.ReadRange(ctx, reportSheet.ID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "50" {
		t.Fatalf("cross persisted cells=%#v error=%v", cells, err)
	}
	undone, err := repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: updated.OperationID, ActorID: "cross-user", ClientID: "input", IdempotencyKey: "postgres-cross-undo"})
	if err != nil || len(undone.RecalculatedCells) != 1 || undone.RecalculatedCells[0].SheetID != reportSheet.ID {
		t.Fatalf("cross undo=%#v error=%v", undone, err)
	}
	cells, _ = repository.ReadRange(ctx, reportSheet.ID, selected)
	if len(cells) != 1 || string(cells[0].Value) != "20" {
		t.Fatalf("cross undo persisted cells=%#v", cells)
	}

	// A stale edit on the dependent sheet must see the derived change even
	// though the operation that caused it was submitted on another sheet.
	changedAgain, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: inputSheet.ID, ActorID: "cross-user", ClientID: "input", BaseVersion: undone.ServerVersion, IdempotencyKey: "postgres-cross-change-again", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`30`)}}})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: reportSheet.ID, ActorID: "other-user", ClientID: "other", BaseVersion: undone.ServerVersion, IdempotencyKey: "postgres-cross-stale", Cells: []workbook.CellInput{{Row: 1, Column: 2, Value: json.RawMessage(`999`)}}})
	if err != nil || changedAgain.ServerVersion+1 != stale.ServerVersion || len(stale.Conflicts) != 1 {
		t.Fatalf("cross-sheet derived conflict=%#v error=%v", stale, err)
	}
}

func TestPostgresNamedRangeLifecycleAndVersionRestore(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres named range", OwnerID: "named-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "named-user", BaseVersion: 1, IdempotencyKey: "pg-named-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`12`)}, {Row: 2, Column: 1, Value: json.RawMessage(`18`)}, {Row: 1, Column: 2, Formula: "=SUM(Sales_Data)"},
	}})
	if err != nil || seed.ServerVersion != 2 || len(seed.FormulaErrors) != 1 || seed.FormulaErrors[0].Code != "#NAME?" {
		t.Fatalf("PostgreSQL missing named formula = %#v, error=%v", seed, err)
	}
	created, err := repository.CreateNamedRange(ctx, book.ID, "named-user", workbook.CreateNamedRangeInput{IdempotencyKey: "pg-named-create", Name: "Sales_Data", SheetID: sheetID, Range: "A1:A2"})
	if err != nil || created.WorkbookVersion != 3 || created.Revision != 1 {
		t.Fatalf("PostgreSQL named create = %#v, error=%v", created, err)
	}
	duplicate, err := repository.CreateNamedRange(ctx, book.ID, "named-user", workbook.CreateNamedRangeInput{IdempotencyKey: "pg-named-create", Name: "ignored", SheetID: sheetID, Range: "A1"})
	if err != nil || duplicate.ID != created.ID || duplicate.WorkbookVersion != 3 {
		t.Fatalf("PostgreSQL named idempotency = %#v, error=%v", duplicate, err)
	}
	assertPostgresNamedFormula(t, ctx, repository, sheetID, "=SUM(Sales_Data)", "30")
	expectedOne := int64(1)
	name, selectedRange := "Revenue", "A1"
	updated, err := repository.UpdateNamedRange(ctx, created.ID, "named-user", workbook.UpdateNamedRangeInput{Name: &name, Range: &selectedRange, ExpectedRevision: &expectedOne})
	if err != nil || updated.WorkbookVersion != 4 || updated.Revision != 2 {
		t.Fatalf("PostgreSQL named update = %#v, error=%v", updated, err)
	}
	assertPostgresNamedFormula(t, ctx, repository, sheetID, "=SUM(Revenue)", "12")
	version, err := repository.CreateVersion(ctx, book.ID, "named snapshot", "named-user")
	if err != nil {
		t.Fatal(err)
	}
	expectedTwo := int64(2)
	if err := repository.DeleteNamedRange(ctx, created.ID, "named-user", &expectedTwo); err != nil {
		t.Fatal(err)
	}
	assertPostgresNamedFormula(t, ctx, repository, sheetID, "=SUM(Revenue)", `"#NAME?"`)
	restored, err := repository.RestoreVersion(ctx, version.ID, "named-user")
	if err != nil || restored.ServerVersion != 6 {
		t.Fatalf("PostgreSQL named restore = %#v, error=%v", restored, err)
	}
	ranges, err := repository.ListNamedRanges(ctx, book.ID)
	if err != nil || len(ranges) != 1 || ranges[0].Name != "Revenue" || ranges[0].Range != "A1" {
		t.Fatalf("PostgreSQL restored named ranges = %#v, error=%v", ranges, err)
	}
	assertPostgresNamedFormula(t, ctx, repository, sheetID, "=SUM(Revenue)", "12")
	copy, err := repository.DuplicateWorkbook(ctx, book.ID, workbook.DuplicateWorkbookInput{OwnerID: "named-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), copy.ID, "integration-cleanup")
	copyRanges, err := repository.ListNamedRanges(ctx, copy.ID)
	if err != nil || len(copyRanges) != 1 || copyRanges[0].SheetID == sheetID {
		t.Fatalf("PostgreSQL copied named ranges = %#v, error=%v", copyRanges, err)
	}
	assertPostgresNamedFormula(t, ctx, repository, copy.Sheets[0].ID, "=SUM(Revenue)", "12")
}

func assertPostgresNamedFormula(t *testing.T, ctx context.Context, repository workbook.Repository, sheetID, formula, value string) {
	t.Helper()
	selected, _ := cellrange.Parse("B1")
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || cells[0].Formula != formula || string(cells[0].Value) != value {
		t.Fatalf("PostgreSQL named formula = %#v, error=%v; want formula=%s value=%s", cells, err, formula, value)
	}
}

func TestPostgresStructureMutationMovesDefinitionsAndRestoresBrokenNames(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres structure", OwnerID: "structure-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	inputSheet := book.Sheets[0]
	reportSheet, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "Report"})
	if err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(ctx, book.ID)
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: inputSheet.ID, ActorID: "structure-user", ClientID: "one", BaseVersion: book.Version, IdempotencyKey: "postgres-structure-seed", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`10`)}, {Row: 2, Column: 1, Value: json.RawMessage(`20`)}, {Row: 2, Column: 2, Formula: "=A2*2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: reportSheet.ID, ActorID: "structure-user", ClientID: "one", BaseVersion: seed.ServerVersion, IdempotencyKey: "postgres-structure-report", Cells: []workbook.CellInput{{Row: 1, Column: 1, Formula: "=Sheet1!A2"}}}); err != nil {
		t.Fatal(err)
	}
	name, err := repository.CreateNamedRange(ctx, book.ID, "structure-user", workbook.CreateNamedRangeInput{IdempotencyKey: "postgres-structure-name", Name: "Sales", SheetID: inputSheet.ID, Range: "A1:A2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateDataValidation(ctx, inputSheet.ID, "structure-user", workbook.CreateDataValidationInput{IdempotencyKey: "postgres-structure-validation", Range: "B1:B3", RuleType: "number", Operator: "greater_than", Value: json.RawMessage(`0`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateFilterView(ctx, inputSheet.ID, "structure-user", workbook.CreateFilterViewInput{IdempotencyKey: "postgres-structure-filter", Name: "Rows", Range: "A1:B4", HeaderRows: 1, Criteria: []workbook.FilterCriterion{{Column: 1, Operator: "is_not_blank"}}, Active: true}); err != nil {
		t.Fatal(err)
	}
	book, _ = repository.GetWorkbook(ctx, book.ID)
	inserted, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: inputSheet.ID, ActorID: "structure-user", ClientID: "one", BaseVersion: book.Version, IdempotencyKey: "postgres-insert-row", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil || inserted.BackupVersionID == "" || inserted.ServerVersion != book.Version+1 {
		t.Fatalf("PostgreSQL structure insert = %#v, error=%v", inserted, err)
	}
	duplicate, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: inputSheet.ID, ActorID: "structure-user", BaseVersion: book.Version, IdempotencyKey: "postgres-insert-row", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil || !duplicate.Duplicate || duplicate.OperationID != inserted.OperationID || duplicate.BackupVersionID != inserted.BackupVersionID {
		t.Fatalf("PostgreSQL structure duplicate = %#v, error=%v", duplicate, err)
	}
	assertPostgresStructureCell(t, ctx, repository, inputSheet.ID, "A3", "", "20")
	assertPostgresStructureCell(t, ctx, repository, inputSheet.ID, "B3", "=A3*2", "40")
	assertPostgresStructureCell(t, ctx, repository, reportSheet.ID, "A1", "=Sheet1!A3", "20")
	ranges, _ := repository.ListNamedRanges(ctx, book.ID)
	if len(ranges) != 1 || ranges[0].Range != "A1:A3" || ranges[0].Revision != name.Revision+1 {
		t.Fatalf("PostgreSQL structure names = %#v", ranges)
	}
	rules, _ := repository.ListDataValidations(ctx, inputSheet.ID)
	if len(rules) != 1 || rules[0].Range != "B1:B4" || rules[0].Revision != 2 {
		t.Fatalf("PostgreSQL structure validations = %#v", rules)
	}
	views, _ := repository.ListFilterViews(ctx, inputSheet.ID, "structure-user")
	if len(views) != 1 || views[0].Range != "A1:B5" || views[0].HeaderRows != 1 {
		t.Fatalf("PostgreSQL structure filters = %#v", views)
	}
	deleted, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: inputSheet.ID, ActorID: "structure-user", ClientID: "one", BaseVersion: inserted.ServerVersion, IdempotencyKey: "postgres-delete-row", Axis: "row", Action: "delete", Index: 1, Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	assertPostgresStructureCell(t, ctx, repository, reportSheet.ID, "A1", "=#REF!", `"#REF!"`)
	ranges, _ = repository.ListNamedRanges(ctx, book.ID)
	if len(ranges) != 1 || ranges[0].Range != "#REF!" {
		t.Fatalf("PostgreSQL broken named range = %#v", ranges)
	}
	brokenVersion, err := repository.CreateVersion(ctx, book.ID, "broken named range", "structure-user")
	if err != nil {
		t.Fatal(err)
	}
	repairedRange, expectedRevision := "A1", ranges[0].Revision
	if _, err := repository.UpdateNamedRange(ctx, ranges[0].ID, "structure-user", workbook.UpdateNamedRangeInput{Range: &repairedRange, ExpectedRevision: &expectedRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RestoreVersion(ctx, brokenVersion.ID, "structure-user"); err != nil {
		t.Fatalf("restore #REF named range: %v", err)
	}
	ranges, _ = repository.ListNamedRanges(ctx, book.ID)
	if len(ranges) != 1 || ranges[0].Range != "#REF!" {
		t.Fatalf("PostgreSQL restored broken named range = %#v", ranges)
	}
	if _, err := repository.RestoreVersion(ctx, deleted.BackupVersionID, "structure-user"); err != nil {
		t.Fatal(err)
	}
	assertPostgresStructureCell(t, ctx, repository, inputSheet.ID, "A3", "", "20")
	views, _ = repository.ListFilterViews(ctx, inputSheet.ID, "structure-user")
	if len(views) != 1 || views[0].Range != "A1:B5" || views[0].HeaderRows != 1 {
		t.Fatalf("PostgreSQL restored structure filters = %#v", views)
	}
}

func assertPostgresStructureCell(t *testing.T, ctx context.Context, repository workbook.Repository, sheetID, address, formulaText, value string) {
	t.Helper()
	selected, _ := cellrange.Parse(address)
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || cells[0].Formula != formulaText || string(cells[0].Value) != value {
		t.Fatalf("PostgreSQL structure cell %s = %#v, error=%v; want formula=%s value=%s", address, cells, err, formulaText, value)
	}
}

func TestPostgresSheetLayoutPersistsAndFollowsStructure(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres layout", OwnerID: "layout-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	resized, err := repository.ApplySheetLayout(ctx, workbook.SheetLayoutMutation{SheetID: sheet.ID, ActorID: "layout-user", ClientID: "browser", ExpectedRevision: 1, IdempotencyKey: "pg-layout-resize", Action: "resize", Axis: "column", Start: 2, Count: 2, Size: 180})
	if err != nil || resized.ServerVersion != 2 || resized.Layout.Revision != 2 {
		t.Fatalf("PostgreSQL layout resize = %#v, error=%v", resized, err)
	}
	duplicate, err := repository.ApplySheetLayout(ctx, workbook.SheetLayoutMutation{SheetID: sheet.ID, ActorID: "layout-user", ExpectedRevision: 1, IdempotencyKey: "pg-layout-resize", Action: "resize", Axis: "column", Start: 2, Count: 2, Size: 180})
	if err != nil || !duplicate.Duplicate || duplicate.OperationID != resized.OperationID {
		t.Fatalf("PostgreSQL layout duplicate = %#v, error=%v", duplicate, err)
	}
	hidden, err := repository.ApplySheetLayout(ctx, workbook.SheetLayoutMutation{SheetID: sheet.ID, ActorID: "layout-user", ExpectedRevision: 2, IdempotencyKey: "pg-layout-hide", Action: "hide", Axis: "column", Start: 3, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := repository.ApplySheetLayout(ctx, workbook.SheetLayoutMutation{SheetID: sheet.ID, ActorID: "layout-user", ExpectedRevision: 3, IdempotencyKey: "pg-layout-freeze", Action: "freeze", FrozenRows: 1, FrozenColumns: 3})
	if err != nil {
		t.Fatal(err)
	}
	structured, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{SheetID: sheet.ID, ActorID: "layout-user", BaseVersion: frozen.ServerVersion, IdempotencyKey: "pg-layout-insert", Axis: "column", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	layout := loaded.Sheets[0].Layout
	if layout.Revision != 5 || layout.FrozenColumns != 4 || len(layout.ColumnWidths) != 2 || layout.ColumnWidths[0].Index != 3 || len(layout.HiddenColumns) != 1 || layout.HiddenColumns[0] != (workbook.DimensionRange{Start: 4, End: 5}) {
		t.Fatalf("PostgreSQL transformed layout = %#v (hidden result %#v, structure %#v)", layout, hidden, structured)
	}
	if _, err := repository.RestoreVersion(ctx, structured.BackupVersionID, "layout-user"); err != nil {
		t.Fatal(err)
	}
	restored, _ := repository.GetWorkbook(ctx, book.ID)
	if restored.Sheets[0].Layout.Revision != 4 || restored.Sheets[0].Layout.FrozenColumns != 3 || restored.Sheets[0].Layout.HiddenColumns[0] != (workbook.DimensionRange{Start: 3, End: 4}) {
		t.Fatalf("PostgreSQL restored layout = %#v", restored.Sheets[0].Layout)
	}
}

func TestPostgresDeletingReferencedSheetStoresRefError(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres deleted reference", WorkspaceID: "integration", OwnerID: "delete-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), wb.ID, "integration-cleanup")
	inputSheet := wb.Sheets[0]
	reportSheet, err := repository.CreateSheet(ctx, wb.ID, workbook.CreateSheetInput{Name: "Report"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: reportSheet.ID, ActorID: "delete-user", BaseVersion: 2, IdempotencyKey: "postgres-delete-reference", Cells: []workbook.CellInput{{Row: 1, Column: 1, Formula: `=Sheet1!A1`}, {Row: 1, Column: 2, Formula: `=A1+1`}}})
	if err != nil || len(created.FormulaErrors) != 0 {
		t.Fatalf("create deleted references=%#v error=%v", created, err)
	}
	if _, err := repository.DeleteSheet(ctx, inputSheet.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A1:B1")
	cells, err := repository.ReadRange(ctx, reportSheet.ID, selected)
	if err != nil || len(cells) != 2 || string(cells[0].Value) != `"#REF!"` || string(cells[1].Value) != `"#REF!"` {
		t.Fatalf("PostgreSQL deleted reference cells=%#v error=%v", cells, err)
	}
}

func TestPostgresWorkbookDuplicateIsIndependentAndPreservesData(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	source, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres original", WorkspaceID: "integration", OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), source.ID, "integration-cleanup")
	detail, err := repository.CreateSheet(ctx, source.ID, workbook.CreateSheetInput{Name: "detail", Color: "#2563eb"})
	if err != nil {
		t.Fatal(err)
	}
	hidden := true
	if _, err := repository.UpdateSheet(ctx, detail.ID, workbook.UpdateSheetInput{Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: detail.ID, ActorID: "owner-a", BaseVersion: 3, IdempotencyKey: "postgres-workbook-copy-seed", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`7`), Style: json.RawMessage(`{"bold":true}`)}, {Row: 2, Column: 1, Formula: "=A1*2"}}}); err != nil {
		t.Fatal(err)
	}
	duplicated, err := repository.DuplicateWorkbook(ctx, source.ID, workbook.DuplicateWorkbookInput{OwnerID: "owner-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), duplicated.ID, "integration-cleanup")
	if duplicated.ID == source.ID || duplicated.Title != "postgres original 복사본" || duplicated.OwnerID != "owner-b" || duplicated.Version != 1 || len(duplicated.Sheets) != 2 || duplicated.Sheets[1].ID == detail.ID || duplicated.Sheets[1].Color != "#2563eb" || !duplicated.Sheets[1].Hidden {
		t.Fatalf("duplicated workbook: %#v", duplicated)
	}
	selected, _ := cellrange.Parse("A1:A2")
	cells, err := repository.ReadRange(ctx, duplicated.Sheets[1].ID, selected)
	var copiedStyle map[string]any
	if len(cells) > 0 {
		_ = json.Unmarshal(cells[0].Style, &copiedStyle)
	}
	if err != nil || len(cells) != 2 || cells[0].SheetID != duplicated.Sheets[1].ID || string(cells[0].Value) != "7" || copiedStyle["bold"] != true || cells[1].Formula != "=A1*2" || string(cells[1].Value) != "14" {
		t.Fatalf("duplicated cells: %#v, %v", cells, err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: detail.ID, ActorID: "owner-a", BaseVersion: 4, IdempotencyKey: "postgres-change-original", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`9`)}}}); err != nil {
		t.Fatal(err)
	}
	cells, err = repository.ReadRange(ctx, duplicated.Sheets[1].ID, selected)
	if err != nil || string(cells[0].Value) != "7" || string(cells[1].Value) != "14" {
		t.Fatalf("copy changed with source: %#v, %v", cells, err)
	}
	if err := repository.DeleteWorkbook(ctx, source.ID, "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetWorkbook(ctx, duplicated.ID); err != nil {
		t.Fatalf("copy was deleted with source: %v", err)
	}
}

func TestPostgresVersionRestoresDeletedSheetStructure(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "snapshot original", WorkspaceID: "integration", OwnerID: "version-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	detail, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "detail", Color: "#16a34a"})
	if err != nil {
		t.Fatal(err)
	}
	favorite := true
	if _, err := repository.UpdateWorkbook(ctx, book.ID, workbook.UpdateWorkbookInput{Favorite: &favorite}); err != nil {
		t.Fatal(err)
	}
	firstName, color, hidden := "summary", "#2563eb", true
	if _, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, workbook.UpdateSheetInput{Name: &firstName, Color: &color, Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: detail.ID, ActorID: "version-user", ClientID: "integration", BaseVersion: 4, IdempotencyKey: "version-detail", Cells: []workbook.CellInput{{Row: 2, Column: 2, Value: json.RawMessage(`42`)}}}); err != nil {
		t.Fatal(err)
	}
	target, err := repository.CreateVersion(ctx, book.ID, "structural target", "version-user")
	if err != nil {
		t.Fatal(err)
	}

	changedTitle := "snapshot changed"
	favorite = false
	if _, err := repository.UpdateWorkbook(ctx, book.ID, workbook.UpdateWorkbookInput{Title: &changedTitle, Favorite: &favorite}); err != nil {
		t.Fatal(err)
	}
	changedName := "temporary summary"
	if _, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, workbook.UpdateSheetInput{Name: &changedName}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteSheet(ctx, detail.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	temporary, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "temporary"})
	if err != nil {
		t.Fatal(err)
	}

	restored, err := repository.RestoreVersion(ctx, target.ID, "version-user")
	if err != nil || restored.ServerVersion != 10 {
		t.Fatalf("restore: %#v, %v", restored, err)
	}
	after, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "snapshot original" || !after.Favorite || len(after.Sheets) != 2 || after.Sheets[0].Name != "summary" || after.Sheets[1].ID != detail.ID {
		t.Fatalf("restored workbook: %#v", after)
	}
	selected, _ := cellrange.Parse("B2")
	cells, err := repository.ReadRange(ctx, detail.ID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "42" {
		t.Fatalf("restored detail: %#v, %v", cells, err)
	}
	if _, err := repository.ReadRange(ctx, temporary.ID, selected); !errors.Is(err, workbook.ErrNotFound) {
		t.Fatalf("temporary sheet survived: %v", err)
	}
	// Three versions: the one this test made, the snapshot the sheet deletion
	// left behind, and the one the restore itself took.
	versions, err := repository.ListVersions(ctx, book.ID)
	if err != nil || len(versions) != 3 || versions[0].Name != "복원 전 자동 백업" || versions[0].WorkbookVersion != 9 {
		t.Fatalf("automatic backup: %#v, %v", versions, err)
	}
	if versions[1].Name != "시트 detail 삭제 전 자동 백업" {
		t.Fatalf("sheet deletion backup: %#v", versions[1])
	}
}

func TestPostgresSheetLifecyclePreservesDataAndPositions(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "postgres sheet lifecycle", WorkspaceID: "integration", OwnerID: "sheet-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	source, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "Data", Color: "#2563eb"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "Tail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: source.ID, ActorID: "sheet-user", BaseVersion: 3, IdempotencyKey: "postgres-sheet-data", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`7`)}, {Row: 2, Column: 1, Formula: "=A1*2", Style: json.RawMessage(`{"bold":true}`)}}}); err != nil {
		t.Fatal(err)
	}
	duplicated, err := repository.DuplicateSheet(ctx, source.ID, workbook.DuplicateSheetInput{})
	if err != nil || duplicated.Name != "Data 복사본" || duplicated.Position != 2 || duplicated.Color != source.Color {
		t.Fatalf("duplicate: %#v, %v", duplicated, err)
	}
	selected, _ := cellrange.Parse("A1:A2")
	cells, err := repository.ReadRange(ctx, duplicated.ID, selected)
	var copiedStyle map[string]any
	if len(cells) == 2 {
		_ = json.Unmarshal(cells[1].Style, &copiedStyle)
	}
	if err != nil || len(cells) != 2 || cells[0].SheetID != duplicated.ID || string(cells[0].Value) != "7" || cells[1].Formula != "=A1*2" || string(cells[1].Value) != "14" || copiedStyle["bold"] != true {
		t.Fatalf("duplicated cells: %#v, %v", cells, err)
	}
	position := 0
	if _, err := repository.UpdateSheet(ctx, duplicated.ID, workbook.UpdateSheetInput{Position: &position}); err != nil {
		t.Fatal(err)
	}
	afterMove, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil || afterMove.Version != 6 || len(afterMove.Sheets) != 4 {
		t.Fatalf("after move: %#v, %v", afterMove, err)
	}
	expected := []string{"Data 복사본", "Sheet1", "Data", "Tail"}
	for index, name := range expected {
		if afterMove.Sheets[index].Position != index || afterMove.Sheets[index].Name != name {
			t.Fatalf("position %d: %#v", index, afterMove.Sheets[index])
		}
	}
	if _, err := repository.UpdateSheet(ctx, duplicated.ID, workbook.UpdateSheetInput{}); err != nil {
		t.Fatal(err)
	}
	afterNoop, _ := repository.GetWorkbook(ctx, book.ID)
	if afterNoop.Version != afterMove.Version {
		t.Fatalf("no-op changed version: %d -> %d", afterMove.Version, afterNoop.Version)
	}
	duplicateName := "DATA"
	if _, err := repository.UpdateSheet(ctx, duplicated.ID, workbook.UpdateSheetInput{Name: &duplicateName}); !errors.Is(err, workbook.ErrDuplicateName) {
		t.Fatalf("case-insensitive duplicate name: %v", err)
	}
	if _, err := repository.DeleteSheet(ctx, book.Sheets[0].ID, "tester"); err != nil {
		t.Fatal(err)
	}
	afterDelete, _ := repository.GetWorkbook(ctx, book.ID)
	if afterDelete.Version != 7 || len(afterDelete.Sheets) != 3 {
		t.Fatalf("after delete: %#v", afterDelete)
	}
	for index, sheet := range afterDelete.Sheets {
		if sheet.Position != index {
			t.Fatalf("compacted position %d: %#v", index, sheet)
		}
	}
}

func TestPostgresAtomicImportExport(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	service := importexport.New(repository)
	key := "integration-import-" + time.Now().Format("150405.000000")
	request := importexport.ImportRequest{FileName: "atomic.csv", Data: []byte("name,amount\nalpha,10\nbeta,20\n"), WorkspaceID: "integration", ActorID: "integration-import-user", IdempotencyKey: key, MaxExpandedBytes: importexport.DefaultMaxExpandedBytes}
	created, err := service.Import(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), created.ID, "integration-cleanup")
	duplicate, err := service.Import(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != created.ID {
		t.Fatalf("idempotent import created %s and %s", created.ID, duplicate.ID)
	}
	cells, err := repository.ReadAllCells(ctx, created.Sheets[0].ID)
	if err != nil || len(cells) != 6 {
		t.Fatalf("imported cells: %d, %v", len(cells), err)
	}
	exported, err := service.Export(ctx, importexport.ExportRequest{WorkbookID: created.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := importexport.Parse(exported.Name, exported.Data, importexport.DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Preview.TotalCells != 6 {
		t.Fatalf("round trip cells = %d", parsed.Preview.TotalCells)
	}
	// 이름 있는 수식은 가져오기 트랜잭션 안에서 직접 INSERT 한다. 메모리 쪽만
	// 시험하면 열 이름이나 자리표시자가 어긋나도 알 수 없고, LAMBDA 이름이
	// 든 파일을 처음 여는 사람에게서 터진다.
	withName, err := repository.CreateNamedFunction(ctx, created.ID, "integration-importer", workbook.CreateNamedFunctionInput{
		IdempotencyKey: "import-fn", Name: "마진율", Parameters: []string{"매출", "원가"}, Body: "(매출-원가)/매출",
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetWorkbook(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{
		SheetID: created.Sheets[0].ID, ActorID: "integration-importer", BaseVersion: current.Version,
		IdempotencyKey: "import-fn-cell", Cells: []workbook.CellInput{{Row: 5, Column: 1, Formula: "=마진율(1000,600)"}},
	}); err != nil {
		t.Fatal(err)
	}
	shipped, err := service.Export(ctx, importexport.ExportRequest{WorkbookID: created.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := importexport.Parse(shipped.Name, shipped.Data, importexport.DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(reparsed.NamedFunctions) != 1 || reparsed.NamedFunctions[0].Name != withName.Name {
		t.Fatalf("돌아온 이름=%#v", reparsed.NamedFunctions)
	}
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "integration", Title: "이름 있는 수식 복원", OwnerID: "integration-importer", ActorID: "integration-importer",
		IdempotencyKey: key + "-fn", FileName: shipped.Name, Format: "xlsx",
		Sheets: reparsed.Sheets, NamedRanges: reparsed.NamedRanges, NamedFunctions: reparsed.NamedFunctions,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), restored.ID, "integration-cleanup")
	stored, err := repository.ListNamedFunctions(ctx, restored.ID)
	if err != nil || len(stored) != 1 || stored[0].Name != "마진율" || len(stored[0].Parameters) != 2 || stored[0].Body != "(매출-원가)/매출" {
		t.Fatalf("저장된 이름=%#v, %v", stored, err)
	}
	// 첫 계산 전에 들어가야 그 이름을 부르는 칸이 #NAME? 으로 굳지 않는다.
	restoredCells, err := repository.ReadAllCells(ctx, restored.Sheets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	calculated := false
	for _, cell := range restoredCells {
		if cell.Row != 5 || cell.Column != 1 {
			continue
		}
		calculated = true
		if string(cell.Value) != "0.4" {
			t.Errorf("이름을 부르는 칸=%s (수식 %q)", cell.Value, cell.Formula)
		}
	}
	if !calculated {
		t.Error("이름을 부르는 칸이 돌아오지 않았다")
	}
}

func TestSettingsAndAPIKeyLifecycle(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	settingRepository := settings.New(pool)
	if err := settingRepository.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	keyName := "integration.setting." + time.Now().Format("150405.000000")
	if _, err := settingRepository.Put(ctx, settings.Setting{Key: keyName, Value: json.RawMessage(`true`), ValueType: "boolean", Description: "integration"}, "test-user"); err != nil {
		t.Fatal(err)
	}
	defer settingRepository.Delete(context.Background(), keyName, "test-cleanup")
	validation, err := settingRepository.Validate(ctx)
	if err != nil || !validation.Valid {
		t.Fatalf("validation: %#v %v", validation, err)
	}
	settingsList, err := settingRepository.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	clientSecretFound := false
	for _, item := range settingsList {
		if item.Key == "auth.oidc.client_secret" {
			clientSecretFound = true
			if !item.Secret || item.Value != nil {
				t.Fatalf("client secret was not redacted: %#v", item)
			}
		}
	}
	if !clientSecretFound {
		t.Fatal("auth.oidc.client_secret default is missing")
	}
	secretKey := "integration.secret." + time.Now().Format("150405.000000")
	secretSetting, err := settingRepository.Put(ctx, settings.Setting{Key: secretKey, Value: json.RawMessage(`"integration-secret"`), ValueType: "string", Secret: true}, "test-user")
	if err != nil || secretSetting.Value != nil || !secretSetting.Configured {
		t.Fatalf("secret setting was not redacted after save: %#v %v", secretSetting, err)
	}
	defer settingRepository.Delete(context.Background(), secretKey, "test-cleanup")

	authService := auth.New(pool, settingRepository, auth.BootstrapCredentials{ID: "integration-admin", Password: "integration-password"})
	if _, _, err := authService.BootstrapLogin(ctx, "integration-admin", "wrong-password"); !errors.Is(err, auth.ErrInvalidBootstrapCredentials) {
		t.Fatalf("invalid bootstrap password: %v", err)
	}
	sessionToken, bootstrapUser, err := authService.BootstrapLogin(ctx, "integration-admin", "integration-password")
	if err != nil {
		t.Fatal(err)
	}
	defer authService.Logout(context.Background(), sessionToken)
	persistedUser, err := authService.Session(ctx, sessionToken)
	if err != nil || persistedUser.ID != bootstrapUser.ID || !authService.IsAdmin(ctx, persistedUser) {
		t.Fatalf("bootstrap session: %#v %v", persistedUser, err)
	}

	keys := apikey.New(pool)
	userID := "integration-key-user"
	defer pool.Exec(context.Background(), `DELETE FROM api_keys WHERE user_id=$1`, userID)
	created, err := keys.Create(ctx, userID, apikey.CreateInput{Name: "agent", Scopes: []string{"mcp.use", "workbook.*"}})
	if err != nil || created.Secret == "" {
		t.Fatalf("create key: %#v %v", created, err)
	}
	principal, err := keys.Authenticate(ctx, created.Secret)
	if err != nil || !principal.Allows("workbook.read") || principal.Allows("admin.*") {
		t.Fatalf("principal: %#v %v", principal, err)
	}
	updatedName := "agent-updated"
	updatedScopes := []string{"mcp.use", "workbook.*", "chart.*"}
	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	expiresPointer := &expiresAt
	updated, err := keys.Update(ctx, created.ID, userID, apikey.UpdateInput{Name: &updatedName, Scopes: &updatedScopes, ExpiresAt: &expiresPointer}, false)
	if err != nil || updated.Name != updatedName || updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("update key: %#v %v", updated, err)
	}
	var clearExpiration *time.Time
	updated, err = keys.Update(ctx, created.ID, userID, apikey.UpdateInput{ExpiresAt: &clearExpiration}, false)
	if err != nil || updated.ExpiresAt != nil {
		t.Fatalf("clear key expiration: %#v %v", updated, err)
	}
	rotated, err := keys.Rotate(ctx, created.ID, userID, false)
	if err != nil || rotated.Secret == created.Secret {
		t.Fatalf("rotate key: %#v %v", rotated, err)
	}
	if _, err := keys.Authenticate(ctx, created.Secret); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("old key remained valid: %v", err)
	}
	if err := keys.Revoke(ctx, rotated.ID, userID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Authenticate(ctx, rotated.Secret); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked key remained valid: %v", err)
	}
}

func TestPostgresAIPlanApprovalUndoAndAudit(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("ai-user-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "AI lifecycle", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, ClientID: "browser", BaseVersion: 1, IdempotencyKey: "ai-seed", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`5`)}}})
	if err != nil {
		t.Fatal(err)
	}

	var gatewayCalls int
	gatewayMode := kanpicai.ModeFormula
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"offline-test","context_length":16384}]}`))
			return
		}
		gatewayCalls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer internal-key" {
			t.Errorf("gateway request path/auth=%s/%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch gatewayMode {
		case kanpicai.ModeExplain:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"선택 수식 설명\",\"explanation\":\"A1의 값을 두 배로 계산합니다.\",\"changes\":[]}"}}]}`))
			return
		case kanpicai.ModeSummarize:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"선택 범위 요약\",\"explanation\":\"숫자 셀 하나가 있으며 값은 5입니다.\",\"findings\":[{\"row\":0,\"column\":0,\"severity\":\"info\",\"title\":\"데이터 구성\",\"description\":\"숫자 값 한 개가 있습니다.\"}],\"changes\":[]}"}}]}`))
			return
		case kanpicai.ModeAnomaly:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"이상치 탐지\",\"explanation\":\"A1을 확인해야 합니다.\",\"findings\":[{\"row\":1,\"column\":1,\"severity\":\"warning\",\"title\":\"검토 값\",\"description\":\"표본이 적어 수동 검토가 필요합니다.\"}],\"changes\":[]}"}}]}`))
			return
		case kanpicai.ModeClean:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"숫자 형식 정제\",\"explanation\":\"A1을 문자열 숫자로 표준화합니다.\",\"findings\":[],\"changes\":[{\"row\":1,\"column\":1,\"value\":\"5\"}]}"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"A1을 두 배로 계산\",\"explanation\":\"B1에 A1의 두 배 수식을 제안합니다.\",\"changes\":[{\"row\":1,\"column\":2,\"formula\":\"=A1*2\"}]}"}}]}`))
	}))
	defer gateway.Close()
	config := staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "offline-test",
		"ai.api_key": "internal-key", "ai.ca_pem": "", "ai.timeout_seconds": float64(5),
		"ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}
	service := kanpicai.NewService(pool, config, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	planInput := kanpicai.PlanInput{WorkbookID: book.ID, SheetID: sheetID, Range: "A1:B1", Request: "B1에 A1의 두 배 수식을 넣어줘", Mode: kanpicai.ModeFormula, BaseVersion: seed.ServerVersion, IdempotencyKey: "ai-plan", ClientID: "browser", ActorID: actor}
	planned, err := service.Plan(ctx, planInput)
	if err != nil || planned.Status != kanpicai.StatusPlanned || planned.Revision != 1 || len(planned.Changes) != 1 || planned.Changes[0].After.Formula != "=A1*2" {
		t.Fatalf("planned=%#v, %v", planned, err)
	}
	duplicatePlan, err := service.Plan(ctx, planInput)
	if err != nil || !duplicatePlan.Duplicate || duplicatePlan.ID != planned.ID || gatewayCalls != 1 {
		t.Fatalf("duplicate plan=%#v calls=%d err=%v", duplicatePlan, gatewayCalls, err)
	}
	changedRequest := planInput
	changedRequest.Request = "같은 키로 다른 계획"
	if _, err := service.Plan(ctx, changedRequest); !errors.Is(err, kanpicai.ErrInvalid) || gatewayCalls != 1 {
		t.Fatalf("reused idempotency key error=%v calls=%d", err, gatewayCalls)
	}
	if _, err := service.Get(ctx, planned.ID, "another-user"); !errors.Is(err, kanpicai.ErrNotFound) {
		t.Fatalf("cross-user get error=%v", err)
	}
	applied, err := service.Approve(ctx, planned.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "ai-approve", ExpectedRevision: 1})
	if err != nil || applied.Action.Status != kanpicai.StatusApplied || applied.Action.Revision != 2 || applied.Operation.ServerVersion != seed.ServerVersion+1 || applied.Operation.AppliedCells != 1 {
		t.Fatalf("applied=%#v, %v", applied, err)
	}
	duplicateApproval, err := service.Approve(ctx, planned.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "ai-approve", ExpectedRevision: 1})
	if err != nil || !duplicateApproval.Operation.Duplicate || duplicateApproval.Operation.OperationID != applied.Operation.OperationID {
		t.Fatalf("duplicate approval=%#v, %v", duplicateApproval, err)
	}
	selected, _ := cellrange.Parse("B1")
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || cells[0].Formula != "=A1*2" || string(cells[0].Value) != "10" {
		t.Fatalf("AI formula cells=%#v, %v", cells, err)
	}
	undone, err := service.Undo(ctx, planned.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "ai-undo", ExpectedRevision: 2})
	if err != nil || undone.Action.Status != kanpicai.StatusUndone || undone.Action.Revision != 3 || undone.Operation.ServerVersion != seed.ServerVersion+2 {
		t.Fatalf("undone=%#v, %v", undone, err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 0 {
		t.Fatalf("AI undo cells=%#v, %v", cells, err)
	}
	loaded, err := service.Get(ctx, planned.ID, actor)
	if err != nil || len(loaded.Events) != 3 || loaded.Events[0].ToolName != "range.read" || loaded.Events[1].ToolName != "formula.set" || loaded.Events[2].ToolName != "operation.undo" {
		t.Fatalf("AI events=%#v, %v", loaded.Events, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_type='ai_action' AND resource_id=$1`, planned.ID).Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("AI audit count=%d, %v", auditCount, err)
	}
	gatewayMode = kanpicai.ModeExplain
	explained, err := service.Plan(ctx, kanpicai.PlanInput{WorkbookID: book.ID, SheetID: sheetID, Range: "A1:B1", Request: "선택 수식을 설명해줘", Mode: kanpicai.ModeExplain, BaseVersion: undone.Operation.ServerVersion, IdempotencyKey: "ai-explain", ActorID: actor})
	gatewayMode = kanpicai.ModeFormula
	if err != nil || explained.Status != kanpicai.StatusCompleted || len(explained.Changes) != 0 || explained.Explanation == "" || len(explained.Events) != 1 || explained.Events[0].EventType != "completed" {
		t.Fatalf("explain action=%#v, %v", explained, err)
	}
	if _, err := service.Approve(ctx, explained.ID, kanpicai.ApprovalInput{ActorID: actor, IdempotencyKey: "ai-explain-approve", ExpectedRevision: 1}); !errors.Is(err, kanpicai.ErrInvalid) {
		t.Fatalf("explain approval error=%v", err)
	}
	gatewayMode = kanpicai.ModeSummarize
	summarized, err := service.Plan(ctx, kanpicai.PlanInput{WorkbookID: book.ID, SheetID: sheetID, Range: "A1:B1", Request: "선택 범위를 요약해줘", Mode: kanpicai.ModeSummarize, BaseVersion: undone.Operation.ServerVersion, IdempotencyKey: "ai-summary", ActorID: actor})
	gatewayMode = kanpicai.ModeAnomaly
	analyzed, anomalyErr := service.Plan(ctx, kanpicai.PlanInput{WorkbookID: book.ID, SheetID: sheetID, Range: "A1:B1", Request: "이상치를 찾아줘", Mode: kanpicai.ModeAnomaly, BaseVersion: undone.Operation.ServerVersion, IdempotencyKey: "ai-anomaly", ActorID: actor})
	gatewayMode = kanpicai.ModeFormula
	if err != nil || summarized.Status != kanpicai.StatusCompleted || len(summarized.Findings) != 1 || summarized.Findings[0].Address != "" {
		t.Fatalf("summary action=%#v, %v", summarized, err)
	}
	if anomalyErr != nil || analyzed.Status != kanpicai.StatusCompleted || len(analyzed.Findings) != 1 || analyzed.Findings[0].Address != "A1" || string(analyzed.Findings[0].Cell.Value) != "5" {
		t.Fatalf("anomaly action=%#v, %v", analyzed, anomalyErr)
	}

	gatewayMode = kanpicai.ModeClean
	cleaned, err := service.Plan(ctx, kanpicai.PlanInput{WorkbookID: book.ID, SheetID: sheetID, Range: "A1", Request: "숫자 형식을 정제해줘", Mode: kanpicai.ModeClean, BaseVersion: undone.Operation.ServerVersion, IdempotencyKey: "ai-clean", ActorID: actor})
	gatewayMode = kanpicai.ModeFormula
	if err != nil || cleaned.Status != kanpicai.StatusPlanned || len(cleaned.Changes) != 1 || string(cleaned.Changes[0].Before.Value) != "5" || string(cleaned.Changes[0].After.Value) != `"5"` {
		t.Fatalf("clean plan=%#v, %v", cleaned, err)
	}
	cleanApplied, err := service.Approve(ctx, cleaned.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "ai-clean-approve", ExpectedRevision: 1})
	if err != nil || cleanApplied.Action.Status != kanpicai.StatusApplied || cleanApplied.Operation.ServerVersion != undone.Operation.ServerVersion+1 {
		t.Fatalf("clean applied=%#v, %v", cleanApplied, err)
	}
	selectedA1, _ := cellrange.Parse("A1")
	cells, err = repository.ReadRange(ctx, sheetID, selectedA1)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != `"5"` || cells[0].Formula != "" {
		t.Fatalf("cleaned cells=%#v, %v", cells, err)
	}
	cleanUndone, err := service.Undo(ctx, cleaned.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "ai-clean-undo", ExpectedRevision: 2})
	if err != nil || cleanUndone.Action.Status != kanpicai.StatusUndone || cleanUndone.Operation.ServerVersion != undone.Operation.ServerVersion+2 {
		t.Fatalf("clean undone=%#v, %v", cleanUndone, err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selectedA1)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "5" {
		t.Fatalf("clean undo cells=%#v, %v", cells, err)
	}
	cleanHistory, err := service.Get(ctx, cleaned.ID, actor)
	if err != nil || len(cleanHistory.Events) != 3 || cleanHistory.Events[1].ToolName != "data.clean" || cleanHistory.Events[2].ToolName != "operation.undo" {
		t.Fatalf("clean events=%#v, %v", cleanHistory.Events, err)
	}

	stale, err := service.Plan(ctx, kanpicai.PlanInput{WorkbookID: book.ID, SheetID: sheetID, Range: "A1:B1", Request: "다시 수식을 제안해줘", Mode: kanpicai.ModeFormula, BaseVersion: cleanUndone.Operation.ServerVersion, IdempotencyKey: "ai-stale-plan", ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "other", ClientID: "other", BaseVersion: cleanUndone.Operation.ServerVersion, IdempotencyKey: "ai-unrelated-change", Cells: []workbook.CellInput{{Row: 2, Column: 1, Value: json.RawMessage(`9`)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Approve(ctx, stale.ID, kanpicai.ApprovalInput{ActorID: actor, IdempotencyKey: "ai-stale-approve", ExpectedRevision: 1})
	if !errors.Is(err, workbook.ErrVersionConflict) {
		t.Fatalf("stale approval error=%v", err)
	}
	stale, err = service.Get(ctx, stale.ID, actor)
	if err != nil || stale.Status != kanpicai.StatusPlanned || stale.Revision != 1 {
		t.Fatalf("stale action changed=%#v, %v", stale, err)
	}
}

func TestPostgresAutomationLifecycleTriggerUndoAndAudit(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("automation-user-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "automation lifecycle", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{
		SheetID: sheetID, ActorID: actor, ClientID: "browser", BaseVersion: 1, IdempotencyKey: "automation-seed",
		Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`3`)}, {Row: 2, Column: 1, Value: json.RawMessage(`4`)}},
	})
	if err != nil || seed.ServerVersion != 2 {
		t.Fatalf("seed=%#v, %v", seed, err)
	}
	config := staticAISettings{
		"automation.enabled":           true,
		"automation.max_cells_per_run": float64(1000),
		"automation.max_runs_per_hour": float64(1000),
	}
	service := automation.NewService(pool, config, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	createInput := automation.CreateInput{
		Name: "두 배 수식", Enabled: true, IdempotencyKey: "automation-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerManual},
		Action:  automation.ActionDefinition{Type: automation.ActionSetFormula, SheetID: sheetID, Range: "B1:B2", Formula: "=A1*2"},
	}
	item, err := service.Create(ctx, book.ID, actor, createInput)
	if err != nil || item.Revision != 1 || item.Action.Range != "B1:B2" {
		t.Fatalf("created=%#v, %v", item, err)
	}
	duplicate, err := service.Create(ctx, book.ID, actor, createInput)
	if err != nil || !duplicate.Duplicate || duplicate.ID != item.ID {
		t.Fatalf("duplicate create=%#v, %v", duplicate, err)
	}
	preview, err := service.Preview(ctx, item.ID)
	if err != nil || preview.BaseVersion != seed.ServerVersion || len(preview.Changes) != 2 || preview.Changes[1].After.Formula != "=A2*2" {
		t.Fatalf("preview=%#v, %v", preview, err)
	}
	selected, _ := cellrange.Parse("B1:B2")
	cells, err := repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 0 {
		t.Fatalf("preview wrote cells=%#v, %v", cells, err)
	}
	runInput := automation.RunInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "automation-run", ExpectedRevision: item.Revision, ExpectedBaseVersion: preview.BaseVersion}
	executed, err := service.Run(ctx, item.ID, runInput)
	if err != nil || executed.Run.Status != automation.StatusSucceeded || executed.Operation.ServerVersion != 3 || executed.Operation.AppliedCells != 2 {
		t.Fatalf("executed=%#v, %v", executed, err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 2 || cells[0].Formula != "=A1*2" || string(cells[0].Value) != "6" || cells[1].Formula != "=A2*2" || string(cells[1].Value) != "8" {
		t.Fatalf("formula cells=%#v, %v", cells, err)
	}
	duplicateRun, err := service.Run(ctx, item.ID, runInput)
	if err != nil || !duplicateRun.Run.Duplicate || duplicateRun.Operation.OperationID != executed.Operation.OperationID || duplicateRun.Operation.ServerVersion != 3 {
		t.Fatalf("duplicate run=%#v, %v", duplicateRun, err)
	}
	noChanges, err := service.Preview(ctx, item.ID)
	if err != nil || noChanges.BaseVersion != executed.Operation.ServerVersion || noChanges.AutomationRevision != item.Revision || len(noChanges.Changes) != 0 {
		t.Fatalf("no-change preview=%#v, %v", noChanges, err)
	}
	_, err = service.Run(ctx, item.ID, automation.RunInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "automation-stale-run", ExpectedRevision: item.Revision, ExpectedBaseVersion: preview.BaseVersion})
	if !errors.Is(err, workbook.ErrVersionConflict) {
		t.Fatalf("stale preview run error=%v", err)
	}
	runs, err := service.ListRuns(ctx, item.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].ID != executed.Run.ID {
		t.Fatalf("runs=%#v, %v", runs, err)
	}
	undone, err := service.Undo(ctx, executed.Run.ID, automation.RunInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "automation-undo"})
	if err != nil || undone.Run.Status != automation.StatusUndone || undone.Operation.ServerVersion != 4 || undone.Operation.AppliedCells != 2 {
		t.Fatalf("undone=%#v, %v", undone, err)
	}
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 0 {
		t.Fatalf("undo cells=%#v, %v", cells, err)
	}
	duplicateUndo, err := service.Undo(ctx, executed.Run.ID, automation.RunInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "automation-undo"})
	if err != nil || !duplicateUndo.Run.Duplicate || duplicateUndo.Operation.OperationID != undone.Operation.OperationID {
		t.Fatalf("duplicate undo=%#v, %v", duplicateUndo, err)
	}
	updated, err := service.Update(ctx, item.ID, actor, automation.UpdateInput{Name: item.Name, Enabled: true, Trigger: item.Trigger, Action: automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: sheetID, Range: "B1:B2", Value: json.RawMessage(`"완료"`)}, ExpectedRevision: 1})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("updated=%#v, %v", updated, err)
	}
	if _, err := service.Update(ctx, item.ID, actor, automation.UpdateInput{Name: item.Name, Enabled: true, Trigger: item.Trigger, Action: updated.Action, ExpectedRevision: 1}); !errors.Is(err, automation.ErrRevision) {
		t.Fatalf("stale update error=%v", err)
	}
	triggered, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "A3 변경 알림", Enabled: true, IdempotencyKey: "automation-trigger-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerCellChange, SheetID: sheetID, Range: "A3"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: sheetID, Range: "C3", Value: json.RawMessage(`"triggered"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	changeCells := []workbook.CellInput{{Row: 3, Column: 1, Value: json.RawMessage(`1`)}}
	changed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, ClientID: "browser", BaseVersion: undone.Operation.ServerVersion, IdempotencyKey: "automation-trigger-source", Cells: changeCells})
	if err != nil || changed.ServerVersion != 5 {
		t.Fatalf("trigger source=%#v, %v", changed, err)
	}
	triggerRuns, err := service.TriggerCellChange(ctx, changed, changeCells, actor)
	if err != nil || len(triggerRuns) != 1 || triggerRuns[0].Run.AutomationID != triggered.ID || triggerRuns[0].Operation.ServerVersion != 6 {
		t.Fatalf("trigger runs=%#v, %v", triggerRuns, err)
	}
	selected, _ = cellrange.Parse("C3")
	cells, err = repository.ReadRange(ctx, sheetID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != `"triggered"` {
		t.Fatalf("triggered cell=%#v, %v", cells, err)
	}
	triggerRuns, err = service.TriggerCellChange(ctx, changed, changeCells, actor)
	if err != nil || len(triggerRuns) != 1 || !triggerRuns[0].Run.Duplicate || triggerRuns[0].Operation.ServerVersion != 6 {
		t.Fatalf("duplicate trigger=%#v, %v", triggerRuns, err)
	}
	if err := service.Delete(ctx, triggered.ID, actor, triggered.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, triggered.ID); !errors.Is(err, automation.ErrNotFound) {
		t.Fatalf("deleted automation error=%v", err)
	}
	if err := service.Delete(ctx, item.ID, actor, updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, book.ID, actor, createInput); !errors.Is(err, automation.ErrRevision) {
		t.Fatalf("deleted automation idempotency replay error=%v", err)
	}
	items, err := service.List(ctx, book.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("automations after delete=%#v, %v", items, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_type='automation' AND actor_id=$1`, actor).Scan(&auditCount); err != nil || auditCount != 8 {
		t.Fatalf("automation audit count=%d, %v", auditCount, err)
	}
}

func TestPostgresScheduledAutomationRunsOnceAndSkipsNoChange(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("schedule-user-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "scheduled automation", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	config := staticAISettings{
		"automation.enabled":                true,
		"automation.max_cells_per_run":      float64(1000),
		"automation.max_runs_per_hour":      float64(1000),
		"automation.scheduler_poll_seconds": float64(5),
	}
	service := automation.NewService(pool, config, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disabled, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "중지된 예약", Enabled: false, IdempotencyKey: "disabled-schedule-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerSchedule, Cron: "0 * * * *", Timezone: "UTC"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "D1", Value: json.RawMessage(`"disabled"`)},
	})
	if err != nil || disabled.NextRunAt != nil {
		t.Fatalf("disabled schedule=%#v, %v", disabled, err)
	}
	item, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "평일 상태 갱신", Enabled: true, IdempotencyKey: "schedule-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerSchedule, Cron: "*/5 * * * MON-FRI", Timezone: "Asia/Seoul"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "B1", Value: json.RawMessage(`"scheduled"`)},
	})
	if err != nil || item.NextRunAt == nil || item.Trigger.Cron != "*/5 * * * MON-FRI" || item.Trigger.Timezone != "Asia/Seoul" {
		t.Fatalf("scheduled automation=%#v, %v", item, err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	firstDue := now.Add(-10 * time.Minute)
	deletedBook, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "deleted scheduled automation", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	deletedItem, err := service.Create(ctx, deletedBook.ID, actor, automation.CreateInput{
		Name: "삭제된 워크북 예약", Enabled: true, IdempotencyKey: "deleted-schedule-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerSchedule, Cron: "*/5 * * * *", Timezone: "UTC"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: deletedBook.Sheets[0].ID, Range: "A1", Value: json.RawMessage(`"must-not-run"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE automations SET next_run_at=$2 WHERE id=$1`, deletedItem.ID, firstDue); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteWorkbook(ctx, deletedBook.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE automations SET next_run_at=$2 WHERE id=$1`, item.ID, firstDue); err != nil {
		t.Fatal(err)
	}
	results, err := runDueForWorkbook(ctx, service, now, book.ID)
	if err != nil || len(results) != 1 || results[0].Run.Status != automation.StatusSucceeded || results[0].Run.ScheduledFor == nil || !results[0].Run.ScheduledFor.Equal(firstDue) || results[0].Operation.ServerVersion != 2 {
		t.Fatalf("first scheduled results=%#v, %v", results, err)
	}
	selected, _ := cellrange.Parse("B1")
	cells, err := repository.ReadRange(ctx, book.Sheets[0].ID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != `"scheduled"` {
		t.Fatalf("scheduled cells=%#v, %v", cells, err)
	}
	stored, err := service.Get(ctx, item.ID)
	if err != nil || stored.NextRunAt == nil || !stored.NextRunAt.After(now) {
		t.Fatalf("advanced schedule=%#v, %v", stored, err)
	}
	duplicateTick, err := runDueForWorkbook(ctx, service, now, book.ID)
	if err != nil || len(duplicateTick) != 0 {
		t.Fatalf("duplicate due tick=%#v, %v", duplicateTick, err)
	}
	secondDue := now.Add(-5 * time.Minute)
	if _, err := pool.Exec(ctx, `UPDATE automations SET next_run_at=$2 WHERE id=$1`, item.ID, secondDue); err != nil {
		t.Fatal(err)
	}
	skipped, err := runDueForWorkbook(ctx, service, now, book.ID)
	if err != nil || len(skipped) != 1 || skipped[0].Run.Status != automation.StatusSkipped || skipped[0].Operation.OperationID != "" {
		t.Fatalf("no-change schedule=%#v, %v", skipped, err)
	}
	after, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil || after.Version != 2 {
		t.Fatalf("skipped schedule changed workbook=%#v, %v", after, err)
	}
	runs, err := service.ListRuns(ctx, item.ID, 10)
	if err != nil || len(runs) != 2 || runs[0].Status != automation.StatusSkipped || runs[1].Status != automation.StatusSucceeded {
		t.Fatalf("scheduled run history=%#v, %v", runs, err)
	}
	backgroundItem, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "백그라운드 예약 실행", Enabled: true, IdempotencyKey: "schedule-background-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerSchedule, Cron: "0 * * * *", Timezone: "UTC"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "C1", Value: json.RawMessage(`"background"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	backgroundDue := now.Add(-time.Minute)
	if _, err := pool.Exec(ctx, `UPDATE automations SET next_run_at=$2 WHERE id=$1`, backgroundItem.ID, backgroundDue); err != nil {
		t.Fatal(err)
	}
	completed := make(chan automation.ExecutionResult, 1)
	service.SetScheduledExecutionListener(func(result automation.ExecutionResult) { completed <- result })
	schedulerContext, stopScheduler := context.WithCancel(ctx)
	schedulerDone := make(chan struct{})
	go func() {
		service.RunScheduler(schedulerContext)
		close(schedulerDone)
	}()
	select {
	case result := <-completed:
		if result.Run.AutomationID != backgroundItem.ID || result.Run.TriggerType != automation.TriggerSchedule || result.Run.Status != automation.StatusSucceeded {
			t.Fatalf("background schedule result=%#v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background scheduler did not execute the due automation")
	}
	stopScheduler()
	<-schedulerDone
	selected, _ = cellrange.Parse("C1")
	cells, err = repository.ReadRange(ctx, book.Sheets[0].ID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != `"background"` {
		t.Fatalf("background scheduled cells=%#v, %v", cells, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_type='automation' AND actor_id=$1 AND action IN ('automation.run','automation.run.skip') AND resource_id IN (SELECT id::text FROM automation_runs WHERE workbook_id=$2)`, "system:scheduler", book.ID).Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("scheduled audit count=%d, %v", auditCount, err)
	}
}

func TestPostgresWebhookAutomationStoresOnlyPayloadMetadataAndDeduplicates(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("webhook-user-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "webhook automation", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	keys := apikey.New(pool)
	key, err := keys.Create(ctx, actor, apikey.CreateInput{Name: "webhook integration", Scopes: []string{"automation.webhook.invoke"}})
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Revoke(context.Background(), key.ID, actor, false)
	service := automation.NewService(pool, staticAISettings{
		"automation.enabled":           true,
		"automation.max_cells_per_run": float64(1000),
		"automation.max_runs_per_hour": float64(1000),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	item, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "외부 승인 수신", Enabled: true, IdempotencyKey: "webhook-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerWebhook},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "B1", Value: json.RawMessage(`"received"`)},
	})
	if err != nil || item.Trigger.Type != automation.TriggerWebhook || item.NextRunAt != nil {
		t.Fatalf("webhook automation=%#v, %v", item, err)
	}
	payload := []byte(`{"event":"approved","sensitive":"not-persisted"}`)
	digest := sha256.Sum256(payload)
	digestText := hex.EncodeToString(digest[:])
	input := automation.RunInput{ActorID: actor, IdempotencyKey: "delivery-1", TriggerType: automation.TriggerWebhook, TriggerKeyID: key.ID, PayloadDigest: digestText, PayloadBytes: len(payload)}
	result, err := service.Run(ctx, item.ID, input)
	if err != nil || result.Run.Status != automation.StatusSucceeded || result.Run.TriggerKeyID != key.ID || result.Run.PayloadDigest != digestText || result.Run.PayloadBytes != len(payload) || result.Operation.ServerVersion != 2 {
		t.Fatalf("webhook result=%#v, %v", result, err)
	}
	duplicate, err := service.Run(ctx, item.ID, automation.RunInput{ActorID: actor, IdempotencyKey: "delivery-1", TriggerType: automation.TriggerWebhook, TriggerKeyID: key.ID, PayloadDigest: strings.Repeat("0", 64), PayloadBytes: 2})
	if err != nil || !duplicate.Run.Duplicate || duplicate.Run.ID != result.Run.ID || duplicate.Run.PayloadDigest != digestText {
		t.Fatalf("webhook duplicate=%#v, %v", duplicate, err)
	}
	skipped, err := service.Run(ctx, item.ID, automation.RunInput{ActorID: actor, IdempotencyKey: "delivery-2", TriggerType: automation.TriggerWebhook, TriggerKeyID: key.ID, PayloadDigest: digestText, PayloadBytes: len(payload)})
	if err != nil || skipped.Run.Status != automation.StatusSkipped || skipped.Operation.OperationID != "" {
		t.Fatalf("webhook no-change=%#v, %v", skipped, err)
	}
	runs, err := service.ListRuns(ctx, item.ID, 10)
	if err != nil || len(runs) != 2 || runs[0].Status != automation.StatusSkipped || runs[1].Status != automation.StatusSucceeded {
		t.Fatalf("webhook runs=%#v, %v", runs, err)
	}
	var storedDigest, storedKey string
	var storedBytes, rawPayloadMatches int
	if err := pool.QueryRow(ctx, `SELECT metadata->>'payload_digest',metadata->>'trigger_key_id',(metadata->>'payload_bytes')::int FROM audit_logs WHERE resource_type='automation' AND resource_id=$1 AND action='automation.run'`, result.Run.ID).Scan(&storedDigest, &storedKey, &storedBytes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE automation_id=$1 AND (action_snapshot::text LIKE '%not-persisted%' OR cells_snapshot::text LIKE '%not-persisted%' OR expected_snapshot::text LIKE '%not-persisted%')`, item.ID).Scan(&rawPayloadMatches); err != nil {
		t.Fatal(err)
	}
	if storedDigest != digestText || storedKey != key.ID || storedBytes != len(payload) || rawPayloadMatches != 0 {
		t.Fatalf("webhook audit digest=%q key=%q bytes=%d raw_matches=%d", storedDigest, storedKey, storedBytes, rawPayloadMatches)
	}
	_, err = service.Run(ctx, item.ID, automation.RunInput{ActorID: actor, IdempotencyKey: "missing-key", TriggerType: automation.TriggerWebhook, PayloadDigest: digestText, PayloadBytes: len(payload)})
	if !errors.Is(err, automation.ErrInvalid) {
		t.Fatalf("webhook missing key error=%v", err)
	}
}

func TestPostgresWorkbookAgentPlansExecutesValidatesAndRollsBackChangeSet(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 통합", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"월"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"8월"`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-test","context_length":16384}]}`))
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[1].Content, `"workbook_title": "Workbook Agent 통합"`) || !strings.Contains(body.Messages[1].Content, `"semantic_type": "revenue"`) {
			t.Errorf("gateway did not receive structured workbook context: %#v", body.Messages)
		}
		plan := `{"summary":"매출 요약과 차트 생성","explanation":"B2에 합계 수식을 넣고 선택 범위로 차트를 만듭니다.","findings":[],"changes":[{"row":2,"column":2,"formula":"=100+20"}],"tool_calls":[{"name":"create_chart","arguments":{"type":"bar","title":"월별 매출","source_range":"A1:B2"}}]}`
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 300, "completion_tokens": 100}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	messageInput := kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B2", Message: "분석하고 월별 매출 차트까지 만들어줘", Mode: kanpicai.ModeAgent, BaseVersion: seed.ServerVersion, IdempotencyKey: "agent-message", ClientID: "browser", ActorID: actor}
	run, err := service.SendMessage(ctx, messageInput)
	if err != nil || run.State != kanpicai.AgentWaitingApproval || run.Risk != kanpicai.RiskHigh || run.ChangeSetID == "" || len(run.Plan.Steps) < 4 || len(run.Action.ToolCalls) != 1 || len(run.Action.Changes) != 1 {
		t.Fatalf("planned agent run=%#v, %v", run, err)
	}
	if len(run.Messages) != 2 || run.Messages[0].Role != "user" || run.Messages[1].Role != "assistant" {
		t.Fatalf("conversation messages=%#v", run.Messages)
	}
	duplicateRun, err := service.SendMessage(ctx, messageInput)
	if err != nil || duplicateRun.ID != run.ID || duplicateRun.ConversationID != run.ConversationID || len(duplicateRun.Messages) != 2 {
		t.Fatalf("duplicate agent message=%#v, %v", duplicateRun, err)
	}
	executed, err := service.ApproveRun(ctx, run.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "agent-approve", ExpectedRevision: run.Action.Revision})
	if err != nil || executed.Run.State != kanpicai.AgentCompleted || !executed.Run.Validation.Passed || executed.Operation == nil || executed.Operation.AppliedCells != 1 {
		t.Fatalf("executed agent run=%#v, %v", executed, err)
	}
	cells, err := repository.ReadRange(ctx, sheet.ID, mustRange(t, "B2"))
	if err != nil || len(cells) != 1 || cells[0].Formula != "=100+20" {
		t.Fatalf("agent formula cells=%#v, %v", cells, err)
	}
	charts, err := repository.ListCharts(ctx, book.ID, sheet.ID)
	if err != nil || len(charts) != 1 || charts[0].SourceRange != "A1:B2" {
		t.Fatalf("agent chart=%#v, %v", charts, err)
	}
	var plans, steps, toolCalls, operations, audits int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_plans WHERE run_id=$1),(SELECT count(*) FROM agent_steps s JOIN agent_plans p ON p.id=s.plan_id WHERE p.run_id=$1),(SELECT count(*) FROM agent_tool_calls WHERE run_id=$1),(SELECT count(*) FROM change_operations o JOIN change_sets c ON c.id=o.change_set_id WHERE c.run_id=$1),(SELECT count(*) FROM agent_audit_logs WHERE run_id=$1)`, run.ID).Scan(&plans, &steps, &toolCalls, &operations, &audits); err != nil {
		t.Fatal(err)
	}
	if plans != 1 || steps < 4 || toolCalls < steps || operations != 2 || audits < 2 {
		t.Fatalf("agent audit plan=%d steps=%d tools=%d operations=%d audits=%d", plans, steps, toolCalls, operations, audits)
	}
	currentBook, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	rollbackMessage := kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B2", Message: "지난번 작업 취소해줘", BaseVersion: currentBook.Version, IdempotencyKey: "agent-rollback-message", ClientID: "browser", ActorID: actor}
	rolledBackRun, err := service.SendMessage(ctx, rollbackMessage)
	if err != nil || rolledBackRun.Action.Status != kanpicai.StatusUndone || len(rolledBackRun.Messages) != 4 || rolledBackRun.Messages[3].Content != "최근 Workbook Agent ChangeSet을 전체 원복했습니다." {
		t.Fatalf("message rollback run=%#v, %v", rolledBackRun, err)
	}
	duplicateRollback, err := service.SendMessage(ctx, rollbackMessage)
	if err != nil || duplicateRollback.ID != run.ID || len(duplicateRollback.Messages) != 4 {
		t.Fatalf("duplicate message rollback=%#v, %v", duplicateRollback, err)
	}
	cells, _ = repository.ReadRange(ctx, sheet.ID, mustRange(t, "B2"))
	charts, _ = repository.ListCharts(ctx, book.ID, sheet.ID)
	if len(cells) != 0 || len(charts) != 0 {
		t.Fatalf("rollback left cells=%#v charts=%#v", cells, charts)
	}
}

func TestPostgresWorkbookAgentContinuesConversationAndUpdatesExistingChart(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-chat-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 멀티턴", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "chat-agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"월"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"8월"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`120`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	postCalls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-chat-test","context_length":16384}]}`))
			return
		}
		postCalls++
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		content := ""
		if len(body.Messages) > 1 {
			content = body.Messages[1].Content
		}
		plan := `{"summary":"막대 차트 생성","explanation":"선택 범위를 막대 차트로 만듭니다.","findings":[],"changes":[],"tool_calls":[{"name":"create_chart","arguments":{"type":"bar","title":"월별 매출","source_range":"A1:B2"}}]}`
		if postCalls == 2 {
			charts, listErr := repository.ListCharts(ctx, book.ID, sheet.ID)
			if listErr != nil || len(charts) != 1 {
				t.Errorf("chart inventory before follow-up=%#v err=%v", charts, listErr)
				return
			}
			if !strings.Contains(content, `"conversation_history"`) || !strings.Contains(content, "선택 범위로 막대 차트를 만들어줘") || !strings.Contains(content, `"conversation_work_memory"`) || !strings.Contains(content, `"status": "applied"`) || !strings.Contains(content, `"name": "create_chart"`) || !strings.Contains(content, charts[0].ID) || !strings.Contains(content, `"type": "bar"`) {
				t.Errorf("follow-up prompt is missing conversation or chart inventory: %s", content)
			}
			plan = fmt.Sprintf(`{"summary":"선 차트로 변경","explanation":"앞서 만든 차트의 유형만 선 차트로 변경합니다.","findings":[],"changes":[],"tool_calls":[{"name":"update_chart","arguments":{"chart_id":%q,"type":"line"}}]}`, charts[0].ID)
		}
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 300, "completion_tokens": 100}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-chat-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	first, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B2", Message: "선택 범위로 막대 차트를 만들어줘", BaseVersion: seed.ServerVersion, IdempotencyKey: "chat-agent-first", ClientID: "browser", ActorID: actor})
	if err != nil || first.Action.Mode != kanpicai.ModeChart || first.ConversationID == "" || len(first.Action.ToolCalls) != 1 || first.Action.ToolCalls[0].Name != "create_chart" {
		t.Fatalf("first chart turn=%#v err=%v", first, err)
	}
	created, err := service.ApproveRun(ctx, first.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "chat-agent-first-approve", ExpectedRevision: first.Action.Revision})
	if err != nil || !created.Run.Validation.Passed {
		t.Fatalf("first chart approval=%#v err=%v", created, err)
	}
	conversations, err := service.ListConversations(ctx, book.ID, actor, 20)
	if err != nil || len(conversations) != 1 || conversations[0].ID != first.ConversationID || conversations[0].LatestRunID != first.ID || conversations[0].MessageCount != 2 || conversations[0].RunCount != 1 {
		t.Fatalf("conversation index=%#v err=%v", conversations, err)
	}
	current, _ := repository.GetWorkbook(ctx, book.ID)
	second, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B2", Message: "막대 차트를 선 차트로 바꿔줘", ConversationID: first.ConversationID, BaseVersion: current.Version, IdempotencyKey: "chat-agent-second", ClientID: "browser", ActorID: actor})
	if err != nil || second.ConversationID != first.ConversationID || len(second.Messages) != 4 || len(second.Action.ToolCalls) != 1 || second.Action.ToolCalls[0].Name != "update_chart" {
		t.Fatalf("follow-up chart turn=%#v err=%v", second, err)
	}
	updated, err := service.ApproveRun(ctx, second.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "chat-agent-second-approve", ExpectedRevision: second.Action.Revision})
	if err != nil || !updated.Run.Validation.Passed {
		t.Fatalf("follow-up chart approval=%#v err=%v", updated, err)
	}
	charts, err := repository.ListCharts(ctx, book.ID, sheet.ID)
	if err != nil || len(charts) != 1 || charts[0].Type != "line" || charts[0].Revision != 2 {
		t.Fatalf("updated chart=%#v err=%v", charts, err)
	}
	rolledBack, err := service.RollbackChangeSet(ctx, second.ChangeSetID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "chat-agent-second-rollback", ExpectedRevision: updated.Run.Action.Revision})
	if err != nil || rolledBack.Run.Action.Status != kanpicai.StatusUndone {
		t.Fatalf("follow-up rollback=%#v err=%v", rolledBack, err)
	}
	charts, err = repository.ListCharts(ctx, book.ID, sheet.ID)
	if err != nil || len(charts) != 1 || charts[0].Type != "bar" || charts[0].Revision != 3 {
		t.Fatalf("restored chart=%#v err=%v", charts, err)
	}
}

func TestPostgresWorkbookAgentCreatesAndRollsBackReportSheetFormulaAndChart(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-report-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 보고서", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	source := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: source.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "report-agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"월"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"8월"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`120`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-report-test","context_length":16384}]}`))
			return
		}
		plan := `{"summary":"경영 보고서 생성","explanation":"새 시트에 요약 수식과 차트를 만듭니다.","findings":[],"changes":[],"tool_calls":[{"name":"create_report_sheet","arguments":{"name":"경영 보고","cells":[{"row":1,"column":1,"value":"월"},{"row":1,"column":2,"value":"매출"},{"row":2,"column":1,"value":"8월"},{"row":2,"column":2,"formula":"=100+20"}],"chart":{"type":"bar","title":"월별 매출","source_range":"A1:B2"}}}]}`
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 320, "completion_tokens": 120}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-report-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	run, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: source.ID, Selection: "A1:B2", Message: "신규 시트에 수식과 차트가 있는 경영 보고서를 만들어줘", Mode: kanpicai.ModeAgent, BaseVersion: seed.ServerVersion, IdempotencyKey: "report-agent-message", ClientID: "browser", ActorID: actor})
	if err != nil || run.State != kanpicai.AgentWaitingApproval || len(run.Action.Changes) != 0 || len(run.Action.ToolCalls) != 1 || run.Action.ToolCalls[0].Name != "create_report_sheet" {
		t.Fatalf("report agent plan=%#v, %v", run, err)
	}
	executed, err := service.ApproveRun(ctx, run.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "report-agent-approve", ExpectedRevision: run.Action.Revision})
	if err != nil || executed.Run.State != kanpicai.AgentCompleted || !executed.Run.Validation.Passed || executed.Operation != nil {
		t.Fatalf("report agent execution=%#v, %v", executed, err)
	}
	updated, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil || len(updated.Sheets) != 2 || updated.Sheets[1].Name != "경영 보고" {
		t.Fatalf("report workbook=%#v, %v", updated, err)
	}
	reportSheet := updated.Sheets[1]
	cells, err := repository.ReadRange(ctx, reportSheet.ID, mustRange(t, "A1:B2"))
	if err != nil || len(cells) != 4 || cells[3].Formula != "=100+20" || string(cells[3].Value) != "120" {
		t.Fatalf("report cells=%#v, %v", cells, err)
	}
	charts, err := repository.ListCharts(ctx, book.ID, reportSheet.ID)
	if err != nil || len(charts) != 1 || charts[0].SourceRange != "A1:B2" {
		t.Fatalf("report charts=%#v, %v", charts, err)
	}
	rolledBack, err := service.RollbackChangeSet(ctx, run.ChangeSetID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "report-agent-rollback", ExpectedRevision: executed.Run.Action.Revision})
	if err != nil || rolledBack.Run.Action.Status != kanpicai.StatusUndone {
		t.Fatalf("report rollback=%#v, %v", rolledBack, err)
	}
	updated, err = repository.GetWorkbook(ctx, book.ID)
	if err != nil || len(updated.Sheets) != 1 || updated.Sheets[0].ID != source.ID {
		t.Fatalf("report rollback workbook=%#v, %v", updated, err)
	}
	var changeSetStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM change_sets WHERE id=$1`, run.ChangeSetID).Scan(&changeSetStatus); err != nil || changeSetStatus != "rolled_back" {
		t.Fatalf("report changeset status=%q, %v", changeSetStatus, err)
	}
}

func TestPostgresAutomationRateLimitIsAtomicAndSnapshotIsBounded(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	baseRepository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("automation-limit-user-%d", time.Now().UnixNano())
	book, err := baseRepository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "automation atomic limit", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer baseRepository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	repository := &barrierRepository{Repository: baseRepository, reached: make(chan struct{}, 2), release: make(chan struct{})}
	service := automation.NewService(pool, staticAISettings{
		"automation.enabled":                true,
		"automation.max_cells_per_run":      float64(1000),
		"automation.max_runs_per_hour":      float64(1),
		"automation.scheduler_poll_seconds": float64(15),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))

	definitions := make([]automation.Automation, 0, 2)
	for index, address := range []string{"A1", "B1"} {
		item, createErr := service.Create(ctx, book.ID, actor, automation.CreateInput{
			Name: fmt.Sprintf("동시 실행 %d", index+1), Enabled: true, IdempotencyKey: fmt.Sprintf("atomic-create-%d", index+1),
			Trigger: automation.TriggerDefinition{Type: automation.TriggerManual},
			Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: address, Value: json.RawMessage(fmt.Sprintf("%d", index+1))},
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		definitions = append(definitions, item)
	}

	errorsByRun := make(chan error, 2)
	for index, item := range definitions {
		go func(index int, item automation.Automation) {
			_, runErr := service.Run(ctx, item.ID, automation.RunInput{
				ActorID: actor, IdempotencyKey: fmt.Sprintf("atomic-run-%d", index+1), ExpectedRevision: item.Revision, ExpectedBaseVersion: book.Version,
			})
			errorsByRun <- runErr
		}(index, item)
	}
	for range definitions {
		select {
		case <-repository.reached:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(repository.release)
	succeeded, rateLimited := 0, 0
	for range definitions {
		runErr := <-errorsByRun
		switch {
		case runErr == nil:
			succeeded++
		case errors.Is(runErr, automation.ErrRate):
			rateLimited++
		default:
			t.Fatalf("unexpected concurrent run error=%v", runErr)
		}
	}
	var runCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE workbook_id=$1`, book.ID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || rateLimited != 1 || runCount != 1 {
		t.Fatalf("atomic rate limit succeeded=%d rate_limited=%d stored_runs=%d", succeeded, rateLimited, runCount)
	}

	// A scheduler rejection is useful history, but it must not itself consume a
	// slot and extend the rate-limit window indefinitely.
	scheduled, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "제한 후 재시도", Enabled: true, IdempotencyKey: "rate-schedule-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerSchedule, Cron: "* * * * *", Timezone: "UTC"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "D1", Value: json.RawMessage(`"scheduled"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	firstDue := now.Add(-time.Minute)
	if _, err := pool.Exec(ctx, `UPDATE automations SET next_run_at=$2 WHERE id=$1`, scheduled.ID, firstDue); err != nil {
		t.Fatal(err)
	}
	limitedResults, limitedErr := runDueForWorkbook(ctx, service, now, book.ID)
	if !errors.Is(limitedErr, automation.ErrRate) || len(limitedResults) != 1 || limitedResults[0].Run.Status != automation.StatusFailed {
		t.Fatalf("rate-limited schedule results=%#v error=%v", limitedResults, limitedErr)
	}
	var excludedRuns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE automation_id=$1 AND NOT counts_toward_rate`, scheduled.ID).Scan(&excludedRuns); err != nil || excludedRuns != 1 {
		t.Fatalf("rate-excluded schedule runs=%d, %v", excludedRuns, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE automation_runs SET started_at=$2 WHERE workbook_id=$1 AND counts_toward_rate`, book.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	secondDue := now.Add(-30 * time.Second)
	if _, err := pool.Exec(ctx, `UPDATE automations SET next_run_at=$2 WHERE id=$1`, scheduled.ID, secondDue); err != nil {
		t.Fatal(err)
	}
	retried, retryErr := runDueForWorkbook(ctx, service, now, book.ID)
	if retryErr != nil || len(retried) != 1 || retried[0].Run.Status != automation.StatusSucceeded {
		t.Fatalf("schedule after expired admitted run=%#v, %v", retried, retryErr)
	}

	largeValue, _ := json.Marshal(strings.Repeat("가", 10_000))
	large, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "확장 스냅샷 제한", Enabled: true, IdempotencyKey: "large-snapshot-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerManual},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "C1:C1000", Value: largeValue},
	})
	if err != nil {
		t.Fatal(err)
	}
	// This preview intentionally bypasses the synchronization barrier used only
	// by the concurrent run check above.
	boundedService := automation.NewService(pool, staticAISettings{
		"automation.enabled":           true,
		"automation.max_cells_per_run": float64(1000),
		"automation.max_runs_per_hour": float64(1),
	}, baseRepository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := boundedService.Preview(ctx, large.ID); !errors.Is(err, automation.ErrInvalid) || !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("expanded snapshot error=%v", err)
	}
}

func TestPostgresAutomationRunRecoversAmbiguousApplyAndClosesAdmissionRaces(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	baseRepository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("automation-recovery-user-%d", time.Now().UnixNano())
	book, err := baseRepository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "automation recovery", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer baseRepository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	settings := staticAISettings{
		"automation.enabled":                true,
		"automation.max_cells_per_run":      float64(1000),
		"automation.max_runs_per_hour":      float64(100),
		"automation.scheduler_poll_seconds": float64(15),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ambiguousRepository := &ambiguousApplyRepository{Repository: baseRepository, failOnce: true}
	ambiguousService := automation.NewService(pool, settings, ambiguousRepository, logger)
	ambiguous, err := ambiguousService.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "응답 유실 복구", Enabled: true, IdempotencyKey: "ambiguous-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerManual},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "A1", Value: json.RawMessage(`5`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := ambiguousService.Preview(ctx, ambiguous.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := automation.RunInput{ActorID: actor, IdempotencyKey: "ambiguous-run", ExpectedRevision: preview.AutomationRevision, ExpectedBaseVersion: preview.BaseVersion}
	if _, err := ambiguousService.Run(ctx, ambiguous.ID, input); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first ambiguous run error=%v", err)
	}
	running, err := ambiguousService.ListRuns(ctx, ambiguous.ID, 10)
	if err != nil || len(running) != 1 || running[0].Status != automation.StatusRunning || running[0].ErrorMessage == "" {
		t.Fatalf("retryable run history=%#v, %v", running, err)
	}
	recovered, err := ambiguousService.Run(ctx, ambiguous.ID, input)
	if err != nil || recovered.Run.Status != automation.StatusSucceeded || recovered.Operation.OperationID == "" || !recovered.Operation.Duplicate {
		t.Fatalf("recovered ambiguous run=%#v, %v", recovered, err)
	}
	var operationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cell_operations WHERE workbook_id=$1 AND operation_type='automation.set_value'`, book.ID).Scan(&operationCount); err != nil || operationCount != 1 {
		t.Fatalf("ambiguous operation count=%d, %v", operationCount, err)
	}

	revisionBarrier := &barrierRepository{Repository: baseRepository, reached: make(chan struct{}, 1), release: make(chan struct{})}
	revisionService := automation.NewService(pool, settings, revisionBarrier, logger)
	revisionItem, err := revisionService.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "정의 경합", Enabled: true, IdempotencyKey: "revision-race-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerManual},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "B1", Value: json.RawMessage(`"old"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	bookAfterAmbiguous, err := baseRepository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	revisionError := make(chan error, 1)
	go func() {
		_, runErr := revisionService.Run(ctx, revisionItem.ID, automation.RunInput{ActorID: actor, IdempotencyKey: "revision-race-run", ExpectedRevision: revisionItem.Revision, ExpectedBaseVersion: bookAfterAmbiguous.Version})
		revisionError <- runErr
	}()
	select {
	case <-revisionBarrier.reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := revisionService.Update(ctx, revisionItem.ID, actor, automation.UpdateInput{
		Name: revisionItem.Name, Enabled: true, ExpectedRevision: revisionItem.Revision,
		Trigger: revisionItem.Trigger,
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "B1", Value: json.RawMessage(`"new"`)},
	}); err != nil {
		t.Fatal(err)
	}
	close(revisionBarrier.release)
	if err := <-revisionError; !errors.Is(err, automation.ErrRevision) {
		t.Fatalf("definition admission race error=%v", err)
	}
	var staleRunCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE automation_id=$1`, revisionItem.ID).Scan(&staleRunCount); err != nil || staleRunCount != 0 {
		t.Fatalf("stale definition stored runs=%d, %v", staleRunCount, err)
	}
	latestRevisionItem, err := revisionService.Get(ctx, revisionItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	latestRevisionPreview, err := revisionService.Preview(ctx, revisionItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revisionService.Update(ctx, revisionItem.ID, actor, automation.UpdateInput{
		Name: latestRevisionItem.Name, Enabled: false, ExpectedRevision: latestRevisionItem.Revision,
		Trigger: latestRevisionItem.Trigger, Action: latestRevisionItem.Action,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = revisionService.Run(ctx, revisionItem.ID, automation.RunInput{ActorID: actor, IdempotencyKey: "stale-disabled-run", ExpectedRevision: latestRevisionPreview.AutomationRevision, ExpectedBaseVersion: latestRevisionPreview.BaseVersion})
	if !errors.Is(err, automation.ErrRevision) {
		t.Fatalf("stale disabled definition must report revision conflict, got %v", err)
	}

	currentBook, err := baseRepository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := baseRepository.ApplyCells(ctx, workbook.CellMutation{SheetID: book.Sheets[0].ID, ActorID: actor, BaseVersion: currentBook.Version, IdempotencyKey: "trigger-race-source", Cells: []workbook.CellInput{{Row: 1, Column: 3, Value: json.RawMessage(`1`)}}})
	if err != nil {
		t.Fatal(err)
	}
	applyBarrier := &firstApplyBarrierRepository{Repository: baseRepository, reached: make(chan struct{}), release: make(chan struct{})}
	triggerService := automation.NewService(pool, settings, applyBarrier, logger)
	triggerItem, err := triggerService.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "트리거 경합", Enabled: true, IdempotencyKey: "trigger-race-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerCellChange, SheetID: book.Sheets[0].ID, Range: "C1"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: book.Sheets[0].ID, Range: "D1", Value: json.RawMessage(`"triggered"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	triggerInput := func(runActor, key string) automation.RunInput {
		return automation.RunInput{ActorID: runActor, IdempotencyKey: key, ExpectedRevision: triggerItem.Revision, TriggerType: automation.TriggerCellChange, TriggerOperationID: source.OperationID}
	}
	firstResult := make(chan struct {
		result automation.ExecutionResult
		err    error
	}, 1)
	go func() {
		result, runErr := triggerService.Run(ctx, triggerItem.ID, triggerInput(actor, "trigger-race-first"))
		firstResult <- struct {
			result automation.ExecutionResult
			err    error
		}{result: result, err: runErr}
	}()
	select {
	case <-applyBarrier.reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second, secondErr := triggerService.Run(ctx, triggerItem.ID, triggerInput(actor+"-other", "trigger-race-second"))
	if secondErr != nil || second.Operation.OperationID == "" {
		t.Fatalf("running trigger duplicate recovery=%#v, %v", second, secondErr)
	}
	close(applyBarrier.release)
	first := <-firstResult
	if first.err != nil || first.result.Operation.OperationID == "" || first.result.Operation.OperationID != second.Operation.OperationID {
		t.Fatalf("first trigger race result=%#v, %v; second=%#v", first.result, first.err, second)
	}
	var triggerRunCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE automation_id=$1`, triggerItem.ID).Scan(&triggerRunCount); err != nil || triggerRunCount != 1 {
		t.Fatalf("trigger race stored runs=%d, %v", triggerRunCount, err)
	}
}

func mustRange(t *testing.T, value string) cellrange.Range {
	t.Helper()
	selected, err := cellrange.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

// TestPostgresEntityFieldsSurviveARoundTrip writes every optional field of the
// entities that own their own tables and reads each one back from the database.
// Two shipped bugs had the same shape — a new field reached the struct and the
// API but never the INSERT — and neither was visible without a read-back:
// the create response was built in memory and looked right.
func TestPostgresEntityFieldsSurviveARoundTrip(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "round trip", WorkspaceID: "integration", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "round-trip-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"항목"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"값"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"가"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`10`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"나"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`20`)},
	}}); err != nil {
		t.Fatal(err)
	}

	yes := true
	chart, err := repository.CreateChart(ctx, book.ID, "owner", workbook.CreateChartInput{
		IdempotencyKey: "rt-chart", SheetID: sheetID, SourceSheetID: sheetID, Type: "combo", Title: "왕복",
		SourceRange: "A1:B3", FirstRowHeaders: &yes, FirstColumnLabels: &yes, LegendPosition: "bottom",
		XAxisTitle: "가로", YAxisTitle: "세로", SecondaryAxis: &yes,
		Position: &workbook.ChartPosition{X: 11, Y: 22, Width: 333, Height: 244},
	})
	if err != nil {
		t.Fatal(err)
	}
	storedChart, err := repository.GetChart(ctx, chart.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedChart.Type != "combo" || !storedChart.SecondaryAxis || storedChart.LegendPosition != "bottom" ||
		storedChart.XAxisTitle != "가로" || storedChart.YAxisTitle != "세로" ||
		storedChart.Position != (workbook.ChartPosition{X: 11, Y: 22, Width: 333, Height: 244}) {
		t.Fatalf("chart round trip = %#v", storedChart)
	}

	protection, err := repository.CreateProtectedRange(ctx, sheetID, "owner", workbook.CreateProtectedRangeInput{
		IdempotencyKey: "rt-protection", Scope: "sheet", Exceptions: []string{"B2:B9"},
		Description: "왕복 보호", Editors: []string{"mate"}, WarningOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	protections, err := repository.ListProtectedRanges(ctx, sheetID)
	if err != nil {
		t.Fatal(err)
	}
	var storedProtection workbook.ProtectedRange
	for _, item := range protections {
		if item.ID == protection.ID {
			storedProtection = item
		}
	}
	if storedProtection.Scope != "sheet" || len(storedProtection.Exceptions) != 1 || storedProtection.Exceptions[0] != "B2:B9" ||
		!storedProtection.WarningOnly || storedProtection.Description != "왕복 보호" || len(storedProtection.Editors) != 1 {
		t.Fatalf("protection round trip = %#v", storedProtection)
	}

	rule, err := repository.CreateConditionalFormat(ctx, sheetID, "owner", workbook.CreateConditionalFormatInput{
		IdempotencyKey: "rt-conditional", Name: "왕복 규칙", Range: "A2:B3", RuleType: "custom_formula",
		Formula: `=$B2>15`, Style: json.RawMessage(`{"background":"#dcfce7"}`), Priority: 7, StopIfTrue: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	storedRule, err := repository.GetConditionalFormat(ctx, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRule.RuleType != "custom_formula" || storedRule.Formula != `=$B2>15` || storedRule.Priority != 7 || !storedRule.StopIfTrue {
		t.Fatalf("conditional format round trip = %#v", storedRule)
	}

	view, err := repository.CreateFilterView(ctx, sheetID, "owner", workbook.CreateFilterViewInput{
		IdempotencyKey: "rt-filter", Name: "왕복 필터", Range: "A1:B3", HeaderRows: 1, Active: true,
		Criteria: []workbook.FilterCriterion{{Column: 2, Operator: "greater_than", Value: json.RawMessage(`15`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	storedView, err := repository.GetFilterView(ctx, view.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !storedView.Active || storedView.HeaderRows != 1 || len(storedView.Criteria) != 1 || storedView.Criteria[0].Operator != "greater_than" {
		t.Fatalf("filter view round trip = %#v", storedView)
	}

	// A slicer lives in the sheet layout rather than a table of its own, so the
	// round trip that matters is the layout blob.
	layout, err := repository.ApplySheetLayout(ctx, workbook.SheetLayoutMutation{
		SheetID: sheetID, ActorID: "owner", IdempotencyKey: "rt-slicer", ExpectedRevision: 1, Action: "slicer_add",
		Slicer: &workbook.Slicer{FilterViewID: view.ID, Column: 2, Title: "왕복 슬라이서", Position: workbook.ChartPosition{X: 5, Y: 6, Width: 210, Height: 250}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	var storedSlicers []workbook.Slicer
	for _, sheet := range stored.Sheets {
		if sheet.ID == sheetID {
			storedSlicers = sheet.Layout.Slicers
		}
	}
	if len(storedSlicers) != 1 || storedSlicers[0].Title != "왕복 슬라이서" || storedSlicers[0].Column != 2 ||
		storedSlicers[0].FilterViewID != view.ID || storedSlicers[0].Position.Width != 210 {
		t.Fatalf("slicer round trip = %#v (layout %#v)", storedSlicers, layout.Layout)
	}
}

// Writing one cell used to read and lock every block in the workbook, because
// a formula anywhere might depend on it. Skipping that read when nothing can
// depend on the write is only safe if "nothing can depend on it" is judged
// correctly, so this checks both sides of the judgement.
func TestPostgresNarrowWriteStillRecalculatesAndValidates(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "narrow write", OwnerID: "narrow-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0].ID
	seed := make([]workbook.CellInput, 0, 200)
	for row := 1; row <= 200; row++ {
		seed = append(seed, workbook.CellInput{Row: row, Column: 1, Value: json.RawMessage(`1`)})
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet, ActorID: "narrow-user", IdempotencyKey: "narrow-seed", Cells: seed}); err != nil {
		t.Fatal(err)
	}
	// A write with no formulas anywhere reads only its own block. The value
	// still has to land and its neighbours still have to survive.
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet, ActorID: "narrow-user", IdempotencyKey: "narrow-one",
		Cells: []workbook.CellInput{{Row: 150, Column: 1, Value: json.RawMessage(`99`)}}}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A149:A151")
	read, err := repository.ReadRange(ctx, sheet, selected)
	if err != nil || len(read) != 3 {
		t.Fatalf("neighbours = %#v, %v", read, err)
	}
	for _, cell := range read {
		want := "1"
		if cell.Row == 150 {
			want = "99"
		}
		if string(cell.Value) != want {
			t.Errorf("A%d = %s, want %s", cell.Row, cell.Value, want)
		}
	}
	// A formula three blocks away must still see a change to what it reads.
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet, ActorID: "narrow-user", IdempotencyKey: "narrow-formula",
		Cells: []workbook.CellInput{{Row: 1, Column: 3, Formula: "=A200*2"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet, ActorID: "narrow-user", IdempotencyKey: "narrow-input",
		Cells: []workbook.CellInput{{Row: 200, Column: 1, Value: json.RawMessage(`21`)}}}); err != nil {
		t.Fatal(err)
	}
	target, _ := cellrange.Parse("C1")
	computed, err := repository.ReadRange(ctx, sheet, target)
	if err != nil || len(computed) != 1 || string(computed[0].Value) != "42" {
		t.Fatalf("C1 = %#v, %v (the formula did not follow its input)", computed, err)
	}
	// A validation rule carries its own condition and lives outside the cell
	// blocks, so it also has to force the wider read.
	if _, err := repository.CreateDataValidation(ctx, sheet, "narrow-user", workbook.CreateDataValidationInput{IdempotencyKey: "narrow-rule",
		Range: "E1:E5", RuleType: "number", Operator: "greater_than", Value: json.RawMessage(`10`)}); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet, ActorID: "narrow-user", IdempotencyKey: "narrow-violation",
		Cells: []workbook.CellInput{{Row: 1, Column: 5, Value: json.RawMessage(`3`)}}})
	if err == nil && len(result.ValidationWarnings) == 0 {
		t.Fatalf("a value below the rule was accepted without a word: %#v", result)
	}
}

// The very first formula in a workbook is written when no formula exists yet,
// so the write that introduces it must already read widely — otherwise it is
// calculated against a sheet it cannot see.
func TestPostgresFirstFormulaSeesTheWholeSheet(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "first formula", OwnerID: "first-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0].ID
	other, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "Data"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: other.ID, ActorID: "first-user", IdempotencyKey: "first-seed",
		Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`7`)}}}); err != nil {
		t.Fatal(err)
	}
	// Far enough away to be in another block, and on another sheet.
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet, ActorID: "first-user", IdempotencyKey: "first-formula",
		Cells: []workbook.CellInput{{Row: 300, Column: 1, Formula: "=Data!A1*3"}}}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A300")
	read, err := repository.ReadRange(ctx, sheet, selected)
	if err != nil || len(read) != 1 || string(read[0].Value) != "21" {
		t.Fatalf("A300 = %#v, %v (the first formula did not see the other sheet)", read, err)
	}
}

// A cell-change trigger runs with nobody watching. When one failed before it
// reached a run row, the failure existed only in the server log: the panel's
// history stayed empty and the editor who typed the cell was told nothing. The
// admin switch was invisible the same way — definitions saved and previewed
// while every trigger silently did nothing.
func TestPostgresAutomationOverviewReportsTheGlobalSwitchAndFailedTriggers(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("automation-visibility-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "automation visibility", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheetID := book.Sheets[0].ID
	config := staticAISettings{"automation.enabled": true, "automation.max_cells_per_run": float64(1000), "automation.max_runs_per_hour": float64(1000)}
	service := automation.NewService(pool, config, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	item, err := service.Create(ctx, book.ID, actor, automation.CreateInput{
		Name: "A열 감시", Enabled: true, IdempotencyKey: "visibility-create",
		Trigger: automation.TriggerDefinition{Type: automation.TriggerCellChange, SheetID: sheetID, Range: "A1:A5"},
		Action:  automation.ActionDefinition{Type: automation.ActionSetValue, SheetID: sheetID, Range: "C1:C5", Value: json.RawMessage(`"복사됨"`)},
	})
	if err != nil {
		t.Fatal(err)
	}

	change := func(row int, value string, baseVersion int64, key string) workbook.MutationResult {
		t.Helper()
		result, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, BaseVersion: baseVersion, IdempotencyKey: key, Cells: []workbook.CellInput{{Row: row, Column: 1, Value: json.RawMessage(value)}}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := change(1, `10`, book.Version, "visibility-first")
	if _, err := service.TriggerCellChange(ctx, first, []workbook.CellInput{{Row: 1, Column: 1}}, actor); err != nil {
		t.Fatalf("healthy trigger: %v", err)
	}
	if first, err := service.ListRuns(ctx, item.ID, 10); err != nil || len(first) != 1 || first[0].Status != automation.StatusSucceeded {
		t.Fatalf("healthy run history: %#v, %v", first, err)
	}
	healthy, err := service.Overview(ctx, book.ID)
	if err != nil || !healthy.ExecutionEnabled || len(healthy.Items) != 1 || healthy.Items[0].LastFailure != nil {
		t.Fatalf("overview after a successful run: %#v, %v", healthy, err)
	}

	// Lowering the limit after the definition was saved is the realistic way an
	// automation starts failing: the definition itself is still valid.
	config["automation.max_cells_per_run"] = float64(1)
	current, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Clearing the target puts the five cells back in play; an unchanged target
	// would only ever be skipped, never refused.
	cleared := make([]workbook.CellInput, 0, 5)
	for row := 1; row <= 5; row++ {
		cleared = append(cleared, workbook.CellInput{Row: row, Column: 3})
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, BaseVersion: current.Version, IdempotencyKey: "visibility-clear", Cells: cleared}); err != nil {
		t.Fatal(err)
	}
	if current, err = repository.GetWorkbook(ctx, book.ID); err != nil {
		t.Fatal(err)
	}
	second := change(2, `20`, current.Version, "visibility-second")
	results, triggerErr := service.TriggerCellChange(ctx, second, []workbook.CellInput{{Row: 2, Column: 1}}, actor)
	if triggerErr == nil {
		t.Fatal("a run over the cell limit should report an error")
	}
	if len(results) != 1 || results[0].Run.Status != automation.StatusFailed || !strings.Contains(results[0].Run.ErrorMessage, "1 cell limit") {
		t.Fatalf("failed run handed back to the caller: %#v", results)
	}
	runs, err := service.ListRuns(ctx, item.ID, 10)
	if err != nil || len(runs) != 2 || runs[0].Status != automation.StatusFailed {
		t.Fatalf("run history: %#v, %v", runs, err)
	}
	// The insert used to drop error_message, so history showed "실패" with no reason.
	if !strings.Contains(runs[0].ErrorMessage, "1 cell limit") || runs[0].TriggerOperationID != second.OperationID {
		t.Fatalf("recorded failure: %#v", runs[0])
	}
	broken, err := service.Overview(ctx, book.ID)
	if err != nil || broken.Items[0].LastFailure == nil || !strings.Contains(broken.Items[0].LastFailure.Message, "1 cell limit") || broken.Items[0].LastFailure.TriggerType != automation.TriggerCellChange {
		t.Fatalf("overview after a failed run: %#v, %v", broken, err)
	}

	// A later success means the automation recovered; the panel must stop warning.
	config["automation.max_cells_per_run"] = float64(1000)
	if current, err = repository.GetWorkbook(ctx, book.ID); err != nil {
		t.Fatal(err)
	}
	third := change(3, `30`, current.Version, "visibility-third")
	if _, err := service.TriggerCellChange(ctx, third, []workbook.CellInput{{Row: 3, Column: 1}}, actor); err != nil {
		t.Fatalf("recovered trigger: %v", err)
	}
	recovered, err := service.Overview(ctx, book.ID)
	if err != nil || recovered.Items[0].LastFailure != nil {
		t.Fatalf("overview after recovery: %#v, %v", recovered, err)
	}

	config["automation.enabled"] = false
	off, err := service.Overview(ctx, book.ID)
	if err != nil || off.ExecutionEnabled {
		t.Fatalf("overview with execution switched off: %#v, %v", off, err)
	}
}

// The workbook list asked the database for one workbook's sheets at a time, so
// a home page with five thousand workbooks made five thousand round trips and
// took 1.8 seconds. The sheets are now read in one query.
//
// The guard is the number of connection acquisitions rather than elapsed time:
// a clock makes a flaky test, while "the query count does not grow with the
// number of workbooks" is the property that actually matters.
func TestPostgresWorkbookListReadsEverySheetInOneQuery(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	owner := fmt.Sprintf("list-owner-%d", time.Now().UnixNano())
	workspace := fmt.Sprintf("list-%d", time.Now().UnixNano())
	created := make([]workbook.Workbook, 0, 24)
	for index := 0; index < 24; index++ {
		book, createErr := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: fmt.Sprintf("목록 %d", index), WorkspaceID: workspace, OwnerID: owner})
		if createErr != nil {
			t.Fatal(createErr)
		}
		defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
		created = append(created, book)
	}
	// One workbook gets extra sheets, so the batched read has to keep each
	// workbook's own sheets together and in position order.
	for _, name := range []string{"둘째", "셋째"} {
		if _, err := repository.CreateSheet(ctx, created[0].ID, workbook.CreateSheetInput{Name: name}); err != nil {
			t.Fatal(err)
		}
	}

	principal := workbook.AccessPrincipal{UserID: owner, Authenticated: true}
	measure := func() (int64, []workbook.Workbook) {
		t.Helper()
		before := pool.Stat().AcquireCount()
		items, listErr := repository.ListWorkbooks(ctx, workspace, principal)
		if listErr != nil {
			t.Fatal(listErr)
		}
		return pool.Stat().AcquireCount() - before, items
	}
	acquires, items := measure()
	if len(items) != 24 {
		t.Fatalf("listed %d workbooks", len(items))
	}
	// 워크북 스물넷을 한 줄씩 물으면 스물넷을 넘는다. 몇 번인지를 못박기보다
	// 워크북 수에 따라 늘지 않는다는 것을 본다.
	if acquires > 10 {
		t.Fatalf("listing 24 workbooks took %d connection acquisitions", acquires)
	}
	for _, item := range items {
		if len(item.Sheets) == 0 {
			t.Fatalf("%s came back without sheets", item.Title)
		}
		if item.ID == created[0].ID {
			names := make([]string, 0, len(item.Sheets))
			for _, sheet := range item.Sheets {
				names = append(names, sheet.Name)
			}
			if !reflect.DeepEqual(names, []string{"Sheet1", "둘째", "셋째"}) {
				t.Fatalf("sheets of the multi-sheet workbook = %v", names)
			}
		} else if len(item.Sheets) != 1 {
			t.Fatalf("%s has %d sheets", item.Title, len(item.Sheets))
		}
	}
}

// 목록 화면이 열 수 있는 워크북을 전부 받아 브라우저에서 걸러 내던 것을
// 서버로 옮겼다. 메모리 저장소와 같은 답을 내야 하므로 같은 경우들을 본다.
func TestPostgresBrowseWorkbooksPagesSearchesAndFilters(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	owner := fmt.Sprintf("browse-owner-%d", time.Now().UnixNano())
	workspace := fmt.Sprintf("browse-%d", time.Now().UnixNano())
	titles := []string{"분기 보고", "분기 예산", "월간 보고", "연간 계획", "50% 달성"}
	created := make([]workbook.Workbook, 0, len(titles))
	for _, title := range titles {
		book, createErr := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: title, WorkspaceID: workspace, OwnerID: owner})
		if createErr != nil {
			t.Fatal(createErr)
		}
		defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
		created = append(created, book)
	}
	if err := repository.SetWorkbookFavorite(ctx, created[0].ID, owner, true); err != nil {
		t.Fatal(err)
	}
	principal := workbook.AccessPrincipal{UserID: owner, Authenticated: true}
	browse := func(query workbook.WorkbookQuery) workbook.WorkbookPage {
		t.Helper()
		query.WorkspaceID = workspace
		page, browseErr := repository.BrowseWorkbooks(ctx, principal, query)
		if browseErr != nil {
			t.Fatal(browseErr)
		}
		return page
	}

	all := browse(workbook.WorkbookQuery{})
	if all.Total != 5 || len(all.Items) != 5 || all.HasMore {
		t.Fatalf("unlimited browse = %d of %d, more=%v", len(all.Items), all.Total, all.HasMore)
	}
	for _, item := range all.Items {
		if len(item.Sheets) != 1 {
			t.Fatalf("%s came back with %d sheets", item.Title, len(item.Sheets))
		}
	}
	// 페이지를 이어 붙이면 전체와 같은 순서여야 한다.
	joined := make([]string, 0, 5)
	for offset := 0; offset < 5; offset += 2 {
		page := browse(workbook.WorkbookQuery{Limit: 2, Offset: offset})
		if page.Total != 5 {
			t.Fatalf("page at %d reported %d in total", offset, page.Total)
		}
		if wantMore := offset+len(page.Items) < 5; page.HasMore != wantMore {
			t.Fatalf("page at %d said more=%v", offset, page.HasMore)
		}
		for _, item := range page.Items {
			joined = append(joined, item.ID)
		}
	}
	for index, id := range joined {
		if id != all.Items[index].ID {
			t.Fatalf("paged order differs from the whole list at %d", index)
		}
	}

	if search := browse(workbook.WorkbookQuery{Search: "분기"}); search.Total != 2 {
		t.Fatalf("search for 분기 found %d", search.Total)
	}
	if paged := browse(workbook.WorkbookQuery{Search: "보고", Limit: 1}); paged.Total != 2 || len(paged.Items) != 1 || !paged.HasMore {
		t.Fatalf("searched page = %d of %d, more=%v", len(paged.Items), paged.Total, paged.HasMore)
	}
	// % 는 제목의 글자일 뿐이며 아무것이나 맞는 표시가 아니다.
	if wildcard := browse(workbook.WorkbookQuery{Search: "%"}); wildcard.Total != 1 {
		t.Fatalf("searching for a percent sign matched %d workbooks", wildcard.Total)
	}
	if underscore := browse(workbook.WorkbookQuery{Search: "_"}); underscore.Total != 0 {
		t.Fatalf("searching for an underscore matched %d workbooks", underscore.Total)
	}
	if favorite := browse(workbook.WorkbookQuery{Filter: "favorite"}); favorite.Total != 1 || favorite.Items[0].ID != created[0].ID {
		t.Fatalf("favorite filter = %d", favorite.Total)
	}
	if owned := browse(workbook.WorkbookQuery{Filter: "owned"}); owned.Total != 5 {
		t.Fatalf("owned filter = %d", owned.Total)
	}
	if shared := browse(workbook.WorkbookQuery{Filter: "shared"}); shared.Total != 0 {
		t.Fatalf("shared filter = %d", shared.Total)
	}
}

// 요약은 사람이 손으로 하기에 가장 지루한 일이다. 에이전트가 할 수 있는
// 것에 피벗이 빠져 있어 "부서별 매출 합계" 같은 요청은 셀마다 SUMIF 를 박아
// 넣는 수밖에 없었다. 그러면 자료가 늘 때마다 수식을 다시 손봐야 한다.
//
// 여기서는 계획부터 되돌리기까지 실제로 돌려 본다. 만든 것을 되돌렸는데
// 피벗이 남으면, 되돌렸다는 말과 화면이 어긋난다.
func TestPostgresWorkbookAgentCreatesAndRollsBackPivot(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-pivot-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 피벗", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "pivot-agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"부서"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"영업"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`100`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"영업"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`50`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"개발"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`70`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-pivot-test","context_length":16384}]}`))
			return
		}
		plan := `{"summary":"부서별 매출 요약","explanation":"원본을 그대로 두고 피벗으로 부서별 합계를 냅니다.","findings":[],"changes":[],"tool_calls":[{"name":"create_pivot","arguments":{"source_range":"A1:B4","name":"부서별 매출","rows":[{"column":1,"name":"부서"}],"values":[{"column":2,"aggregation":"sum"}]}}]}`
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 300, "completion_tokens": 100}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-pivot-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	run, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B4", Message: "부서별 매출 합계를 요약해줘", Mode: kanpicai.ModeAgent, BaseVersion: seed.ServerVersion, IdempotencyKey: "pivot-agent-message", ClientID: "browser", ActorID: actor})
	if err != nil || run.State != kanpicai.AgentWaitingApproval || len(run.Action.ToolCalls) != 1 || run.Action.ToolCalls[0].Name != "create_pivot" {
		t.Fatalf("pivot agent plan=%#v, %v", run, err)
	}
	executed, err := service.ApproveRun(ctx, run.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "pivot-agent-approve", ExpectedRevision: run.Action.Revision})
	if err != nil || executed.Run.State != kanpicai.AgentCompleted || !executed.Run.Validation.Passed {
		t.Fatalf("pivot agent execution=%#v, %v", executed, err)
	}
	pivots, err := repository.ListPivots(ctx, book.ID, sheet.ID)
	if err != nil || len(pivots) != 1 || pivots[0].Name != "부서별 매출" || len(pivots[0].Values) != 1 {
		t.Fatalf("pivots=%#v, %v", pivots, err)
	}
	// 요약이 실제로 맞는지까지 본다. 정의만 저장되고 계산이 어긋나면 사람은
	// 승인 화면을 믿고 틀린 숫자를 받는다.
	data, err := repository.GetPivotData(ctx, pivots[0].ID)
	if err != nil || len(data.Rows) != 2 {
		t.Fatalf("pivot data=%#v, %v", data, err)
	}
	totals := map[string]any{}
	for _, row := range data.Rows {
		if len(row.Labels) != 1 || len(row.Values) != 1 {
			t.Fatalf("pivot row=%#v", row)
		}
		totals[row.Labels[0]] = row.Values[0]
	}
	if fmt.Sprint(totals["영업"]) != "150" || fmt.Sprint(totals["개발"]) != "70" {
		t.Fatalf("pivot totals=%#v", totals)
	}
	// 원본은 그대로여야 한다. 피벗은 읽기만 한다.
	cells, err := repository.ReadRange(ctx, sheet.ID, mustRange(t, "A1:B4"))
	if err != nil || len(cells) != 8 {
		t.Fatalf("source cells=%d, %v", len(cells), err)
	}
	rolledBack, err := service.RollbackChangeSet(ctx, run.ChangeSetID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "pivot-agent-rollback", ExpectedRevision: executed.Run.Action.Revision})
	if err != nil || rolledBack.Run.Action.Status != kanpicai.StatusUndone {
		t.Fatalf("pivot rollback=%#v, %v", rolledBack, err)
	}
	remaining, err := repository.ListPivots(ctx, book.ID, sheet.ID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("rollback left pivots=%#v, %v", remaining, err)
	}
}

// "상태 열은 진행/완료만 고르게 해줘" 는 서식으로는 못 한다. 색을 칠해 봐야
// 잘못된 값은 그대로 들어간다. 규칙이 실제로 잘못된 입력을 막는지, 되돌리면
// 규칙이 사라져 다시 넣을 수 있는지까지 본다.
func TestPostgresWorkbookAgentCreatesAndRollsBackDataValidation(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-validation-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 입력 규칙", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "validation-agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 2, Value: json.RawMessage(`"상태"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`"진행"`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-validation-test","context_length":16384}]}`))
			return
		}
		plan := `{"summary":"상태 열 입력 제한","explanation":"상태 열에 진행/완료만 넣을 수 있게 합니다.","findings":[],"changes":[],"tool_calls":[{"name":"create_data_validation","arguments":{"range":"B2:B5","rule_type":"list","options":[{"value":"진행"},{"value":"완료"}],"reject_input":true}}]}`
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 280, "completion_tokens": 90}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-validation-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	run, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "B1:B5", Message: "상태 열은 진행/완료만 고르게 해줘", Mode: kanpicai.ModeAgent, BaseVersion: seed.ServerVersion, IdempotencyKey: "validation-agent-message", ClientID: "browser", ActorID: actor})
	if err != nil || len(run.Action.ToolCalls) != 1 || run.Action.ToolCalls[0].Name != "create_data_validation" {
		t.Fatalf("validation agent plan=%#v, %v", run, err)
	}
	executed, err := service.ApproveRun(ctx, run.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "validation-agent-approve", ExpectedRevision: run.Action.Revision})
	if err != nil || executed.Run.State != kanpicai.AgentCompleted || !executed.Run.Validation.Passed {
		t.Fatalf("validation agent execution=%#v, %v", executed, err)
	}
	rules, err := repository.ListDataValidations(ctx, sheet.ID)
	if err != nil || len(rules) != 1 || rules[0].Range != "B2:B5" || len(rules[0].Options) != 2 {
		t.Fatalf("rules=%#v, %v", rules, err)
	}
	// 규칙이 저장만 되고 막지 못하면 아무 일도 하지 않은 것과 같다.
	current, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: current.Version, IdempotencyKey: "validation-agent-reject", Cells: []workbook.CellInput{
		{Row: 3, Column: 2, Value: json.RawMessage(`"보류"`)},
	}}); !errors.Is(err, workbook.ErrValidation) {
		t.Fatalf("규칙이 잘못된 값을 막지 못했다: %v", err)
	}
	rolledBack, err := service.RollbackChangeSet(ctx, run.ChangeSetID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "validation-agent-rollback", ExpectedRevision: executed.Run.Action.Revision})
	if err != nil || rolledBack.Run.Action.Status != kanpicai.StatusUndone {
		t.Fatalf("validation rollback=%#v, %v", rolledBack, err)
	}
	remaining, err := repository.ListDataValidations(ctx, sheet.ID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("rollback left rules=%#v, %v", remaining, err)
	}
	// 되돌렸으면 다시 넣을 수 있어야 한다. 규칙만 사라지고 막힘이 남으면
	// 되돌렸다는 말과 화면이 어긋난다.
	current, err = repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: current.Version, IdempotencyKey: "validation-agent-after-rollback", Cells: []workbook.CellInput{
		{Row: 3, Column: 2, Value: json.RawMessage(`"보류"`)},
	}}); err != nil {
		t.Fatalf("되돌린 뒤에도 입력이 막힌다: %v", err)
	}
}

// "매출 100 이상만 보이게 해줘" 는 줄을 지우는 것이 아니다. 걸러내기는
// 숨길 뿐이므로 자료가 그대로 남는다. 되돌리면 숨긴 것도 함께 풀려야 한다 —
// 걸러내기가 남으면 되돌린 줄이 계속 숨어 있다.
func TestPostgresWorkbookAgentCreatesAndRollsBackFilterView(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-filter-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 필터", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "filter-agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"부서"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"영업"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`150`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"개발"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`70`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-filter-test","context_length":16384}]}`))
			return
		}
		plan := `{"summary":"매출 100 이상만 보기","explanation":"줄을 지우지 않고 숨깁니다.","findings":[],"changes":[],"tool_calls":[{"name":"create_filter_view","arguments":{"name":"매출 100 이상","range":"A1:B3","criteria":[{"column":2,"operator":"greater_or_equal","value":100}]}}]}`
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 260, "completion_tokens": 80}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-filter-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	run, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B3", Message: "매출 100 이상만 보이게 해줘", Mode: kanpicai.ModeAgent, BaseVersion: seed.ServerVersion, IdempotencyKey: "filter-agent-message", ClientID: "browser", ActorID: actor})
	if err != nil || len(run.Action.ToolCalls) != 1 || run.Action.ToolCalls[0].Name != "create_filter_view" {
		t.Fatalf("filter agent plan=%#v, %v", run, err)
	}
	executed, err := service.ApproveRun(ctx, run.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "filter-agent-approve", ExpectedRevision: run.Action.Revision})
	if err != nil || executed.Run.State != kanpicai.AgentCompleted || !executed.Run.Validation.Passed {
		t.Fatalf("filter agent execution=%#v, %v", executed, err)
	}
	views, err := repository.ListFilterViews(ctx, sheet.ID, actor)
	if err != nil || len(views) != 1 || len(views[0].Criteria) != 1 {
		t.Fatalf("filter views=%#v, %v", views, err)
	}
	// 만들어 놓고 꺼져 있으면 사람은 아무 변화도 보지 못한다.
	if !views[0].Active {
		t.Error("필터가 꺼진 채로 만들어졌다")
	}
	// 숨기기만 한다. 줄은 그대로 있어야 한다.
	cells, err := repository.ReadRange(ctx, sheet.ID, mustRange(t, "A1:B3"))
	if err != nil || len(cells) != 6 {
		t.Fatalf("filter changed cells=%d, %v", len(cells), err)
	}
	rolledBack, err := service.RollbackChangeSet(ctx, run.ChangeSetID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "filter-agent-rollback", ExpectedRevision: executed.Run.Action.Revision})
	if err != nil || rolledBack.Run.Action.Status != kanpicai.StatusUndone {
		t.Fatalf("filter rollback=%#v, %v", rolledBack, err)
	}
	remaining, err := repository.ListFilterViews(ctx, sheet.ID, actor)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("rollback left filter views=%#v, %v", remaining, err)
	}
}

// 정렬은 앞의 도구들과 성격이 다르다. 피벗·필터·규칙은 물건을 하나 더
// 얹을 뿐이라 되돌리기가 그것을 지우는 것으로 끝났다. 정렬은 있던 자료를
// 다시 쓰므로, 되돌리려면 작업 자체를 되돌려야 한다. 순서가 원래대로
// 돌아오는지, 수식이 줄을 따라 옮겨졌다가 함께 돌아오는지까지 본다.
func TestPostgresWorkbookAgentSortsAndRestoresTheOriginalOrder(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-sort-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 정렬", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "sort-agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"부서"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"영업"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`70`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"개발"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`150`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"지원"`)}, {Row: 4, Column: 2, Formula: "=70+30"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-sort-test","context_length":16384}]}`))
			return
		}
		plan := `{"summary":"매출 많은 순 정렬","explanation":"머리글을 두고 매출 열 기준으로 줄을 다시 세웁니다.","findings":[],"changes":[],"tool_calls":[{"name":"sort_range","arguments":{"range":"A1:B4","header_rows":1,"keys":[{"column":2,"direction":"desc"}]}}]}`
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 260, "completion_tokens": 80}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-sort-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	run, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B4", Message: "매출 많은 순으로 정렬해줘", Mode: kanpicai.ModeAgent, BaseVersion: seed.ServerVersion, IdempotencyKey: "sort-agent-message", ClientID: "browser", ActorID: actor})
	if err != nil || len(run.Action.ToolCalls) != 1 || run.Action.ToolCalls[0].Name != "sort_range" {
		t.Fatalf("sort agent plan=%#v, %v", run, err)
	}
	// 있는 자료를 다시 쓰는 계획이므로 위험도가 높아야 한다.
	if run.Action.ToolCalls[0].Risk != kanpicai.RiskHigh {
		t.Errorf("risk=%q", run.Action.ToolCalls[0].Risk)
	}
	executed, err := service.ApproveRun(ctx, run.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "sort-agent-approve", ExpectedRevision: run.Action.Revision})
	if err != nil || executed.Run.State != kanpicai.AgentCompleted || !executed.Run.Validation.Passed {
		t.Fatalf("sort agent execution=%#v, %v", executed, err)
	}
	order := func(t *testing.T) []string {
		t.Helper()
		cells, err := repository.ReadRange(ctx, sheet.ID, mustRange(t, "A2:A4"))
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(cells))
		for _, cell := range cells {
			names = append(names, string(cell.Value))
		}
		return names
	}
	// 150, 100(=70+30), 70 순서. 수식 셀도 계산된 값으로 줄을 세운다.
	if sorted := order(t); len(sorted) != 3 || sorted[0] != `"개발"` || sorted[1] != `"지원"` || sorted[2] != `"영업"` {
		t.Fatalf("sorted order=%#v", sorted)
	}
	// 수식은 줄을 따라 옮겨져야 한다. 값만 옮기면 다시 계산할 때 어긋난다.
	moved, err := repository.ReadRange(ctx, sheet.ID, mustRange(t, "B3"))
	if err != nil || len(moved) != 1 || moved[0].Formula != "=70+30" {
		t.Fatalf("moved formula=%#v, %v", moved, err)
	}
	rolledBack, err := service.RollbackChangeSet(ctx, run.ChangeSetID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "sort-agent-rollback", ExpectedRevision: executed.Run.Action.Revision})
	if err != nil || rolledBack.Run.Action.Status != kanpicai.StatusUndone {
		t.Fatalf("sort rollback=%#v, %v", rolledBack, err)
	}
	if restored := order(t); len(restored) != 3 || restored[0] != `"영업"` || restored[1] != `"개발"` || restored[2] != `"지원"` {
		t.Fatalf("restored order=%#v", restored)
	}
	back, err := repository.ReadRange(ctx, sheet.ID, mustRange(t, "B4"))
	if err != nil || len(back) != 1 || back[0].Formula != "=70+30" {
		t.Fatalf("restored formula=%#v, %v", back, err)
	}
}

// 도구 하나가 실패하면 앞서 성공한 도구는 어떻게 되는가. 차트가 남는 것도
// 곤란하지만 정렬은 훨씬 나쁘다 — 있던 자료가 다시 쓰인 채로 남고, 실행이
// 실패로 끝나면 되돌리기 경로도 없다.
func TestPostgresWorkbookAgentUndoesEarlierToolsWhenALaterOneFails(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("workbook-partial-agent-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "Workbook Agent 부분 실패", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "partial-agent-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"부서"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"영업"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`70`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"개발"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`150`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"agent-partial-test","context_length":16384}]}`))
			return
		}
		// 두 번째 도구는 계획은 통과하지만 저장소가 거부한다 — 체크박스는
		// 켠 값과 끈 값 딱 둘이어야 한다.
		plan := `{"summary":"정렬 뒤 체크박스","explanation":"매출 순으로 세우고 상태 열에 체크박스를 넣습니다.","findings":[],"changes":[],"tool_calls":[` +
			`{"name":"sort_range","arguments":{"range":"A1:B3","header_rows":1,"keys":[{"column":2,"direction":"desc"}]}},` +
			`{"name":"create_pivot","arguments":{"source_range":"A1:B3","name":"부서별","rows":[{"column":1}],"values":[{"column":2,"aggregation":"sum"}]}},` +
			`{"name":"create_data_validation","arguments":{"range":"A2:A3","rule_type":"checkbox","options":[{"value":"예"},{"value":"아니오"},{"value":"보류"}]}}]}`
		encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 260, "completion_tokens": 90}})
		_, _ = w.Write(encoded)
	}))
	defer gateway.Close()
	service := kanpicai.NewService(pool, staticAISettings{
		"ai.enabled": true, "ai.gateway_url": gateway.URL + "/v1", "ai.model": "agent-partial-test", "ai.api_key": "",
		"ai.timeout_seconds": float64(5), "ai.max_input_cells": float64(20), "ai.max_changes": float64(10),
	}, repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.SetHTTPClient(gateway.Client())
	run, err := service.SendMessage(ctx, kanpicai.AgentMessageInput{WorkbookID: book.ID, SheetID: sheet.ID, Selection: "A1:B3", Message: "매출 순으로 세우고 체크박스를 넣어줘", Mode: kanpicai.ModeAgent, BaseVersion: seed.ServerVersion, IdempotencyKey: "partial-agent-message", ClientID: "browser", ActorID: actor})
	if err != nil || len(run.Action.ToolCalls) != 3 {
		t.Fatalf("partial agent plan=%#v, %v", run, err)
	}
	if _, err := service.ApproveRun(ctx, run.ID, kanpicai.ApprovalInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "partial-agent-approve", ExpectedRevision: run.Action.Revision}); err == nil {
		t.Fatal("두 번째 도구가 실패했는데 승인이 성공했다")
	}
	// 실패로 끝났으면 첫 도구가 바꾼 것도 남아 있으면 안 된다. 실패한 실행은
	// 되돌리기 경로가 없으므로, 남으면 사람이 손으로 되돌려야 한다.
	cells, err := repository.ReadRange(ctx, sheet.ID, mustRange(t, "A2:A3"))
	if err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(cells))
	for _, cell := range cells {
		order = append(order, string(cell.Value))
	}
	if len(order) != 2 || order[0] != `"영업"` || order[1] != `"개발"` {
		t.Fatalf("실패한 실행이 정렬을 남겼다: %#v", order)
	}
	// 만든 물건도 마찬가지다. 남으면 아무도 만들라고 하지 않은 것이 남는다.
	pivots, err := repository.ListPivots(ctx, book.ID, sheet.ID)
	if err != nil || len(pivots) != 0 {
		t.Fatalf("실패한 실행이 피벗을 남겼다: %#v, %v", pivots, err)
	}
	// 되돌린 도구는 완료로 남아 있으면 안 된다 — 화면이 한 일과 어긋난다.
	failed, err := service.GetRun(ctx, run.ID, actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range failed.Action.ToolCalls {
		if tool.Status == "completed" {
			t.Errorf("되돌린 도구가 완료로 남았다: %s", tool.Name)
		}
	}
}

// 이름 있는 수식은 워크북에 저장해 두고 부르는 것이다. 저장·수정·삭제가
// 그것을 쓰는 칸까지 닿는지, 그리고 그 셈이 PostgreSQL 경로에서도 같은지
// 본다 — 메모리에서만 맞으면 실제로 쓰는 곳에서 깨진다.
func TestPostgresNamedFunctionsFlowThroughToCells(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	actor := fmt.Sprintf("named-function-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "이름 있는 수식", WorkspaceID: "integration", OwnerID: actor})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: book.Version, IdempotencyKey: "fn-seed", Cells: []workbook.CellInput{
		{Row: 1, Column: 1, Value: value(100)}, {Row: 1, Column: 2, Value: value(60)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateNamedFunction(ctx, book.ID, actor, workbook.CreateNamedFunctionInput{
		IdempotencyKey: "fn-1", Name: "마진율", Parameters: []string{"매출", "원가"}, Body: "=(매출-원가)/매출",
		Description: "매출에서 원가를 뺀 몫",
	})
	if err != nil {
		t.Fatalf("만들기: %v", err)
	}
	if created.Body != "(매출-원가)/매출" || len(created.Parameters) != 2 || created.Revision != 1 {
		t.Fatalf("저장된 정의=%#v", created)
	}
	// 같은 열쇠로 다시 부르면 같은 것이 돌아온다.
	again, err := repository.CreateNamedFunction(ctx, book.ID, actor, workbook.CreateNamedFunctionInput{
		IdempotencyKey: "fn-1", Name: "딴이름", Parameters: []string{"a"}, Body: "a",
	})
	if err != nil || again.ID != created.ID {
		t.Fatalf("멱등성=%#v, %v", again, err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet.ID, ActorID: actor, BaseVersion: created.WorkbookVersion, IdempotencyKey: "fn-use", Cells: []workbook.CellInput{
		{Row: 1, Column: 3, Formula: "=마진율(A1,B1)"},
	}}); err != nil {
		t.Fatalf("쓰기: %v (base=%d, seed=%d)", err, created.WorkbookVersion, seed.ServerVersion)
	}
	read := func() string {
		cells, readErr := repository.ReadRange(ctx, sheet.ID, mustRange(t, "C1"))
		if readErr != nil || len(cells) != 1 {
			t.Fatalf("C1 읽기=%v %v", cells, readErr)
		}
		return string(cells[0].Value)
	}
	if actual := read(); actual != "0.4" {
		t.Fatalf("C1=%s, 기대=0.4", actual)
	}
	// 목록으로도 보인다.
	listed, err := repository.ListNamedFunctions(ctx, book.ID)
	if err != nil || len(listed) != 1 || listed[0].Description != "매출에서 원가를 뺀 몫" {
		t.Fatalf("목록=%#v, %v", listed, err)
	}
	// 정의를 바꾸면 쓰던 칸이 따라 바뀐다.
	body := "=(매출-원가)/원가"
	updated, err := repository.UpdateNamedFunction(ctx, created.ID, actor, workbook.UpdateNamedFunctionInput{Body: &body, ExpectedRevision: &created.Revision})
	if err != nil {
		t.Fatalf("고치기: %v", err)
	}
	if updated.Revision != 2 {
		t.Errorf("리비전=%d", updated.Revision)
	}
	if actual := read(); actual != "0.666666666666667" {
		t.Fatalf("고친 뒤 C1=%s", actual)
	}
	// 지난 리비전으로 고치려 하면 막는다.
	if _, err := repository.UpdateNamedFunction(ctx, created.ID, actor, workbook.UpdateNamedFunctionInput{Body: &body, ExpectedRevision: &created.Revision}); !errors.Is(err, workbook.ErrVersionConflict) {
		t.Errorf("지난 리비전=%v", err)
	}
	// 지우면 쓰던 칸이 #NAME? 이 된다. 조용히 예전 값을 남기면 사람은
	// 아직 살아 있는 줄 안다.
	if err := repository.DeleteNamedFunction(ctx, created.ID, actor, nil); err != nil {
		t.Fatalf("지우기: %v", err)
	}
	if actual := read(); actual != `"#NAME?"` {
		t.Fatalf("지운 뒤 C1=%s", actual)
	}
	if remaining, err := repository.ListNamedFunctions(ctx, book.ID); err != nil || len(remaining) != 0 {
		t.Fatalf("지운 뒤 목록=%#v, %v", remaining, err)
	}
}

// "이 범위가 바뀌면 알려줘" 는 저장 경로 스물한 곳 어디로 들어와도 같아야
// 한다. 여기서는 저장소 계약이 지켜지는지 본다 — 누가 무엇을 지켜보는지,
// 남의 것은 보이지 않는지.
func TestPostgresWatchRulesAreOwnedByTheWatcher(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	owner := fmt.Sprintf("watch-owner-%d", time.Now().UnixNano())
	other := owner + "-other"
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "변경 알림", WorkspaceID: "integration", OwnerID: owner})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	created, err := repository.CreateWatchRule(ctx, book.ID, owner, workbook.CreateWatchRuleInput{
		IdempotencyKey: "watch-1", SheetID: sheet.ID, Range: "a1:b10", Label: "매출표",
	})
	if err != nil {
		t.Fatalf("만들기: %v", err)
	}
	// 범위는 한 가지 모양으로 다듬어 저장한다.
	if created.Range != "A1:B10" || created.Watcher != owner || !created.Enabled {
		t.Fatalf("저장된 규칙=%#v", created)
	}
	// 같은 열쇠로 다시 부르면 같은 것이 돌아온다.
	again, err := repository.CreateWatchRule(ctx, book.ID, owner, workbook.CreateWatchRuleInput{
		IdempotencyKey: "watch-1", SheetID: sheet.ID, Range: "Z1:Z9",
	})
	if err != nil || again.ID != created.ID {
		t.Fatalf("멱등성=%#v, %v", again, err)
	}
	// 남이 무엇을 지켜보는지는 보이지 않는다. 누가 무엇을 보고 있는지는
	// 그 사람의 일이다.
	mine, err := repository.ListWatchRules(ctx, book.ID, owner)
	if err != nil || len(mine) != 1 {
		t.Fatalf("내 목록=%#v, %v", mine, err)
	}
	theirs, err := repository.ListWatchRules(ctx, book.ID, other)
	if err != nil || len(theirs) != 0 {
		t.Fatalf("남의 목록=%#v, %v", theirs, err)
	}
	// 셀이 바뀔 때 찾는 길에서는 시트의 모든 규칙이 보인다.
	sheetRules, err := repository.SheetWatchRules(ctx, sheet.ID)
	if err != nil || len(sheetRules) != 1 {
		t.Fatalf("시트 규칙=%#v, %v", sheetRules, err)
	}
	// 꺼 두면 셀이 바뀌어도 찾지 않는다.
	disabled := false
	if _, err := repository.UpdateWatchRule(ctx, created.ID, owner, workbook.UpdateWatchRuleInput{Enabled: &disabled, ExpectedRevision: &created.Revision}); err != nil {
		t.Fatalf("끄기: %v", err)
	}
	if off, err := repository.SheetWatchRules(ctx, sheet.ID); err != nil || len(off) != 0 {
		t.Fatalf("꺼 둔 규칙=%#v, %v", off, err)
	}
	// 지난 리비전으로 고치려 하면 막는다.
	if _, err := repository.UpdateWatchRule(ctx, created.ID, owner, workbook.UpdateWatchRuleInput{Enabled: &disabled, ExpectedRevision: &created.Revision}); !errors.Is(err, workbook.ErrVersionConflict) {
		t.Errorf("지난 리비전=%v", err)
	}
	if err := repository.DeleteWatchRule(ctx, created.ID, owner, nil); err != nil {
		t.Fatalf("지우기: %v", err)
	}
	if remaining, err := repository.ListWatchRules(ctx, book.ID, owner); err != nil || len(remaining) != 0 {
		t.Fatalf("지운 뒤=%#v, %v", remaining, err)
	}
	// 다른 워크북의 시트를 지켜볼 수는 없다.
	stranger, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "남의 워크북", WorkspaceID: "integration", OwnerID: other})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), stranger.ID, "integration-cleanup")
	if _, err := repository.CreateWatchRule(ctx, book.ID, owner, workbook.CreateWatchRuleInput{
		IdempotencyKey: "watch-cross", SheetID: stranger.Sheets[0].ID,
	}); !errors.Is(err, workbook.ErrInvalid) {
		t.Errorf("남의 시트=%v", err)
	}
	// 지켜보는 범위도 행과 열을 따라 움직인다. 규칙이 제자리에 남으면 사람은
	// 자기가 정한 칸을 본다고 믿는 채로 엉뚱한 칸의 알림을 받는다.
	moving, err := repository.CreateWatchRule(ctx, book.ID, owner, workbook.CreateWatchRuleInput{
		IdempotencyKey: "watch-structure", SheetID: sheet.ID, Range: "B5:B9",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 꺼 둔 규칙도 옮겨야 다시 켰을 때 제 칸을 본다.
	off, err := repository.CreateWatchRule(ctx, book.ID, owner, workbook.CreateWatchRuleInput{
		IdempotencyKey: "watch-structure-off", SheetID: sheet.ID, Range: "D5:D6",
	})
	if err != nil {
		t.Fatal(err)
	}
	turnedOff := false
	if off, err = repository.UpdateWatchRule(ctx, off.ID, owner, workbook.UpdateWatchRuleInput{Enabled: &turnedOff, ExpectedRevision: &off.Revision}); err != nil {
		t.Fatal(err)
	}
	seeded, seededErr := repository.GetWorkbook(ctx, book.ID)
	if seededErr != nil {
		t.Fatal(seededErr)
	}
	if _, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: seeded.Version,
		IdempotencyKey: "watch-row-insert", Axis: "row", Action: "insert", Index: 2, Count: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if movedRule, err := repository.GetWatchRule(ctx, moving.ID); err != nil || movedRule.Range != "B6:B10" {
		t.Fatalf("옮긴 범위=%#v, %v", movedRule, err)
	}
	if movedOff, err := repository.GetWatchRule(ctx, off.ID); err != nil || movedOff.Range != "D6:D7" || movedOff.Enabled {
		t.Fatalf("꺼 둔 규칙=%#v, %v", movedOff, err)
	}
	// 지켜보던 칸이 통째로 지워지면 볼 것이 없으므로 규칙도 사라진다.
	shifted, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: shifted.Version,
		IdempotencyKey: "watch-row-delete", Axis: "row", Action: "delete", Index: 6, Count: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if gone, err := repository.GetWatchRule(ctx, moving.ID); err == nil {
		t.Fatalf("지워진 칸을 보는 규칙이 남았다: %#v", gone)
	}
}

// runDueForWorkbook 은 예정된 자동화를 돌리고 그 워크북의 결과만 골라 낸다.
//
// 예정 실행을 쓸어 담는 일은 데이터베이스 전체를 훑는 것이 맞다 — 스케줄러는
// 워크북 하나를 위해 도는 것이 아니다. 그래서 같은 데이터베이스를 나눠 쓰는
// 시험이 나란히 돌면 남의 워크북 자동화가 같은 순간에 걸려 섞여 들어온다.
// 시험이 결과의 수를 세면 그때만 빨갛게 되는데, 원인은 만든 코드에 없다.
func runDueForWorkbook(ctx context.Context, service *automation.Service, now time.Time, workbookID string) ([]automation.ExecutionResult, error) {
	results, err := service.RunDueSchedules(ctx, now, 10)
	mine := make([]automation.ExecutionResult, 0, len(results))
	for _, result := range results {
		if result.Run.WorkbookID == workbookID {
			mine = append(mine, result)
		}
	}
	return mine, err
}

// 표는 이름으로 범위를 가리키는 것이다. =SUM(매출표[금액]) 은 열이 끼워지고
// 지워져도 그대로 맞아야 하고, 파일로 나갔다 들어와도 살아 있어야 한다.
func TestPostgresSheetTablesAnswerFormulasAndFollowStructure(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	owner := fmt.Sprintf("table-owner-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "표", WorkspaceID: "integration", OwnerID: owner})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: book.Version, IdempotencyKey: "table-seed",
		Cells: []workbook.CellInput{
			{Row: 1, Column: 1, Value: value("지역")}, {Row: 1, Column: 2, Value: value("금액")},
			{Row: 2, Column: 1, Value: value("서울")}, {Row: 2, Column: 2, Value: value(100)},
			{Row: 3, Column: 1, Value: value("부산")}, {Row: 3, Column: 2, Value: value(200)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	table, err := repository.CreateSheetTable(ctx, book.ID, owner, workbook.CreateSheetTableInput{
		IdempotencyKey: "table-1", SheetID: sheet.ID, Name: "매출표", Range: "a1:b3",
	})
	if err != nil || table.Range != "A1:B3" {
		t.Fatalf("만든 표=%#v, %v", table, err)
	}
	// 같은 열쇠로 다시 부르면 같은 것이 돌아온다.
	again, err := repository.CreateSheetTable(ctx, book.ID, owner, workbook.CreateSheetTableInput{
		IdempotencyKey: "table-1", SheetID: sheet.ID, Name: "다른이름", Range: "Z1:Z9",
	})
	if err != nil || again.ID != table.ID {
		t.Fatalf("멱등성=%#v, %v", again, err)
	}
	// 이름이 겹치면 어느 것을 가리키는지 알 수 없다.
	if _, err := repository.CreateSheetTable(ctx, book.ID, owner, workbook.CreateSheetTableInput{
		IdempotencyKey: "table-dup", SheetID: sheet.ID, Name: "매출표", Range: "D1:E3",
	}); !errors.Is(err, workbook.ErrDuplicateName) {
		t.Errorf("겹친 이름=%v", err)
	}
	current, err := repository.GetWorkbook(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: current.Version, IdempotencyKey: "table-formula",
		Cells: []workbook.CellInput{{Row: 5, Column: 1, Formula: "=SUM(매출표[금액])"}},
	}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A5")
	cells, err := repository.ReadRange(ctx, sheet.ID, selected)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "300" {
		t.Fatalf("합계 칸=%#v, %v", cells, err)
	}
	// 위에 행을 끼우면 표가 따라 내려가고 수식은 그대로 맞다.
	current, _ = repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyStructure(ctx, workbook.StructuralMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: current.Version,
		IdempotencyKey: "table-insert", Axis: "row", Action: "insert", Index: 1, Count: 1,
	}); err != nil {
		t.Fatal(err)
	}
	moved, err := repository.GetSheetTable(ctx, table.ID)
	if err != nil || moved.Range != "A2:B4" {
		t.Fatalf("옮긴 표=%#v, %v", moved, err)
	}
	movedCell, _ := cellrange.Parse("A6")
	cells, err = repository.ReadRange(ctx, sheet.ID, movedCell)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "300" {
		t.Fatalf("행을 끼운 뒤 합계=%#v, %v", cells, err)
	}
	// 파일로 나갔다 들어와도 표와 그 이름을 쓰는 수식이 살아 있어야 한다.
	service := importexport.New(repository)
	exported, err := service.Export(ctx, importexport.ExportRequest{WorkbookID: book.ID, Format: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := importexport.Parse(exported.Name, exported.Data, importexport.DefaultMaxExpandedBytes)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := repository.ImportWorkbook(ctx, workbook.ImportWorkbookInput{
		WorkspaceID: "integration", Title: "표 복원", OwnerID: owner, ActorID: owner,
		IdempotencyKey: "table-import", FileName: exported.Name, Format: "xlsx",
		Sheets: parsed.Sheets, NamedRanges: parsed.NamedRanges, NamedFunctions: parsed.NamedFunctions,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), restored.ID, "integration-cleanup")
	restoredTables, err := repository.ListSheetTables(ctx, restored.ID)
	if err != nil || len(restoredTables) != 1 || restoredTables[0].Name != "매출표" {
		t.Fatalf("돌아온 표=%#v, %v", restoredTables, err)
	}
	restoredCells, err := repository.ReadRange(ctx, restored.Sheets[0].ID, movedCell)
	if err != nil || len(restoredCells) != 1 || string(restoredCells[0].Value) != "300" {
		t.Fatalf("돌아온 합계=%#v, %v", restoredCells, err)
	}
	// 표 바로 아래 줄에 값을 넣으면 표가 그 줄을 삼킨다. 그때마다 사람이
	// 범위를 손으로 늘려야 한다면 합계는 새 줄을 빼고 셈한다 — 답이 나오기는
	// 하는데 틀린 답이다.
	current, _ = repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: current.Version, IdempotencyKey: "table-grow",
		Cells: []workbook.CellInput{{Row: 5, Column: 1, Value: value("대구")}, {Row: 5, Column: 2, Value: value(300)}},
	}); err != nil {
		t.Fatal(err)
	}
	grown, err := repository.GetSheetTable(ctx, table.ID)
	if err != nil || grown.Range != "A2:B5" {
		t.Fatalf("늘어난 표=%#v, %v", grown, err)
	}
	cells, err = repository.ReadRange(ctx, sheet.ID, movedCell)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != "600" {
		t.Fatalf("늘어난 뒤 합계=%#v, %v", cells, err)
	}
	// 한 줄 건너뛴 자리는 삼키지 않는다.
	current, _ = repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: current.Version, IdempotencyKey: "table-far",
		Cells: []workbook.CellInput{{Row: 8, Column: 2, Value: value(999)}},
	}); err != nil {
		t.Fatal(err)
	}
	if far, err := repository.GetSheetTable(ctx, table.ID); err != nil || far.Range != "A2:B5" {
		t.Fatalf("건너뛴 자리를 삼켰다: %#v, %v", far, err)
	}
	// 표를 지우면 그 이름을 쓰던 칸이 #NAME? 이 된다. 조용히 옛 값이 남으면
	// 사람은 없어진 표를 아직 있는 줄 안다.
	if err := repository.DeleteSheetTable(ctx, table.ID, owner, nil); err != nil {
		t.Fatal(err)
	}
	cells, err = repository.ReadRange(ctx, sheet.ID, movedCell)
	if err != nil || len(cells) != 1 || string(cells[0].Value) != `"#NAME?"` {
		t.Fatalf("표를 지운 뒤=%#v, %v", cells, err)
	}
}

// 워크북을 복제하거나 버전을 되돌릴 때 이름 있는 수식과 표도 함께 가야 한다.
//
// 두고 가면 칸에는 옛 값이 그대로 남아 있는데 그 값을 만든 정의가 없다.
// 사람은 멀쩡한 줄 알다가 무언가 고치는 순간 #NAME? 을 만난다.
func TestPostgresDuplicateAndRestoreCarryNamedFunctionsAndTables(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	owner := fmt.Sprintf("carry-owner-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "옮김", WorkspaceID: "integration", OwnerID: owner})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{
		SheetID: sheet.ID, ActorID: owner, BaseVersion: book.Version, IdempotencyKey: "carry-seed",
		Cells: []workbook.CellInput{
			{Row: 1, Column: 1, Value: value("지역")}, {Row: 1, Column: 2, Value: value("금액")},
			{Row: 2, Column: 1, Value: value("서울")}, {Row: 2, Column: 2, Value: value(100)},
			{Row: 3, Column: 1, Value: value("부산")}, {Row: 3, Column: 2, Value: value(200)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	table, err := repository.CreateSheetTable(ctx, book.ID, owner, workbook.CreateSheetTableInput{
		IdempotencyKey: "carry-table", SheetID: sheet.ID, Name: "매출표", Range: "A1:B3", Theme: "green",
	})
	if err != nil {
		t.Fatal(err)
	}
	namedFunction, err := repository.CreateNamedFunction(ctx, book.ID, owner, workbook.CreateNamedFunctionInput{
		IdempotencyKey: "carry-fn", Name: "두배", Parameters: []string{"x"}, Body: "x*2",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 복제하면 둘 다 사본으로 간다.
	copied, err := repository.DuplicateWorkbook(ctx, book.ID, workbook.DuplicateWorkbookInput{Title: "사본", OwnerID: owner})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), copied.ID, "integration-cleanup")
	copiedTables, err := repository.ListSheetTables(ctx, copied.ID)
	if err != nil || len(copiedTables) != 1 || copiedTables[0].Name != "매출표" || copiedTables[0].Theme != "green" {
		t.Fatalf("사본의 표=%#v, %v", copiedTables, err)
	}
	if copiedTables[0].SheetID != copied.Sheets[0].ID {
		t.Errorf("사본의 표가 원본 시트를 가리킨다: %s", copiedTables[0].SheetID)
	}
	copiedFunctions, err := repository.ListNamedFunctions(ctx, copied.ID)
	if err != nil || len(copiedFunctions) != 1 || copiedFunctions[0].Name != "두배" {
		t.Fatalf("사본의 이름 있는 수식=%#v, %v", copiedFunctions, err)
	}
	// 버전을 찍고 둘을 지운 뒤 되돌리면 둘 다 살아나야 한다.
	version, err := repository.CreateVersion(ctx, book.ID, "둘 다 있는 상태", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteSheetTable(ctx, table.ID, owner, nil); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteNamedFunction(ctx, namedFunction.ID, owner, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RestoreVersion(ctx, version.ID, owner); err != nil {
		t.Fatal(err)
	}
	restoredTables, err := repository.ListSheetTables(ctx, book.ID)
	if err != nil || len(restoredTables) != 1 || restoredTables[0].Name != "매출표" {
		t.Fatalf("되돌린 표=%#v, %v", restoredTables, err)
	}
	restoredFunctions, err := repository.ListNamedFunctions(ctx, book.ID)
	if err != nil || len(restoredFunctions) != 1 || restoredFunctions[0].Name != "두배" {
		t.Fatalf("되돌린 이름 있는 수식=%#v, %v", restoredFunctions, err)
	}
}

// 보호 범위와 필터 보기도 복제와 되돌리기를 따라가야 한다.
//
// 되돌렸는데 보호가 사라지는 것이 가장 나쁘다. 막으라고 걸어 둔 규칙이 조용히
// 없어지므로, 사람은 지켜지고 있다고 믿는 칸을 아무나 고치게 된다.
func TestPostgresDuplicateAndRestoreCarryProtectionsAndFilterViews(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	owner := fmt.Sprintf("guard-owner-%d", time.Now().UnixNano())
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "지킴", WorkspaceID: "integration", OwnerID: owner})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), book.ID, "integration-cleanup")
	sheet := book.Sheets[0]
	protected, err := repository.CreateProtectedRange(ctx, sheet.ID, owner, workbook.CreateProtectedRangeInput{
		IdempotencyKey: "guard-protect", SheetID: sheet.ID, Range: "A1:B1", Description: "머리글 보호", Editors: []string{owner},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := repository.CreateFilterView(ctx, sheet.ID, owner, workbook.CreateFilterViewInput{
		IdempotencyKey: "guard-filter", Name: "보기", Range: "A1:B5",
	})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := repository.DuplicateWorkbook(ctx, book.ID, workbook.DuplicateWorkbookInput{Title: "사본", OwnerID: owner})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), copied.ID, "integration-cleanup")
	copiedProtections, err := repository.ListProtectedRanges(ctx, copied.Sheets[0].ID)
	if err != nil || len(copiedProtections) != 1 || copiedProtections[0].Description != "머리글 보호" {
		t.Fatalf("사본의 보호 범위=%#v, %v", copiedProtections, err)
	}
	copiedViews, err := repository.ListFilterViews(ctx, copied.Sheets[0].ID, owner)
	if err != nil || len(copiedViews) != 1 || copiedViews[0].Name != "보기" {
		t.Fatalf("사본의 필터 보기=%#v, %v", copiedViews, err)
	}
	version, err := repository.CreateVersion(ctx, book.ID, "지키던 상태", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteProtectedRange(ctx, protected.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteFilterView(ctx, view.ID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RestoreVersion(ctx, version.ID, owner); err != nil {
		t.Fatal(err)
	}
	restoredProtections, err := repository.ListProtectedRanges(ctx, sheet.ID)
	if err != nil || len(restoredProtections) != 1 || restoredProtections[0].Range != "A1:B1" {
		t.Fatalf("되돌린 보호 범위=%#v, %v", restoredProtections, err)
	}
	restoredViews, err := repository.ListFilterViews(ctx, sheet.ID, owner)
	if err != nil || len(restoredViews) != 1 {
		t.Fatalf("되돌린 필터 보기=%#v, %v", restoredViews, err)
	}
}
