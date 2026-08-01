//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"kanpic/internal/apikey"
	"kanpic/internal/auth"
	"kanpic/internal/database"
	"kanpic/internal/importexport"
	"kanpic/internal/settings"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

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
