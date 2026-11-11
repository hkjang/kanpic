package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kanpic/internal/auth"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// readerToken is the kind of credential an MCP integration account gets from
// Keycloak when nobody added write scopes to its client: workbook.read only.
const readerToken = "eyJhbGciOiJSUzI1NiJ9.reader.sig"

type gatewayFixture struct {
	server     *httptest.Server
	repository *workbook.MemoryRepository
	own        workbook.Workbook
	shared     workbook.Workbook
}

func newGatewayFixture(t *testing.T) gatewayFixture {
	t.Helper()
	ctx := context.Background()
	repository := workbook.NewMemoryRepository()
	own, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "내 워크북", OwnerID: "mcp-agent"})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "남의 워크북", OwnerID: "someone-else"})
	if err != nil {
		t.Fatal(err)
	}
	// Shared as an editor, so the role would allow writing; only the token's
	// scopes stand in the way, and they must keep standing there.
	if _, err := repository.PutWorkbookShare(ctx, shared.ID, workbook.ShareInput{PrincipalType: workbook.PrincipalUser, PrincipalID: "mcp-agent", Role: workbook.RoleEditor, ActorID: "someone-else"}); err != nil {
		t.Fatal(err)
	}
	tokens := fakeTokens{enabled: true, tokens: map[string]auth.AccessToken{
		readerToken: {User: auth.User{ID: "mcp-agent", DisplayName: "MCP 연동", ExpiresAt: time.Now().Add(time.Hour)}, Scopes: []string{"workbook.read"}, ClientID: "gateway"},
		fullToken:   {User: auth.User{ID: "mcp-agent", DisplayName: "MCP 연동", ExpiresAt: time.Now().Add(time.Hour)}, ClientID: "gateway"},
	}}
	handler := NewPlatformWithServices(repository, nil, nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), WithAccessTokens(tokens))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return gatewayFixture{server: server, repository: repository, own: own, shared: shared}
}

// rawToolCall posts a tools/call whose params are written out by hand, the way
// a gateway re-encodes them.
func rawToolCall(t *testing.T, server *httptest.Server, token, params string, headers map[string]string) (map[string]any, string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var envelope struct {
		Result struct {
			IsError    bool           `json:"isError"`
			Structured map[string]any `json:"structuredContent"`
			Content    []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("not JSON-RPC: %s", raw)
	}
	if envelope.Error != nil {
		return nil, "protocol: " + envelope.Error.Message
	}
	if envelope.Result.IsError {
		return nil, envelope.Result.Content[0].Text
	}
	return envelope.Result.Structured, ""
}

func cellValue(t *testing.T, repository *workbook.MemoryRepository, sheetID, address string) string {
	t.Helper()
	selected, err := cellrange.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	cells, err := repository.ReadRange(context.Background(), sheetID, selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) == 0 {
		return ""
	}
	if cells[0].Formula != "" {
		return cells[0].Formula
	}
	return string(cells[0].Value)
}

// The integration account's token says workbook.read. On the workbook that
// account owns it still writes cells, sets formulas, formats ranges and reads
// conditional formats and automations — the scopes the request asked for —
// because mcp.owner_scopes adds them there by default.
func TestMCPOwnerScopesLetANarrowedTokenEditItsOwnWorkbook(t *testing.T) {
	t.Parallel()
	fixture := newGatewayFixture(t)
	sheet := fixture.own.Sheets[0].ID

	_, refusal := mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.range.write", map[string]any{
		"sheet_id": sheet, "idempotency_key": "owner-write", "cells": []any{map[string]any{"row": 1, "column": 1, "value": "안녕"}},
	})
	if refusal != "" {
		t.Fatalf("range.write on the owner's workbook: %s", refusal)
	}
	if got := cellValue(t, fixture.repository, sheet, "A1"); got != `"안녕"` {
		t.Fatalf("A1=%s", got)
	}
	if _, refusal := mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.formula.set", map[string]any{
		"sheet_id": sheet, "idempotency_key": "owner-formula", "row": 2, "column": 1, "formula": "=1+1",
	}); refusal != "" {
		t.Fatalf("formula.set on the owner's workbook: %s", refusal)
	}
	if got := cellValue(t, fixture.repository, sheet, "A2"); got != "=1+1" {
		t.Fatalf("A2=%s", got)
	}
	if _, refusal := mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.range.format", map[string]any{
		"sheet_id": sheet, "idempotency_key": "owner-format", "range": "A1:A2", "style": map[string]any{"bold": true},
	}); refusal != "" {
		t.Fatalf("range.format (format.write) on the owner's workbook: %s", refusal)
	}
	if _, refusal := mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.conditional_format.list", map[string]any{"sheet_id": sheet}); refusal != "" {
		t.Fatalf("conditional_format.list (format.read) on the owner's workbook: %s", refusal)
	}
	// No automation service runs in this test server, so the call reaching the
	// service — rather than stopping at the scope check — is the proof.
	_, refusal = mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.automation.list", map[string]any{"workbook_id": fixture.own.ID})
	if strings.Contains(refusal, "insufficient scope") || !strings.Contains(refusal, "automation is disabled") {
		t.Fatalf("automation.list must pass the scope check on the owner's workbook: %q", refusal)
	}
}

// Ownership widens nothing elsewhere: the same token on a workbook only shared
// with its user stays read-only, and sharing or deleting the owner's own
// workbook still needs workbook.write.
func TestMCPOwnerScopesStopAtOtherWorkbooksAndAtSharing(t *testing.T) {
	t.Parallel()
	fixture := newGatewayFixture(t)
	_, refusal := mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.range.write", map[string]any{
		"sheet_id": fixture.shared.Sheets[0].ID, "idempotency_key": "not-mine", "cells": []any{map[string]any{"row": 1, "column": 1, "value": 1}},
	})
	if !strings.Contains(refusal, "insufficient scope: range.write") {
		t.Fatalf("a shared workbook keeps the token's scopes: %q", refusal)
	}
	_, refusal = mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.share.link", map[string]any{"workbook_id": fixture.own.ID, "link_access": "anyone"})
	if !strings.Contains(refusal, "insufficient scope: workbook.write") {
		t.Fatalf("sharing is never an owner default: %q", refusal)
	}
	_, refusal = mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.workbook.delete", map[string]any{"workbook_id": fixture.own.ID})
	if !strings.Contains(refusal, "insufficient scope: workbook.write") {
		t.Fatalf("deleting is never an owner default: %q", refusal)
	}
	// Naming the owner's sheet and someone else's workbook in one call must
	// not borrow the owner's widening for the other one.
	_, refusal = mcpWithTokenRaw(t, fixture.server, readerToken, "spreadsheet.range.write", map[string]any{
		"workbook_id": fixture.shared.ID, "sheet_id": fixture.own.Sheets[0].ID, "idempotency_key": "mixed", "cells": []any{map[string]any{"row": 1, "column": 1, "value": 1}},
	})
	if !strings.Contains(refusal, "insufficient scope") {
		t.Fatalf("mixed workbooks must not be widened: %q", refusal)
	}
}

// mcpWithTokenRaw is mcpWithToken that also returns refusals for structured
// results, keeping the test bodies short.
func mcpWithTokenRaw(t *testing.T, server *httptest.Server, token, tool string, args map[string]any) (any, string) {
	t.Helper()
	_, result, refusal := mcpWithToken(t, server, token, tool, args)
	return result, refusal
}

// What a schema-validating gateway forwards is what tools/list declares. The
// write tools used to declare only sheet_id and idempotency_key, so cells, row
// and column were dropped on the way and every write failed.
func TestMCPWriteToolsDeclareEveryArgumentTheyRead(t *testing.T) {
	t.Parallel()
	want := map[string][]string{
		"spreadsheet.range.write":    {"sheet_id", "cells", "base_version", "idempotency_key", "client_id"},
		"spreadsheet.range.paste":    {"sheet_id", "cells", "base_version", "idempotency_key", "client_id"},
		"spreadsheet.range.fill":     {"sheet_id", "cells", "base_version", "idempotency_key", "client_id"},
		"spreadsheet.formula.set":    {"sheet_id", "row", "column", "formula", "base_version", "idempotency_key"},
		"spreadsheet.range.sort":     {"sheet_id", "range", "keys", "header_rows", "case_sensitive", "literal_order", "base_version", "idempotency_key", "client_id"},
		"spreadsheet.range.merge":    {"sheet_id", "range", "base_version", "idempotency_key", "client_id"},
		"spreadsheet.sheet.update":   {"sheet_id", "name", "position", "color", "hidden"},
		"spreadsheet.layout.apply":   {"sheet_id", "action", "range", "slicer"},
		"spreadsheet.chart.create":   {"secondary_axis", "data_labels", "y_axis_min", "y_axis_max"},
		"spreadsheet.import.execute": {"file_name", "data_base64", "idempotency_key", "workspace_id"},
	}
	for name, arguments := range want {
		definition, found := findMCPTool(name)
		if !found {
			t.Fatalf("%s is missing", name)
		}
		properties, _ := definition.InputSchema["properties"].(map[string]any)
		for _, argument := range arguments {
			if _, declared := properties[argument]; !declared {
				t.Errorf("%s does not declare %s", name, argument)
			}
		}
	}
	write, _ := findMCPTool("spreadsheet.range.write")
	cells := write.InputSchema["properties"].(map[string]any)["cells"].(map[string]any)
	items := cells["items"].(map[string]any)["properties"].(map[string]any)
	for _, argument := range []string{"row", "column", "value", "formula", "style"} {
		if _, declared := items[argument]; !declared {
			t.Errorf("cells items do not declare %s", argument)
		}
	}
	chart, _ := findMCPTool("spreadsheet.chart.create")
	types := chart.InputSchema["properties"].(map[string]any)["type"].(map[string]any)["enum"].([]string)
	if !strings.Contains(strings.Join(types, ","), "waterfall") {
		t.Errorf("chart types the server accepts must be in the enum: %v", types)
	}
}

// The shapes gateways actually send. Each one used to fail although every value
// was there.
func TestMCPAcceptsArgumentsTheWayGatewaysReencodeThem(t *testing.T) {
	t.Parallel()
	fixture := newGatewayFixture(t)
	sheet := fixture.own.Sheets[0].ID
	cases := []struct {
		name, params, address, want string
		headers                     map[string]string
	}{
		{name: "arguments as a JSON string", address: "B1", want: "1",
			params: `{"name":"spreadsheet.range.write","arguments":` + quoteJSON(`{"sheet_id":"`+sheet+`","idempotency_key":"s1","cells":[{"row":1,"column":2,"value":1}]}`) + `}`},
		{name: "cells as a JSON string and numbers as strings", address: "B2", want: "2",
			params: `{"name":"spreadsheet.range.write","arguments":{"sheet_id":"` + sheet + `","idempotency_key":"s2","cells":` + quoteJSON(`[{"row":"2","column":"2","value":2}]`) + `}}`},
		{name: "camelCase keys", address: "B3", want: "3",
			params: `{"name":"spreadsheet.range.write","arguments":{"sheetId":"` + sheet + `","idempotencyKey":"s3","baseVersion":"0","cells":[{"row":3,"column":2,"value":3}]}}`},
		{name: "wrapped once more", address: "B4", want: "4",
			params: `{"name":"spreadsheet.range.write","arguments":{"arguments":{"sheet_id":"` + sheet + `","idempotency_key":"s4","cells":[{"row":4,"column":2,"value":4}]}}}`},
		{name: "formula.set with row and column as strings", address: "B5", want: "=2*3",
			params: `{"name":"spreadsheet.formula.set","arguments":{"sheet_id":"` + sheet + `","idempotency_key":"s5","row":"5","column":"2","formula":"=2*3"}}`},
		{name: "idempotency key moved to the HTTP header", address: "B6", want: "6", headers: map[string]string{"Idempotency-Key": "from-header"},
			params: `{"name":"spreadsheet.range.write","arguments":{"sheet_id":"` + sheet + `","cells":[{"row":6,"column":2,"value":6}]}}`},
		{name: "idempotency key dropped altogether", address: "B7", want: "7",
			params: `{"name":"spreadsheet.range.write","arguments":{"sheet_id":"` + sheet + `","cells":[{"row":7,"column":2,"value":7}]}}`},
	}
	for _, testCase := range cases {
		if _, refusal := rawToolCall(t, fixture.server, fullToken, testCase.params, testCase.headers); refusal != "" {
			t.Fatalf("%s: %s", testCase.name, refusal)
		}
		if got := cellValue(t, fixture.repository, sheet, testCase.address); got != testCase.want {
			t.Fatalf("%s: %s=%s, want %s", testCase.name, testCase.address, got, testCase.want)
		}
	}
	// A key that did arrive still deduplicates: the same key twice writes once.
	params := `{"name":"spreadsheet.range.write","arguments":{"sheet_id":"` + sheet + `","cells":[{"row":8,"column":2,"value":8}]}}`
	first, _ := rawToolCall(t, fixture.server, fullToken, params, map[string]string{"Idempotency-Key": "same"})
	second, _ := rawToolCall(t, fixture.server, fullToken, params, map[string]string{"Idempotency-Key": "same"})
	if first["operation_id"] != second["operation_id"] || second["duplicate"] != true {
		t.Fatalf("the header key must deduplicate: %v / %v", first, second)
	}
	// A text value that looks like a number stays text: value has no schema
	// type, so nothing converts it.
	params = `{"name":"spreadsheet.range.write","arguments":{"sheet_id":"` + sheet + `","idempotency_key":"s9","cells":[{"row":9,"column":2,"value":"0012"}]}}`
	if _, refusal := rawToolCall(t, fixture.server, fullToken, params, nil); refusal != "" {
		t.Fatal(refusal)
	}
	if got := cellValue(t, fixture.repository, sheet, "B9"); got != `"0012"` {
		t.Fatalf("B9=%s, text must stay text", got)
	}
	if _, refusal := rawToolCall(t, fixture.server, fullToken, `{"name":"spreadsheet.range.write","arguments":"not json"}`, nil); !strings.HasPrefix(refusal, "protocol:") {
		t.Fatalf("arguments that are not an object are a protocol error: %q", refusal)
	}
}

func quoteJSON(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}
