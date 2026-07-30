package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"kanpic/internal/workbook"
)

func TestWorkbookCellVersionFlow(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]any{"title": "분기 매출", "workspace_id": "default"}, http.StatusCreated)
	if created.Title != "분기 매출" || len(created.Sheets) != 1 || created.Version != 1 {
		t.Fatalf("unexpected workbook: %#v", created)
	}
	sheetID := created.Sheets[0].ID

	mutation := map[string]any{"base_version": 1, "idempotency_key": "edit-1", "cells": []map[string]any{{"row": 1, "column": 1, "value": "매출"}, {"row": 2, "column": 1, "value": 42}}}
	result := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", mutation, http.StatusOK)
	if result.ServerVersion != 2 || result.AppliedCells != 2 || result.Duplicate {
		t.Fatalf("unexpected mutation: %#v", result)
	}
	duplicate := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", mutation, http.StatusOK)
	if !duplicate.Duplicate || duplicate.ServerVersion != 2 {
		t.Fatalf("idempotency failed: %#v", duplicate)
	}

	var rangeResult struct {
		Items []workbook.Cell `json:"items"`
	}
	rangeResult = request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:B3", nil, http.StatusOK)
	if len(rangeResult.Items) != 2 {
		t.Fatalf("range contains %d cells", len(rangeResult.Items))
	}

	version := request[workbook.Version](t, server, http.MethodPost, "/api/v1/workbooks/"+created.ID+"/versions", map[string]string{"name": "초안"}, http.StatusCreated)
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", map[string]any{"base_version": 2, "idempotency_key": "edit-2", "cells": []map[string]any{{"row": 2, "column": 1, "value": 100}}}, http.StatusOK)
	restored := request[workbook.MutationResult](t, server, http.MethodPost, "/api/v1/versions/"+version.ID+":restore", map[string]any{}, http.StatusOK)
	if restored.ServerVersion != 4 {
		t.Fatalf("restore version = %d", restored.ServerVersion)
	}
	rangeResult = request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A2", nil, http.StatusOK)
	if string(rangeResult.Items[0].Value) != "42" {
		t.Fatalf("restore value = %s", rangeResult.Items[0].Value)
	}
}

func TestWorkbookDuplicateRESTAndMCPPreserveStructure(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	source := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]any{"title": "원본", "workspace_id": "finance"}, http.StatusCreated)
	detail := request[workbook.Sheet](t, server, http.MethodPost, "/api/v1/workbooks/"+source.ID+"/sheets", map[string]any{"name": "상세", "color": "#2563eb"}, http.StatusCreated)
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+detail.ID+"/cells:batch", map[string]any{"base_version": 2, "idempotency_key": "copy-http-seed", "cells": []map[string]any{{"row": 1, "column": 1, "value": 4, "style": map[string]any{"bold": true}}, {"row": 2, "column": 1, "formula": "=A1*2"}}}, http.StatusOK)

	copy := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks/"+source.ID+"/duplicate", map[string]any{}, http.StatusCreated)
	if copy.ID == source.ID || copy.Title != "원본 복사본" || copy.WorkspaceID != "finance" || copy.Version != 1 || len(copy.Sheets) != 2 || copy.Sheets[1].ID == detail.ID {
		t.Fatalf("REST workbook duplicate: %#v", copy)
	}
	copiedCells := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+copy.Sheets[1].ID+"/ranges/A1:A2", nil, http.StatusOK)
	if len(copiedCells.Items) != 2 || string(copiedCells.Items[0].Value) != "4" || copiedCells.Items[1].Formula != "=A1*2" || string(copiedCells.Items[1].Value) != "8" {
		t.Fatalf("REST copied cells: %#v", copiedCells.Items)
	}

	mcpCopy := request[struct {
		Result struct {
			Structured workbook.Workbook `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.workbook.duplicate", "arguments": map[string]any{"workbook_id": source.ID, "title": "에이전트 복사본"}}}, http.StatusOK)
	if mcpCopy.Result.Structured.Title != "에이전트 복사본" || mcpCopy.Result.Structured.Version != 1 || len(mcpCopy.Result.Structured.Sheets) != 2 {
		t.Fatalf("MCP workbook duplicate: %#v", mcpCopy.Result.Structured)
	}
}

func TestConcurrentSameCellReportsConflict(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "conflicts"}, http.StatusCreated)
	path := "/api/v1/sheets/" + created.Sheets[0].ID + "/cells:batch"
	request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 1, "idempotency_key": "a", "cells": []map[string]any{{"row": 1, "column": 1, "value": "first"}}}, http.StatusOK)
	result := request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 1, "idempotency_key": "b", "cells": []map[string]any{{"row": 1, "column": 1, "value": "second"}}}, http.StatusOK)
	if len(result.Conflicts) != 1 || result.Conflicts[0].ChangedAtVersion != 2 {
		t.Fatalf("missing conflict: %#v", result)
	}
}

func TestPasteEndpointAppliesMoreThanBatchLimitAtomically(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "large paste"}, http.StatusCreated)
	cells := make([]map[string]any, workbook.MaxBatchCells+1)
	for index := range cells {
		cells[index] = map[string]any{"row": index + 1, "column": 1, "value": index + 1}
	}
	body := map[string]any{"base_version": 1, "idempotency_key": "paste-1001", "client_id": "test", "cells": cells}
	request[map[string]any](t, server, http.MethodPatch, "/api/v1/sheets/"+created.Sheets[0].ID+"/cells:batch", body, http.StatusBadRequest)
	result := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+created.Sheets[0].ID+"/cells:paste", body, http.StatusOK)
	if result.AppliedCells != workbook.MaxBatchCells+1 || result.ServerVersion != 2 {
		t.Fatalf("paste result: %#v", result)
	}
	duplicate := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+created.Sheets[0].ID+"/cells:paste", body, http.StatusOK)
	if !duplicate.Duplicate || duplicate.ServerVersion != 2 {
		t.Fatalf("paste idempotency: %#v", duplicate)
	}
	selected := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+created.Sheets[0].ID+"/ranges/A1001", nil, http.StatusOK)
	if len(selected.Items) != 1 || string(selected.Items[0].Value) != "1001" {
		t.Fatalf("last pasted cell: %#v", selected.Items)
	}
	mcpCells := make([]map[string]any, len(cells))
	for index, cell := range cells {
		mcpCells[index] = map[string]any{"row": cell["row"], "column": 2, "value": cell["value"]}
	}
	mcpPaste := request[struct {
		Result struct {
			Structured workbook.MutationResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.range.paste", "arguments": map[string]any{"sheet_id": created.Sheets[0].ID, "base_version": 2, "idempotency_key": "mcp-paste-1001", "cells": mcpCells}}}, http.StatusOK)
	if mcpPaste.Result.Structured.AppliedCells != workbook.MaxBatchCells+1 || mcpPaste.Result.Structured.ServerVersion != 3 {
		t.Fatalf("MCP paste result: %#v", mcpPaste.Result.Structured)
	}
	tooMany := make([]map[string]any, workbook.MaxPasteCells+1)
	for index := range tooMany {
		tooMany[index] = map[string]any{"row": index + 1, "column": 2, "value": index}
	}
	request[map[string]any](t, server, http.MethodPatch, "/api/v1/sheets/"+created.Sheets[0].ID+"/cells:paste", map[string]any{"base_version": 3, "idempotency_key": "paste-too-large", "cells": tooMany}, http.StatusBadRequest)
}

func TestFillEndpointAndMCPAreAtomicAndUndoable(t *testing.T) {
	t.Parallel()
	fillTool, found := findMCPTool("spreadsheet.range.fill")
	if !found || fillTool.Meta["required_scope"] != "range.write" {
		t.Fatalf("MCP fill tool: %#v", fillTool)
	}
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "fill api"}, http.StatusCreated)
	sheetID := created.Sheets[0].ID
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", map[string]any{"base_version": 1, "idempotency_key": "fill-seed", "cells": []map[string]any{{"row": 1, "column": 1, "value": 1}, {"row": 2, "column": 1, "value": 2}}}, http.StatusOK)
	body := map[string]any{"base_version": 2, "idempotency_key": "fill-rest", "client_id": "browser", "cells": []map[string]any{{"row": 3, "column": 1, "value": 3}, {"row": 4, "column": 1, "value": 4}, {"row": 5, "column": 1, "value": 5}}}
	filled := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:fill", body, http.StatusOK)
	if filled.ServerVersion != 3 || filled.AppliedCells != 3 {
		t.Fatalf("REST fill: %#v", filled)
	}
	duplicate := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:fill", body, http.StatusOK)
	if !duplicate.Duplicate || duplicate.ServerVersion != 3 {
		t.Fatalf("REST fill idempotency: %#v", duplicate)
	}
	mcpFill := request[struct {
		Result struct {
			Structured workbook.MutationResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.range.fill", "arguments": map[string]any{"sheet_id": sheetID, "base_version": 3, "idempotency_key": "fill-mcp", "client_id": "agent", "cells": []map[string]any{{"row": 1, "column": 2, "formula": "=A1*10"}, {"row": 2, "column": 2, "formula": "=A2*10"}}}}}, http.StatusOK)
	if mcpFill.Result.Structured.ServerVersion != 4 || mcpFill.Result.Structured.AppliedCells != 2 || len(mcpFill.Result.Structured.RecalculatedCells) != 2 {
		t.Fatalf("MCP fill: %#v", mcpFill.Result.Structured)
	}
	selected := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:B5", nil, http.StatusOK)
	if len(selected.Items) != 7 {
		t.Fatalf("filled range: %#v", selected.Items)
	}
	undone := request[workbook.MutationResult](t, server, http.MethodPost, "/api/v1/operations/"+filled.OperationID+":undo", map[string]any{"idempotency_key": "undo-fill", "client_id": "browser"}, http.StatusOK)
	if undone.ServerVersion != 5 || undone.AppliedCells != 3 {
		t.Fatalf("fill undo: %#v", undone)
	}
	selected = request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:B5", nil, http.StatusOK)
	if len(selected.Items) != 4 {
		t.Fatalf("range after fill undo: %#v", selected.Items)
	}
}

func TestRangeFormatRESTAndMCPPreserveContent(t *testing.T) {
	t.Parallel()
	formatTool, found := findMCPTool("spreadsheet.range.format")
	required, requiredOK := formatTool.InputSchema["required"].([]string)
	if !found || formatTool.Meta["required_scope"] != "format.write" || !requiredOK || len(required) != 4 {
		t.Fatalf("MCP format tool schema: %#v", formatTool)
	}
	if scope := requiredScope(httptest.NewRequest(http.MethodPatch, "/api/v1/sheets/sheet-id/ranges:format", nil)); scope != "format.write" {
		t.Fatalf("REST format scope = %q", scope)
	}
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "format api"}, http.StatusCreated)
	sheetID := created.Sheets[0].ID
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", map[string]any{
		"base_version": 1, "idempotency_key": "format-seed", "cells": []map[string]any{{"row": 1, "column": 1, "value": 5}, {"row": 2, "column": 1, "formula": "=A1*2"}},
	}, http.StatusOK)

	body := map[string]any{"base_version": 2, "idempotency_key": "format-rest", "client_id": "browser", "range": "A1:A3", "style": map[string]any{"bold": true, "background": "#fef3c7", "horizontal_align": "center"}}
	formatted := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:format", body, http.StatusOK)
	if formatted.ServerVersion != 3 || formatted.AppliedCells != 3 || len(formatted.RecalculatedCells) != 0 {
		t.Fatalf("REST format result: %#v", formatted)
	}
	duplicate := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:format", body, http.StatusOK)
	if !duplicate.Duplicate || duplicate.ServerVersion != 3 {
		t.Fatalf("REST format idempotency: %#v", duplicate)
	}
	formattedCells := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:A3", nil, http.StatusOK)
	if len(formattedCells.Items) != 3 || string(formattedCells.Items[0].Value) != "5" || formattedCells.Items[1].Formula != "=A1*2" || string(formattedCells.Items[1].Value) != "10" {
		t.Fatalf("REST format changed content: %#v", formattedCells.Items)
	}
	for _, cell := range formattedCells.Items {
		var style map[string]any
		if json.Unmarshal(cell.Style, &style) != nil || style["bold"] != true || style["background"] != "#fef3c7" || style["horizontal_align"] != "center" {
			t.Fatalf("REST cell style: %s", cell.Style)
		}
	}

	mcpFormat := request[struct {
		Result struct {
			Structured workbook.MutationResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.range.format", "arguments": map[string]any{"sheet_id": sheetID, "base_version": 3, "idempotency_key": "format-mcp", "client_id": "agent", "range": "A1:A3", "style": map[string]any{"italic": true, "color": "#2563eb"}}}}, http.StatusOK)
	if mcpFormat.Result.Structured.ServerVersion != 4 || mcpFormat.Result.Structured.AppliedCells != 3 {
		t.Fatalf("MCP format result: %#v", mcpFormat.Result.Structured)
	}
	formattedCells = request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:A3", nil, http.StatusOK)
	var mergedStyle map[string]any
	_ = json.Unmarshal(formattedCells.Items[1].Style, &mergedStyle)
	if string(formattedCells.Items[0].Value) != "5" || formattedCells.Items[1].Formula != "=A1*2" || mergedStyle["bold"] != true || mergedStyle["italic"] != true || mergedStyle["color"] != "#2563eb" {
		t.Fatalf("MCP format did not merge style: cells=%#v style=%#v", formattedCells.Items, mergedStyle)
	}

	request[map[string]any](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:format", map[string]any{"base_version": 4, "idempotency_key": "format-invalid", "range": "A1", "style": map[string]any{"color": "red"}}, http.StatusBadRequest)
	request[map[string]any](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:format", map[string]any{"base_version": 4, "idempotency_key": "format-large", "range": "A1:A10001", "style": map[string]any{"bold": true}}, http.StatusBadRequest)
}

func TestRangeMergeRESTMCPAndUndoPreserveEveryCell(t *testing.T) {
	t.Parallel()
	mergeTool, mergeFound := findMCPTool("spreadsheet.range.merge")
	unmergeTool, unmergeFound := findMCPTool("spreadsheet.range.unmerge")
	if !mergeFound || !unmergeFound || mergeTool.Meta["required_scope"] != "format.write" || unmergeTool.Meta["required_scope"] != "format.write" {
		t.Fatalf("merge MCP tools: merge=%#v unmerge=%#v", mergeTool, unmergeTool)
	}
	for _, action := range []string{"merge", "unmerge"} {
		if scope := requiredScope(httptest.NewRequest(http.MethodPatch, "/api/v1/sheets/sheet-id/ranges:"+action, nil)); scope != "format.write" {
			t.Fatalf("%s REST scope = %q", action, scope)
		}
	}
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "merge api"}, http.StatusCreated)
	sheetID := created.Sheets[0].ID
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", map[string]any{"base_version": 1, "idempotency_key": "merge-seed", "cells": []map[string]any{{"row": 1, "column": 1, "value": "title", "style": map[string]any{"bold": true}}, {"row": 2, "column": 2, "value": 9}}}, http.StatusOK)
	body := map[string]any{"base_version": 2, "idempotency_key": "merge-rest", "client_id": "browser", "range": "A1:B2"}
	merged := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:merge", body, http.StatusOK)
	if merged.ServerVersion != 3 || merged.AppliedCells != 4 {
		t.Fatalf("REST merge: %#v", merged)
	}
	duplicate := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:merge", body, http.StatusOK)
	if !duplicate.Duplicate || duplicate.ServerVersion != 3 {
		t.Fatalf("merge idempotency: %#v", duplicate)
	}
	selected := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:B2", nil, http.StatusOK)
	if len(selected.Items) != 4 || string(selected.Items[0].Value) != `"title"` || string(selected.Items[3].Value) != "9" {
		t.Fatalf("merged cells lost content: %#v", selected.Items)
	}
	for _, cell := range selected.Items {
		metadata, exists, err := workbook.CellMerge(cell)
		if err != nil || !exists || metadata.StartRow != 1 || metadata.EndColumn != 2 {
			t.Fatalf("merge metadata: cell=%#v metadata=%#v exists=%v err=%v", cell, metadata, exists, err)
		}
	}
	unmerged := request[struct {
		Result struct {
			Structured workbook.MutationResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.range.unmerge", "arguments": map[string]any{"sheet_id": sheetID, "range": "A1:B2", "base_version": 3, "idempotency_key": "unmerge-mcp", "client_id": "agent"}}}, http.StatusOK)
	if unmerged.Result.Structured.ServerVersion != 4 || unmerged.Result.Structured.AppliedCells != 4 {
		t.Fatalf("MCP unmerge: %#v", unmerged.Result.Structured)
	}
	undone := request[workbook.MutationResult](t, server, http.MethodPost, "/api/v1/operations/"+unmerged.Result.Structured.OperationID+":undo", map[string]any{"idempotency_key": "undo-unmerge", "client_id": "agent"}, http.StatusOK)
	if undone.ServerVersion != 5 || undone.AppliedCells != 4 {
		t.Fatalf("unmerge undo: %#v", undone)
	}
	selected = request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:B2", nil, http.StatusOK)
	for _, cell := range selected.Items {
		if _, exists, err := workbook.CellMerge(cell); err != nil || !exists {
			t.Fatalf("undo did not restore merge: cell=%#v exists=%v err=%v", cell, exists, err)
		}
	}
}

func TestRangeSortRESTMCPIsStableAtomicAndUndoable(t *testing.T) {
	t.Parallel()
	sortTool, found := findMCPTool("spreadsheet.range.sort")
	if !found || sortTool.Meta["required_scope"] != "range.write" {
		t.Fatalf("sort MCP tool: %#v", sortTool)
	}
	if scope := requiredScope(httptest.NewRequest(http.MethodPatch, "/api/v1/sheets/sheet-id/ranges:sort", nil)); scope != "range.write" {
		t.Fatalf("sort REST scope = %q", scope)
	}
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "sort api"}, http.StatusCreated)
	sheetID := created.Sheets[0].ID
	seed := []map[string]any{
		{"row": 1, "column": 1, "value": "Name"}, {"row": 1, "column": 2, "value": "Quantity"}, {"row": 1, "column": 3, "value": "Total"},
		{"row": 2, "column": 1, "value": "beta"}, {"row": 2, "column": 2, "value": 2}, {"row": 2, "column": 3, "formula": "=B2*10", "style": map[string]any{"bold": true}},
		{"row": 3, "column": 1, "value": "Alpha"}, {"row": 3, "column": 2, "value": 10}, {"row": 3, "column": 3, "formula": "=B3*10"},
		{"row": 4, "column": 1, "value": "alpha"}, {"row": 4, "column": 2, "value": 5}, {"row": 4, "column": 3, "formula": "=B4*10"},
	}
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", map[string]any{"base_version": 1, "idempotency_key": "sort-seed", "cells": seed}, http.StatusOK)
	body := map[string]any{"base_version": 2, "idempotency_key": "sort-rest", "client_id": "browser", "range": "A1:C4", "header_rows": 1, "keys": []map[string]any{{"column": 1, "direction": "asc"}, {"column": 2, "direction": "desc"}}}
	sorted := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:sort", body, http.StatusOK)
	if sorted.ServerVersion != 3 || sorted.AppliedCells != 9 {
		t.Fatalf("REST sort: %#v", sorted)
	}
	duplicate := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/ranges:sort", body, http.StatusOK)
	if !duplicate.Duplicate || duplicate.ServerVersion != 3 {
		t.Fatalf("sort idempotency: %#v", duplicate)
	}
	selected := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:C4", nil, http.StatusOK)
	if string(selected.Items[3].Value) != `"Alpha"` || string(selected.Items[6].Value) != `"alpha"` || string(selected.Items[9].Value) != `"beta"` || selected.Items[11].Formula != "=B4*10" {
		t.Fatalf("REST sorted cells: %#v", selected.Items)
	}
	var movedStyle map[string]any
	_ = json.Unmarshal(selected.Items[11].Style, &movedStyle)
	if movedStyle["bold"] != true || string(selected.Items[11].Value) != "20" {
		t.Fatalf("sorted formula/style: cell=%#v style=%#v", selected.Items[11], movedStyle)
	}
	mcpSorted := request[struct {
		Result struct {
			Structured workbook.MutationResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 8, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.range.sort", "arguments": map[string]any{"sheet_id": sheetID, "range": "A1:C4", "base_version": 3, "idempotency_key": "sort-mcp", "client_id": "agent", "header_rows": 1, "keys": []map[string]any{{"column": 2, "direction": "asc"}}}}}, http.StatusOK)
	if mcpSorted.Result.Structured.ServerVersion != 4 || mcpSorted.Result.Structured.AppliedCells != 9 {
		t.Fatalf("MCP sort: %#v", mcpSorted.Result.Structured)
	}
	undone := request[workbook.MutationResult](t, server, http.MethodPost, "/api/v1/operations/"+mcpSorted.Result.Structured.OperationID+":undo", map[string]any{"idempotency_key": "undo-sort", "client_id": "agent"}, http.StatusOK)
	if undone.ServerVersion != 5 || undone.AppliedCells != 9 {
		t.Fatalf("sort undo: %#v", undone)
	}
	selected = request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:C4", nil, http.StatusOK)
	if string(selected.Items[3].Value) != `"Alpha"` || string(selected.Items[9].Value) != `"beta"` {
		t.Fatalf("sort undo order: %#v", selected.Items)
	}
}

func TestFilterViewRESTAndMCPCRUDUseLatestCellsAndPersonalScopes(t *testing.T) {
	t.Parallel()
	for name, scope := range map[string]string{
		"spreadsheet.filter_view.list": "range.read", "spreadsheet.filter_view.get": "range.read", "spreadsheet.filter_view.evaluate": "range.read",
		"spreadsheet.filter_view.create": "range.write", "spreadsheet.filter_view.update": "range.write", "spreadsheet.filter_view.delete": "range.write",
	} {
		tool, found := findMCPTool(name)
		if !found || tool.Meta["required_scope"] != scope {
			t.Fatalf("filter tool %s: %#v", name, tool)
		}
	}
	createTool, _ := findMCPTool("spreadsheet.filter_view.create")
	properties, _ := createTool.InputSchema["properties"].(map[string]any)
	criteriaSchema, _ := properties["criteria"].(map[string]any)
	criterionItems, _ := criteriaSchema["items"].(map[string]any)
	criterionProperties, _ := criterionItems["properties"].(map[string]any)
	if properties["active"] == nil || properties["header_rows"] == nil || criterionProperties["operator"] == nil || criterionProperties["values"] == nil || criterionProperties["color"] == nil {
		t.Fatalf("filter MCP schema is incomplete: %#v", createTool.InputSchema)
	}
	if scope := requiredScope(httptest.NewRequest(http.MethodGet, "/api/v1/sheets/s/filter-views", nil)); scope != "range.read" {
		t.Fatalf("filter list scope = %q", scope)
	}
	if scope := requiredScope(httptest.NewRequest(http.MethodPost, "/api/v1/filter-views/id:evaluate", nil)); scope != "range.read" {
		t.Fatalf("filter evaluate scope = %q", scope)
	}
	if scope := requiredScope(httptest.NewRequest(http.MethodPost, "/api/v1/sheets/s/filter-views", nil)); scope != "range.write" {
		t.Fatalf("filter create scope = %q", scope)
	}
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "filter api"}, http.StatusCreated)
	sheetID := created.Sheets[0].ID
	seed := []map[string]any{
		{"row": 1, "column": 1, "value": "Region"}, {"row": 1, "column": 2, "value": "Amount"}, {"row": 1, "column": 3, "value": "Status"},
		{"row": 2, "column": 1, "value": "Seoul"}, {"row": 2, "column": 2, "value": 12}, {"row": 2, "column": 3, "value": "open", "style": map[string]any{"background": "#fef3c7"}},
		{"row": 3, "column": 1, "value": "Busan"}, {"row": 3, "column": 2, "value": 7}, {"row": 3, "column": 3, "value": "open", "style": map[string]any{"background": "#fef3c7"}},
		{"row": 4, "column": 1, "value": "Daejeon"}, {"row": 4, "column": 2, "value": 20}, {"row": 4, "column": 3, "value": "open", "style": map[string]any{"background": "#fef3c7"}},
		{"row": 5, "column": 1, "value": "Seoul"}, {"row": 5, "column": 2, "value": 15}, {"row": 5, "column": 3, "value": "closed", "style": map[string]any{"background": "#ffffff"}},
	}
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", map[string]any{"base_version": 1, "idempotency_key": "filter-seed", "cells": seed}, http.StatusOK)
	createBody := map[string]any{"idempotency_key": "filter-view-create", "name": "qualified", "range": "A1:C5", "header_rows": 1, "active": true, "criteria": []map[string]any{{"column": 1, "operator": "values", "values": []any{"Seoul", "Busan"}}, {"column": 2, "operator": "greater_or_equal", "value": 10}, {"column": 3, "operator": "background_color", "color": "#fef3c7"}}}
	view := request[workbook.FilterView](t, server, http.MethodPost, "/api/v1/sheets/"+sheetID+"/filter-views", createBody, http.StatusCreated)
	duplicate := request[workbook.FilterView](t, server, http.MethodPost, "/api/v1/sheets/"+sheetID+"/filter-views", map[string]any{"idempotency_key": "filter-view-create", "name": "", "range": "invalid"}, http.StatusCreated)
	if view.ID == "" || duplicate.ID != view.ID || !view.Active {
		t.Fatalf("created filter: view=%#v duplicate=%#v", view, duplicate)
	}
	result := request[workbook.FilterResult](t, server, http.MethodPost, "/api/v1/filter-views/"+view.ID+":evaluate", map[string]any{}, http.StatusOK)
	if result.VisibleCount != 1 || result.HiddenCount != 3 || !reflect.DeepEqual(result.HiddenRows, []int{3, 4, 5}) {
		t.Fatalf("REST filter result: %#v", result)
	}
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+sheetID+"/cells:batch", map[string]any{"base_version": 2, "idempotency_key": "filter-latest-cell", "cells": []map[string]any{{"row": 3, "column": 2, "value": 11}}}, http.StatusOK)
	result = request[workbook.FilterResult](t, server, http.MethodPost, "/api/v1/filter-views/"+view.ID+":evaluate", map[string]any{}, http.StatusOK)
	if !reflect.DeepEqual(result.HiddenRows, []int{4, 5}) {
		t.Fatalf("filter did not use latest cells: %#v", result)
	}
	listed := request[struct {
		Items []workbook.FilterView `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/filter-views", nil, http.StatusOK)
	if len(listed.Items) != 1 || listed.Items[0].ID != view.ID {
		t.Fatalf("filter list: %#v", listed.Items)
	}
	mcpCreated := request[struct {
		Result struct {
			Structured workbook.FilterView `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 11, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.filter_view.create", "arguments": map[string]any{"sheet_id": sheetID, "idempotency_key": "filter-mcp-create", "name": "amounts", "range": "A1:C5", "header_rows": 1, "active": true, "criteria": []map[string]any{{"column": 2, "operator": "greater_than", "value": 15}}}}}, http.StatusOK)
	if mcpCreated.Result.Structured.ID == "" || !mcpCreated.Result.Structured.Active {
		t.Fatalf("MCP filter create: %#v", mcpCreated)
	}
	view = request[workbook.FilterView](t, server, http.MethodGet, "/api/v1/filter-views/"+view.ID, nil, http.StatusOK)
	if view.Active {
		t.Fatalf("previous filter remained active: %#v", view)
	}
	mcpUpdated := request[struct {
		Result struct {
			Structured workbook.FilterView `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 14, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.filter_view.update", "arguments": map[string]any{"filter_view_id": mcpCreated.Result.Structured.ID, "name": "amounts-updated"}}}, http.StatusOK)
	if mcpUpdated.Result.Structured.Name != "amounts-updated" {
		t.Fatalf("MCP filter update: %#v", mcpUpdated)
	}
	mcpGet := request[struct {
		Result struct {
			Structured workbook.FilterView `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 15, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.filter_view.get", "arguments": map[string]any{"filter_view_id": mcpCreated.Result.Structured.ID}}}, http.StatusOK)
	if mcpGet.Result.Structured.Name != "amounts-updated" {
		t.Fatalf("MCP filter get: %#v", mcpGet)
	}
	mcpList := request[struct {
		Result struct {
			Structured []workbook.FilterView `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 16, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.filter_view.list", "arguments": map[string]any{"sheet_id": sheetID}}}, http.StatusOK)
	if len(mcpList.Result.Structured) != 2 {
		t.Fatalf("MCP filter list: %#v", mcpList)
	}
	mcpEvaluated := request[struct {
		Result struct {
			Structured workbook.FilterResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 12, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.filter_view.evaluate", "arguments": map[string]any{"filter_view_id": mcpCreated.Result.Structured.ID}}}, http.StatusOK)
	if !reflect.DeepEqual(mcpEvaluated.Result.Structured.HiddenRows, []int{2, 3, 5}) {
		t.Fatalf("MCP filter evaluate: %#v", mcpEvaluated)
	}
	request[map[string]any](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 13, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.filter_view.delete", "arguments": map[string]any{"filter_view_id": mcpCreated.Result.Structured.ID}}}, http.StatusOK)
	request[map[string]any](t, server, http.MethodGet, "/api/v1/filter-views/"+mcpCreated.Result.Structured.ID, nil, http.StatusNotFound)
	request[map[string]any](t, server, http.MethodDelete, "/api/v1/filter-views/"+view.ID, nil, http.StatusNoContent)
	request[map[string]any](t, server, http.MethodGet, "/api/v1/filter-views/"+view.ID, nil, http.StatusNotFound)
}

func TestSheetDuplicateRESTAndMCPPreserveData(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	created := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "sheet duplicate"}, http.StatusCreated)
	source := created.Sheets[0]
	request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+source.ID+"/cells:batch", map[string]any{"base_version": 1, "idempotency_key": "sheet-data", "cells": []map[string]any{{"row": 1, "column": 1, "value": 42}}}, http.StatusOK)
	restCopy := request[workbook.Sheet](t, server, http.MethodPost, "/api/v1/sheets/"+source.ID+"/duplicate", map[string]any{}, http.StatusCreated)
	if restCopy.Name != "Sheet1 복사본" || restCopy.Position != 1 {
		t.Fatalf("REST duplicate: %#v", restCopy)
	}
	copied := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+restCopy.ID+"/ranges/A1", nil, http.StatusOK)
	if len(copied.Items) != 1 || string(copied.Items[0].Value) != "42" {
		t.Fatalf("REST duplicate data: %#v", copied.Items)
	}
	mcpCopy := request[struct {
		Result struct {
			Structured workbook.Sheet `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.sheet.duplicate", "arguments": map[string]any{"sheet_id": source.ID, "name": "Agent Copy"}}}, http.StatusOK)
	if mcpCopy.Result.Structured.Name != "Agent Copy" || mcpCopy.Result.Structured.Position != 1 {
		t.Fatalf("MCP duplicate: %#v", mcpCopy.Result.Structured)
	}
	book := request[workbook.Workbook](t, server, http.MethodGet, "/api/v1/workbooks/"+created.ID, nil, http.StatusOK)
	if book.Version != 4 || len(book.Sheets) != 3 || book.Sheets[0].Name != "Sheet1" || book.Sheets[1].Name != "Agent Copy" || book.Sheets[2].Name != "Sheet1 복사본" {
		t.Fatalf("sheet order after MCP duplicate: %#v", book)
	}
}

func TestFormulaEvaluationAndStoredResult(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	result := request[struct {
		Value        any      `json:"value"`
		Dependencies []string `json:"dependencies"`
	}](t, server, http.MethodPost, "/api/v1/formulas:evaluate", map[string]any{"formula": "=SUM(A1:A2)*2", "cells": map[string]any{"A1": 2, "A2": 3}}, http.StatusOK)
	if result.Value != float64(10) || len(result.Dependencies) != 2 {
		t.Fatalf("formula result: %#v", result)
	}
	wb := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "formula"}, http.StatusCreated)
	path := "/api/v1/sheets/" + wb.Sheets[0].ID + "/cells:batch"
	request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 1, "idempotency_key": "values", "cells": []map[string]any{{"row": 1, "column": 1, "value": 2}, {"row": 2, "column": 1, "value": 3}}}, http.StatusOK)
	initial := request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 2, "idempotency_key": "formula", "cells": []map[string]any{{"row": 3, "column": 1, "value": 999, "formula": "=SUM(A1:A2)"}, {"row": 4, "column": 1, "formula": "=A3*2"}}}, http.StatusOK)
	if len(initial.RecalculatedCells) != 2 || len(initial.FormulaErrors) != 0 {
		t.Fatalf("initial formula metadata: %#v", initial)
	}
	cells := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+wb.Sheets[0].ID+"/ranges/A3:A4", nil, http.StatusOK)
	if len(cells.Items) != 2 || string(cells.Items[0].Value) != "5" || cells.Items[0].Formula != "=SUM(A1:A2)" || string(cells.Items[1].Value) != "10" {
		t.Fatalf("stored formula: %#v", cells.Items)
	}

	updated := request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 3, "idempotency_key": "recalculate", "cells": []map[string]any{{"row": 1, "column": 1, "value": 7}}}, http.StatusOK)
	if updated.AppliedCells != 1 || len(updated.RecalculatedCells) != 2 {
		t.Fatalf("transitive recalculation metadata: %#v", updated)
	}
	cells = request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+wb.Sheets[0].ID+"/ranges/A3:A4", nil, http.StatusOK)
	if string(cells.Items[0].Value) != "10" || string(cells.Items[1].Value) != "20" {
		t.Fatalf("transitive recalculation values: %#v", cells.Items)
	}
	info := request[formulaInfoResult](t, server, http.MethodGet, "/api/v1/sheets/"+wb.Sheets[0].ID+"/formulas/A3", nil, http.StatusOK)
	if len(info.Dependencies) != 2 || info.Dependencies[0] != "A1" || info.Dependencies[1] != "A2" || len(info.Dependents) != 1 || info.Dependents[0] != "A4" {
		t.Fatalf("formula graph info: %#v", info)
	}
	mcpInfo := request[struct {
		Result struct {
			Structured formulaInfoResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.formula.explain", "arguments": map[string]any{"sheet_id": wb.Sheets[0].ID, "address": "A3"}}}, http.StatusOK)
	if len(mcpInfo.Result.Structured.Dependents) != 1 || mcpInfo.Result.Structured.Dependents[0] != "A4" {
		t.Fatalf("MCP formula graph info: %#v", mcpInfo)
	}
}

func TestCircularFormulaIsStoredAsExplicitError(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	wb := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "cycle"}, http.StatusCreated)
	result := request[workbook.MutationResult](t, server, http.MethodPatch, "/api/v1/sheets/"+wb.Sheets[0].ID+"/cells:batch", map[string]any{
		"base_version": 1, "idempotency_key": "cycle-1", "cells": []map[string]any{
			{"row": 1, "column": 1, "formula": "=B1+1"},
			{"row": 1, "column": 2, "formula": "=A1+1"},
			{"row": 1, "column": 3, "formula": "=A1+10"},
		},
	}, http.StatusOK)
	if len(result.RecalculatedCells) != 3 || len(result.FormulaErrors) != 3 {
		t.Fatalf("cycle metadata: %#v", result)
	}
	for _, formulaErr := range result.FormulaErrors {
		if formulaErr.Code != "#CIRC!" {
			t.Fatalf("cycle error: %#v", formulaErr)
		}
	}
	cells := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+wb.Sheets[0].ID+"/ranges/A1:C1", nil, http.StatusOK)
	for _, cell := range cells.Items {
		if string(cell.Value) != `"#CIRC!"` {
			t.Fatalf("cycle value: %#v", cell)
		}
	}
}

func TestOperationUndoRedoRESTAndMCP(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	wb := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "undo api"}, http.StatusCreated)
	path := "/api/v1/sheets/" + wb.Sheets[0].ID + "/cells:batch"
	request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 1, "idempotency_key": "undo-seed", "cells": []map[string]any{{"row": 1, "column": 1, "value": 2}, {"row": 1, "column": 2, "formula": "=A1*2"}}}, http.StatusOK)
	changed := request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 2, "idempotency_key": "undo-change", "cells": []map[string]any{{"row": 1, "column": 1, "value": 3}}}, http.StatusOK)
	undone := request[workbook.MutationResult](t, server, http.MethodPost, "/api/v1/operations/"+changed.OperationID+":undo", map[string]any{"idempotency_key": "undo-rest", "client_id": "test"}, http.StatusOK)
	if undone.AppliedCells != 1 || len(undone.RecalculatedCells) != 1 {
		t.Fatalf("REST undo: %#v", undone)
	}
	assertHTTPRangeValues(t, server, wb.Sheets[0].ID, []string{"2", "4"})
	redone := request[workbook.MutationResult](t, server, http.MethodPost, "/api/v1/operations/"+undone.OperationID+":undo", map[string]any{"idempotency_key": "redo-rest", "client_id": "test"}, http.StatusOK)
	if redone.AppliedCells != 1 {
		t.Fatalf("REST redo: %#v", redone)
	}
	assertHTTPRangeValues(t, server, wb.Sheets[0].ID, []string{"3", "6"})

	mcpUndo := request[struct {
		Result struct {
			Structured workbook.MutationResult `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.operation.undo", "arguments": map[string]any{"operation_id": redone.OperationID, "idempotency_key": "undo-mcp", "client_id": "agent"}}}, http.StatusOK)
	if mcpUndo.Result.Structured.AppliedCells != 1 {
		t.Fatalf("MCP undo: %#v", mcpUndo)
	}
	assertHTTPRangeValues(t, server, wb.Sheets[0].ID, []string{"2", "4"})
}

func assertHTTPRangeValues(t *testing.T, server *httptest.Server, sheetID string, expected []string) {
	t.Helper()
	cells := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+sheetID+"/ranges/A1:B1", nil, http.StatusOK)
	if len(cells.Items) != len(expected) {
		t.Fatalf("range cell count = %d, want %d", len(cells.Items), len(expected))
	}
	for index, value := range expected {
		if string(cells.Items[index].Value) != value {
			t.Fatalf("range cell %d = %s, want %s", index, cells.Items[index].Value, value)
		}
	}
}

func TestImportExportAndMCPFileTools(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	csvData := []byte("name,amount\nalpha,10\nbeta,20\n")
	previewResponse := multipartRequest(t, server, http.MethodPost, "/api/v1/imports:preview", "data.csv", csvData, "", http.StatusOK)
	var preview struct {
		TotalCells int `json:"total_cells"`
	}
	if err := json.Unmarshal(previewResponse, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.TotalCells != 6 {
		t.Fatalf("preview cells = %d", preview.TotalCells)
	}
	importResponse := multipartRequest(t, server, http.MethodPost, "/api/v1/imports", "data.csv", csvData, "import-http-1", http.StatusCreated)
	var created workbook.Workbook
	if err := json.Unmarshal(importResponse, &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Sheets) != 1 {
		t.Fatalf("created workbook: %#v", created)
	}
	exportRequest, _ := json.Marshal(map[string]any{"workbook_id": created.ID, "format": "csv"})
	response, err := http.Post(server.URL+"/api/v1/exports", "application/json", bytes.NewReader(exportRequest))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	exported, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains(exported, []byte("alpha,10")) {
		t.Fatalf("export: %d %s", response.StatusCode, exported)
	}

	mcpResult := request[struct {
		Result struct {
			Structured map[string]any `json:"structuredContent"`
		} `json:"result"`
	}](t, server, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "spreadsheet.import.preview", "arguments": map[string]any{"file_name": "agent.csv", "data_base64": base64.StdEncoding.EncodeToString(csvData)}}}, http.StatusOK)
	if mcpResult.Result.Structured["total_cells"] != float64(6) {
		t.Fatalf("MCP preview: %#v", mcpResult)
	}
}

func multipartRequest(t *testing.T, server *httptest.Server, method, path, fileName string, data []byte, idempotencyKey string, status int) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	_ = writer.WriteField("workspace_id", "default")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, server.URL+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("%s: status %d body %s", path, response.StatusCode, result)
	}
	return result
}

func request[T any](t *testing.T, server *httptest.Server, method, path string, input any, status int) T {
	t.Helper()
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s: status %d, body %s", method, path, response.StatusCode, data)
	}
	var result T
	if len(data) > 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("decode response %s: %v", data, err)
		}
	}
	return result
}
