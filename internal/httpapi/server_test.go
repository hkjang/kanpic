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
	request[workbook.MutationResult](t, server, http.MethodPatch, path, map[string]any{"base_version": 2, "idempotency_key": "formula", "cells": []map[string]any{{"row": 3, "column": 1, "formula": "=SUM(A1:A2)"}}}, http.StatusOK)
	cells := request[struct {
		Items []workbook.Cell `json:"items"`
	}](t, server, http.MethodGet, "/api/v1/sheets/"+wb.Sheets[0].ID+"/ranges/A3", nil, http.StatusOK)
	if len(cells.Items) != 1 || string(cells.Items[0].Value) != "5" || cells.Items[0].Formula != "=SUM(A1:A2)" {
		t.Fatalf("stored formula: %#v", cells.Items)
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
