//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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
	defer repository.DeleteWorkbook(context.Background(), wb.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), copiedBook.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), copiedBook.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	if err := repository.DeleteWorkbook(ctx, book.ID); err != nil {
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), workbookCopy.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), wb.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), copy.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	defer repository.DeleteWorkbook(context.Background(), wb.ID)
	inputSheet := wb.Sheets[0]
	reportSheet, err := repository.CreateSheet(ctx, wb.ID, workbook.CreateSheetInput{Name: "Report"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: reportSheet.ID, ActorID: "delete-user", BaseVersion: 2, IdempotencyKey: "postgres-delete-reference", Cells: []workbook.CellInput{{Row: 1, Column: 1, Formula: `=Sheet1!A1`}, {Row: 1, Column: 2, Formula: `=A1+1`}}})
	if err != nil || len(created.FormulaErrors) != 0 {
		t.Fatalf("create deleted references=%#v error=%v", created, err)
	}
	if err := repository.DeleteSheet(ctx, inputSheet.ID); err != nil {
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
	defer repository.DeleteWorkbook(context.Background(), source.ID)
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
	defer repository.DeleteWorkbook(context.Background(), duplicated.ID)
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
	if err := repository.DeleteWorkbook(ctx, source.ID); err != nil {
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
	favorite := true
	if _, err := repository.UpdateWorkbook(ctx, book.ID, workbook.UpdateWorkbookInput{Favorite: &favorite}); err != nil {
		t.Fatal(err)
	}
	firstName, color, hidden := "summary", "#2563eb", true
	if _, err := repository.UpdateSheet(ctx, book.Sheets[0].ID, workbook.UpdateSheetInput{Name: &firstName, Color: &color, Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.CreateSheet(ctx, book.ID, workbook.CreateSheetInput{Name: "detail", Color: "#16a34a"})
	if err != nil {
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
	if err := repository.DeleteSheet(ctx, detail.ID); err != nil {
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
	versions, err := repository.ListVersions(ctx, book.ID)
	if err != nil || len(versions) != 2 || versions[0].Name != "복원 전 자동 백업" || versions[0].WorkbookVersion != 9 {
		t.Fatalf("automatic backup: %#v, %v", versions, err)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	if err := repository.DeleteSheet(ctx, book.Sheets[0].ID); err != nil {
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
	defer repository.DeleteWorkbook(context.Background(), created.ID)
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
	sheetID := book.Sheets[0].ID
	seed, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, ClientID: "browser", BaseVersion: 1, IdempotencyKey: "ai-seed", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`5`)}}})
	if err != nil {
		t.Fatal(err)
	}

	var gatewayCalls int
	gatewayMode := kanpicai.ModeFormula
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer repository.DeleteWorkbook(context.Background(), book.ID)
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
	runInput := automation.RunInput{ActorID: actor, ClientID: "browser", IdempotencyKey: "automation-run", ExpectedRevision: item.Revision}
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
	items, err := service.List(ctx, book.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("automations after delete=%#v, %v", items, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_type='automation' AND actor_id=$1`, actor).Scan(&auditCount); err != nil || auditCount != 8 {
		t.Fatalf("automation audit count=%d, %v", auditCount, err)
	}
}
