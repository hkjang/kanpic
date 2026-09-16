package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kanpic/internal/workbook"
)

func protocolServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(New(workbook.NewMemoryRepository(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	return server
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  map[string]any  `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func postMCP(t *testing.T, server *httptest.Server, body string, headers map[string]string) (*http.Response, rpcEnvelope) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var envelope rpcEnvelope
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("response is not JSON-RPC: %s", raw)
		}
	}
	return response, envelope
}

// A client negotiates the version it speaks; the server answers in the same
// version when it knows it and in its newest one otherwise.
func TestMCPInitializeNegotiatesProtocolVersion(t *testing.T) {
	t.Parallel()
	server := protocolServer(t)
	_, older := postMCP(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, nil)
	if older.Result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("a known version is echoed, got %v", older.Result["protocolVersion"])
	}
	_, unknown := postMCP(t, server, `{"jsonrpc":"2.0","id":"abc","method":"initialize","params":{"protocolVersion":"1999-01-01"}}`, nil)
	if unknown.Result["protocolVersion"] != mcpLatestProtocolVersion || string(unknown.ID) != `"abc"` {
		t.Fatalf("an unknown version gets the newest one and the id comes back verbatim: %v %s", unknown.Result["protocolVersion"], unknown.ID)
	}
	if _, ok := unknown.Result["instructions"].(string); !ok {
		t.Fatal("initialize carries instructions")
	}
}

// ping and every notification must be answered without an error, or clients
// that send them on a schedule treat the server as broken.
func TestMCPPingAndNotificationsAreAccepted(t *testing.T) {
	t.Parallel()
	server := protocolServer(t)
	response, ping := postMCP(t, server, `{"jsonrpc":"2.0","id":7,"method":"ping"}`, nil)
	if response.StatusCode != http.StatusOK || ping.Error != nil || ping.Result == nil || len(ping.Result) != 0 {
		t.Fatalf("ping answers with an empty result: %d %+v", response.StatusCode, ping)
	}
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`,
		`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}`,
		`{"jsonrpc":"2.0","method":"something/unknown"}`,
	} {
		response, _ := postMCP(t, server, body, nil)
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: notifications get 202 and no body, got %d", body, response.StatusCode)
		}
	}
	_, unknown := postMCP(t, server, `{"jsonrpc":"2.0","id":8,"method":"resources/list"}`, nil)
	if unknown.Error == nil || unknown.Error.Code != -32601 {
		t.Fatalf("an unknown request method is -32601: %+v", unknown.Error)
	}
}

// Malformed traffic gets a JSON-RPC error a client can show, never a bare 400
// page or a dropped connection.
func TestMCPMalformedRequestsGetJSONRPCErrors(t *testing.T) {
	t.Parallel()
	server := protocolServer(t)
	cases := []struct {
		name, body string
		headers    map[string]string
		status     int
		code       int
	}{
		{"parse error", `{"jsonrpc":"2.0",`, nil, http.StatusBadRequest, -32700},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, nil, http.StatusBadRequest, -32600},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, nil, http.StatusBadRequest, -32600},
		{"no method", `{"jsonrpc":"2.0","id":1}`, nil, http.StatusBadRequest, -32600},
		{"unknown protocol header", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"MCP-Protocol-Version": "1999-01-01"}, http.StatusBadRequest, -32600},
		{"unknown tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spreadsheet.nothing","arguments":{}}}`, nil, http.StatusOK, -32602},
		{"tool without name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":{}}}`, nil, http.StatusOK, -32602},
		{"foreign origin", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden, -32000},
	}
	for _, testCase := range cases {
		response, envelope := postMCP(t, server, testCase.body, testCase.headers)
		if response.StatusCode != testCase.status || envelope.Error == nil || envelope.Error.Code != testCase.code || envelope.JSONRPC != "2.0" {
			t.Errorf("%s: want %d/%d, got %d/%+v", testCase.name, testCase.status, testCase.code, response.StatusCode, envelope.Error)
		}
	}
	// Extra top-level fields and the server's own origin are fine.
	response, ok := postMCP(t, server, `{"jsonrpc":"2.0","id":1,"method":"ping","extra":true}`, map[string]string{"Origin": server.URL, "MCP-Protocol-Version": "2025-06-18"})
	if response.StatusCode != http.StatusOK || ok.Error != nil {
		t.Fatalf("own origin and unknown fields must pass: %d %+v", response.StatusCode, ok.Error)
	}
}

// structuredContent is an object in the schema clients validate against, so a
// list result is wrapped rather than sent bare; the text block keeps the raw
// JSON the model reads.
func TestMCPListResultsAreObjectsInStructuredContent(t *testing.T) {
	t.Parallel()
	server := protocolServer(t)
	_, created := postMCP(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spreadsheet.workbook.create","arguments":{"title":"목록"}}}`, nil)
	if created.Error != nil || created.Result["structuredContent"].(map[string]any)["title"] != "목록" {
		t.Fatalf("object results stay objects: %+v", created)
	}
	_, listed := postMCP(t, server, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"spreadsheet.workbook.list","arguments":{}}}`, nil)
	structured, ok := listed.Result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent must be an object, got %T", listed.Result["structuredContent"])
	}
	items, ok := structured["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("lists are wrapped as items: %v", structured)
	}
	text := listed.Result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "[") {
		t.Fatalf("the text block keeps the raw list: %s", text)
	}
	_, version := postMCP(t, server, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"platform.version.get","arguments":{}}}`, nil)
	if _, ok := version.Result["structuredContent"].(map[string]any); !ok {
		t.Fatalf("version info is an object: %+v", version.Result)
	}
}

// Tools whose service is not wired refuse with a reason instead of crashing.
func TestMCPToolsWithoutBackingServiceRefuse(t *testing.T) {
	t.Parallel()
	server := protocolServer(t)
	for _, tool := range []string{"platform.auth.config", "profile.api_key.list", "profile.preferences.get", "admin.settings.list", "admin.logs.list"} {
		response, envelope := postMCP(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":{}}}`, nil)
		if response.StatusCode != http.StatusOK || envelope.Error != nil || envelope.Result["isError"] != true {
			t.Fatalf("%s: must be a tool failure, got %d %+v", tool, response.StatusCode, envelope)
		}
		if !strings.Contains(envelope.Result["content"].([]any)[0].(map[string]any)["text"].(string), "not configured") {
			t.Fatalf("%s: the reason names the missing service: %+v", tool, envelope.Result)
		}
	}
}

// GET and DELETE exist in the transport for streams and sessions this server
// does not offer; they are refused instead of falling through to the SPA.
func TestMCPOnlyAcceptsPOST(t *testing.T) {
	t.Parallel()
	server := protocolServer(t)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		request, _ := http.NewRequest(method, server.URL+"/mcp", nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != "POST" || !strings.Contains(string(body), `"jsonrpc"`) {
			t.Fatalf("%s /mcp: want 405 with Allow: POST and a JSON-RPC body, got %d %q %s", method, response.StatusCode, response.Header.Get("Allow"), body)
		}
	}
}

// A result that JSON cannot carry becomes a tool failure with the reason, not
// a 200 with half a body.
func TestMCPToolPayloadRejectsUnserialisableResults(t *testing.T) {
	t.Parallel()
	if _, _, err := mcpToolPayload(map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("a channel cannot be JSON")
	}
	text, structured, err := mcpToolPayload(nil)
	if err != nil || structured != nil || text != "null" {
		t.Fatalf("nil is text null and no structuredContent: %q %v %v", text, structured, err)
	}
	_, structured, _ = mcpToolPayload(42)
	if structured["value"] != float64(42) {
		t.Fatalf("scalars are wrapped as value: %v", structured)
	}
}
