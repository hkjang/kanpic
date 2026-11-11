//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"kanpic/internal/database"
	"kanpic/internal/workbook"
)

// Ctrl+arrow and select-all read the sheet through ReadRange, which Postgres
// answers from 64×64 blocks. A column of 9,000 rows crosses many blocks and
// two scan chunks, so an off-by-one at either boundary shows here as a run that
// stops short or a row counted twice.
func TestPostgresDataEdgeAndRegionAcrossBlocks(t *testing.T) {
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
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "integration data edge", WorkspaceID: "integration", OwnerID: "test-user"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), wb.ID, "integration-cleanup")
	sheetID := wb.Sheets[0].ID
	const rows = 9_000
	version := wb.Version
	for first := 1; first <= rows; first += workbook.MaxPasteCells / 2 {
		cells := make([]workbook.CellInput, 0, workbook.MaxPasteCells)
		for row := first; row < first+workbook.MaxPasteCells/2 && row <= rows; row++ {
			cells = append(cells, workbook.CellInput{Row: row, Column: 64, Value: json.RawMessage(`1`)}, workbook.CellInput{Row: row, Column: 65, Value: json.RawMessage(`2`)})
		}
		result, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: "test-user", BaseVersion: version, IdempotencyKey: "edge-seed-" + strconv.Itoa(first), Cells: cells, OperationType: "cells.paste"})
		if err != nil {
			t.Fatal(err)
		}
		version = result.ServerVersion
	}
	down, err := workbook.ScanDataLine(ctx, repository, sheetID, 1, 64, workbook.EdgeDown)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(down.Runs, [][2]int{{1, rows}}) || down.Through <= rows {
		t.Fatalf("down=%+v", down)
	}
	up, err := workbook.ScanDataLine(ctx, repository, sheetID, rows, 65, workbook.EdgeUp)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(up.Runs, [][2]int{{1, rows}}) || up.Through != 1 {
		t.Fatalf("up=%+v", up)
	}
	// Columns 64 and 65 sit in two different block columns.
	right, err := workbook.ScanDataLine(ctx, repository, sheetID, 4_500, 1, workbook.EdgeRight)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(right.Runs, [][2]int{{64, 65}}) {
		t.Fatalf("right=%+v", right)
	}
	region, err := workbook.CurrentRegion(ctx, repository, sheetID, 4_500, 65)
	if err != nil {
		t.Fatal(err)
	}
	if region != (workbook.DataRegion{StartRow: 1, StartColumn: 64, EndRow: rows, EndColumn: 65}) {
		t.Fatalf("region=%+v", region)
	}
}
