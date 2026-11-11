package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kanpic/internal/workbook"
)

// The REST lookups behind Ctrl+arrow and select-all read every stored row.
func TestCellEdgeAndRegionEndpoints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := workbook.NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "끝", OwnerID: "local-user"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	inputs := make([]workbook.CellInput, 0, 2400)
	for row := 1; row <= 1200; row++ {
		inputs = append(inputs, workbook.CellInput{Row: row, Column: 1, Value: json.RawMessage(`1`)}, workbook.CellInput{Row: row, Column: 2, Value: json.RawMessage(`2`)})
	}
	if _, err := repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheet, ActorID: "local-user", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: inputs}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	get := func(path string, target any) int {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if target != nil {
			_ = json.NewDecoder(bytes.NewReader(raw)).Decode(target)
		}
		return response.StatusCode
	}
	var line workbook.DataLine
	if status := get("/api/v1/sheets/"+sheet+"/cells/A1/edge?direction=down", &line); status != http.StatusOK {
		t.Fatalf("edge status=%d", status)
	}
	if len(line.Runs) != 1 || line.Runs[0] != [2]int{1, 1200} || line.Through <= 1200 {
		t.Fatalf("edge=%+v", line)
	}
	var region workbook.DataRegion
	if status := get("/api/v1/sheets/"+sheet+"/cells/B600/region", &region); status != http.StatusOK {
		t.Fatalf("region status=%d", status)
	}
	if region != (workbook.DataRegion{StartRow: 1, StartColumn: 1, EndRow: 1200, EndColumn: 2}) {
		t.Fatalf("region=%+v", region)
	}
	if status := get("/api/v1/sheets/"+sheet+"/cells/A1/edge?direction=sideways", nil); status != http.StatusBadRequest {
		t.Fatalf("bad direction status=%d", status)
	}
}
