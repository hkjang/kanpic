//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"kanpic/internal/apikey"
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
