package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kanpic/internal/auth"
	"kanpic/internal/workbook"
)

// fakeTokens stands in for Keycloak: it knows a few tokens and says which user
// each one belongs to.
type fakeTokens struct {
	tokens  map[string]auth.AccessToken
	enabled bool
}

func (f fakeTokens) VerifyAccessToken(_ context.Context, _, raw string) (auth.AccessToken, error) {
	if !f.enabled {
		return auth.AccessToken{}, auth.ErrTokenDisabled
	}
	if token, ok := f.tokens[raw]; ok {
		return token, nil
	}
	return auth.AccessToken{}, fmt.Errorf("%w: unknown token", auth.ErrTokenRejected)
}

func (f fakeTokens) ProtectedResource(_ context.Context, origin string) (auth.ProtectedResource, bool, error) {
	return auth.ProtectedResource{
		Resource: origin + "/mcp", AuthorizationServers: []string{"https://id.example/realms/company"},
		BearerMethodsSupported: []string{"header"}, MetadataURL: origin + "/.well-known/oauth-protected-resource/mcp",
	}, f.enabled, nil
}

// Tokens look like compact JWTs so the middleware tells them from API keys.
const (
	fullToken   = "eyJhbGciOiJSUzI1NiJ9.full.sig"
	narrowToken = "eyJhbGciOiJSUzI1NiJ9.narrow.sig"
	bogusToken  = "eyJhbGciOiJSUzI1NiJ9.bogus.sig"
)

func oauthServer(t *testing.T, enabled bool) *httptest.Server {
	t.Helper()
	expires := time.Now().Add(time.Hour)
	tokens := fakeTokens{enabled: enabled, tokens: map[string]auth.AccessToken{
		fullToken:   {User: auth.User{ID: "oidc-user", Email: "user@example.com", DisplayName: "홍길동", ExpiresAt: expires}, ClientID: "claude"},
		narrowToken: {User: auth.User{ID: "oidc-reader", DisplayName: "읽기만", ExpiresAt: expires}, Scopes: []string{"workbook.read"}, ClientID: "claude"},
	}}
	handler := NewPlatformWithServices(workbook.NewMemoryRepository(), nil, nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), WithAccessTokens(tokens))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func mcpWithToken(t *testing.T, server *httptest.Server, token, tool string, args map[string]any) (*http.Response, any, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response, nil, ""
	}
	var envelope struct {
		Result struct {
			IsError    bool `json:"isError"`
			Structured any  `json:"structuredContent"`
			Content    []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.IsError {
		return response, nil, envelope.Result.Content[0].Text
	}
	return response, envelope.Result.Structured, ""
}

// A Keycloak token stands for its user. A workbook made through it belongs to
// that user, exactly as if they had signed in with the browser.
func TestMCPAcceptsKeycloakAccessTokenAsTheUser(t *testing.T) {
	t.Parallel()
	server := oauthServer(t, true)

	_, created, refusal := mcpWithToken(t, server, fullToken, "spreadsheet.workbook.create", map[string]any{"title": "OAuth 로 만든 워크북"})
	if refusal != "" {
		t.Fatalf("token user must be able to create: %s", refusal)
	}
	if owner := created.(map[string]any)["owner_id"]; owner != "oidc-user" {
		t.Fatalf("the workbook must belong to the token's user, got %v", owner)
	}
	_, listed, refusal := mcpWithToken(t, server, fullToken, "spreadsheet.workbook.list", nil)
	if refusal != "" || listed == nil {
		t.Fatalf("token user must see their workbooks: %s %v", refusal, listed)
	}
}

// Scopes carried in the token narrow it like an API key, without the admin
// having to hand out mcp.use separately.
func TestMCPNarrowedTokenIsLimitedToItsScopes(t *testing.T) {
	t.Parallel()
	server := oauthServer(t, true)

	_, _, refusal := mcpWithToken(t, server, narrowToken, "spreadsheet.workbook.create", map[string]any{"title": "안 됨"})
	if !strings.Contains(refusal, "insufficient scope: workbook.write") {
		t.Fatalf("a workbook.read token must not create, got %q", refusal)
	}
	_, listed, refusal := mcpWithToken(t, server, narrowToken, "spreadsheet.workbook.list", nil)
	if refusal != "" || listed == nil {
		t.Fatalf("a workbook.read token lists workbooks: %s", refusal)
	}
	_, _, refusal = mcpWithToken(t, server, narrowToken, "admin.logs.list", nil)
	if !strings.Contains(refusal, "admin") {
		t.Fatalf("a narrowed token without admin.* is not an administrator, got %q", refusal)
	}
}

// A refused token gets the RFC 9728 pointer, so the client can start the OAuth
// flow with Keycloak instead of giving up.
func TestMCPRejectedTokenPointsAtResourceMetadata(t *testing.T) {
	t.Parallel()
	server := oauthServer(t, true)

	response, _, _ := mcpWithToken(t, server, bogusToken, "platform.version.get", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown token: %d", response.StatusCode)
	}
	challenge := response.Header.Get("WWW-Authenticate")
	wantMetadata := `resource_metadata="` + server.URL + `/.well-known/oauth-protected-resource/mcp"`
	if !strings.HasPrefix(challenge, "Bearer ") || !strings.Contains(challenge, wantMetadata) || !strings.Contains(challenge, `error="invalid_token"`) {
		t.Fatalf("challenge must name the resource document and the error: %q", challenge)
	}

	metadata, err := http.Get(server.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Body.Close()
	if metadata.StatusCode != http.StatusOK || metadata.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("resource document must be public: %d %q", metadata.StatusCode, metadata.Header.Get("Access-Control-Allow-Origin"))
	}
	var document auth.ProtectedResource
	if err := json.NewDecoder(metadata.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.Resource != server.URL+"/mcp" || len(document.AuthorizationServers) != 1 || document.AuthorizationServers[0] != "https://id.example/realms/company" {
		t.Fatalf("document must point at Keycloak for this resource: %#v", document)
	}
	root, err := http.Get(server.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	root.Body.Close()
	if root.StatusCode != http.StatusOK {
		t.Fatalf("the root well-known path serves the same document: %d", root.StatusCode)
	}
}

// Tokens are an MCP credential. REST keeps the session cookie and API keys, so
// the browser's rules do not change.
func TestOAuthTokenIsNotAcceptedOutsideMCP(t *testing.T) {
	t.Parallel()
	server := oauthServer(t, true)
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/workbooks", nil)
	request.Header.Set("Authorization", "Bearer "+fullToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("REST must not take an MCP token: %d", response.StatusCode)
	}
	if response.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("only /mcp advertises the OAuth flow: %q", response.Header.Get("WWW-Authenticate"))
	}
}

// With the switch off nothing changes: a token is just an unknown key and the
// resource document is not there.
func TestMCPOAuthDisabledKeepsKeyOnlyBehaviour(t *testing.T) {
	t.Parallel()
	server := oauthServer(t, false)
	response, _, _ := mcpWithToken(t, server, fullToken, "platform.version.get", nil)
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("disabled: %d %q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	metadata, err := http.Get(server.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Body.Close()
	_ = json.NewDecoder(metadata.Body).Decode(&body)
	if metadata.StatusCode != http.StatusNotFound || body.Error.Code != "not_found" {
		t.Fatalf("no document while disabled: %d %s", metadata.StatusCode, body.Error.Code)
	}
}
