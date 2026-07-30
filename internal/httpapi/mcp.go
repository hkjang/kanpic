package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kanpic/internal/apikey"
	"kanpic/internal/settings"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

var mcpTools = []mcpTool{
	tool("platform.version.get", "kanpic 서버 빌드 버전을 조회합니다.", "", nil),
	tool("platform.auth.config", "OIDC 로그인 활성화 상태를 조회합니다.", "", nil),
	tool("spreadsheet.workbook.list", "접근 가능한 워크북을 조회합니다.", "workbook.read", props("workspace_id", "string")),
	tool("spreadsheet.workbook.get", "워크북 메타데이터와 시트를 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.workbook.create", "워크북과 첫 시트를 생성합니다.", "workbook.write", requiredProps("title", "string")),
	tool("spreadsheet.workbook.update", "워크북 이름 또는 즐겨찾기를 변경합니다.", "workbook.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.workbook.delete", "워크북을 삭제합니다.", "workbook.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.sheet.list", "워크북의 시트를 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.sheet.create", "워크북에 시트를 추가합니다.", "workbook.write", requiredProps2("workbook_id", "string", "name", "string")),
	tool("spreadsheet.sheet.update", "시트 이름, 순서, 색상, 숨김 상태를 변경합니다.", "workbook.write", requiredProps("sheet_id", "string")),
	tool("spreadsheet.sheet.delete", "시트를 삭제합니다.", "workbook.write", requiredProps("sheet_id", "string")),
	tool("spreadsheet.range.read", "A1 범위의 비어 있지 않은 셀을 조회합니다.", "range.read", requiredProps2("sheet_id", "string", "range", "string")),
	tool("spreadsheet.range.write", "최대 1천 셀을 멱등 일괄 변경합니다.", "range.write", requiredProps2("sheet_id", "string", "idempotency_key", "string")),
	tool("spreadsheet.formula.set", "셀 수식을 멱등 설정합니다.", "formula.write", requiredProps2("sheet_id", "string", "idempotency_key", "string")),
	tool("spreadsheet.formula.evaluate", "MVP 수식과 제공된 A1 셀 값을 서버에서 계산합니다.", "formula.read", requiredProps("formula", "string")),
	tool("spreadsheet.version.create", "현재 워크북 스냅샷 버전을 생성합니다.", "version.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.version.list", "워크북 버전 목록을 조회합니다.", "version.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.version.restore", "선택한 버전을 최신 버전으로 복원합니다.", "version.write", requiredProps("version_id", "string")),
	tool("profile.preferences.get", "현재 사용자의 개인 설정을 조회합니다.", "profile.read", nil),
	tool("profile.preferences.update", "현재 사용자의 개인 설정을 저장합니다.", "profile.write", requiredProps("values", "object")),
	tool("profile.api_key.list", "현재 사용자의 API 키 메타데이터를 조회합니다.", "api_keys.read", nil),
	tool("profile.api_key.create", "새 API 키를 만들고 원문을 한 번 반환합니다.", "api_keys.write", requiredProps2("name", "string", "scopes", "array")),
	tool("profile.api_key.update", "API 키 이름, scope, 만료를 변경합니다.", "api_keys.write", requiredProps("key_id", "string")),
	tool("profile.api_key.revoke", "API 키를 폐기합니다.", "api_keys.write", requiredProps("key_id", "string")),
	tool("profile.api_key.rotate", "기존 키를 폐기하고 같은 정책의 새 키를 발급합니다.", "api_keys.write", requiredProps("key_id", "string")),
	tool("admin.settings.list", "전체 시스템 설정을 조회합니다.", "admin.*", nil),
	tool("admin.settings.upsert", "시스템 설정을 추가하거나 변경하고 버전을 생성합니다.", "admin.*", requiredProps2("key", "string", "value_type", "string")),
	tool("admin.settings.delete", "시스템 설정을 삭제하고 버전을 생성합니다.", "admin.*", requiredProps("key", "string")),
	tool("admin.settings.validate", "현재 시스템 설정 전체를 검증합니다.", "admin.*", nil),
	tool("admin.settings.test", "PostgreSQL과 활성화된 OIDC 연결을 시험합니다.", "admin.*", nil),
	tool("admin.settings.version.list", "설정 변경 버전 목록을 조회합니다.", "admin.*", nil),
	tool("admin.settings.version.restore", "설정 스냅샷을 복원합니다.", "admin.*", requiredProps("revision", "number")),
	tool("admin.logs.list", "서버 구조화 로그를 검색합니다.", "admin.*", props("query", "string")),
	tool("admin.logs.purge", "기준 시각 이전 서버 로그를 삭제합니다.", "admin.*", requiredProps("before", "string")),
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	var request mcpRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.JSONRPC != "2.0" {
		s.writeMCPError(w, request.ID, -32600, "JSON-RPC 2.0이 필요합니다.")
		return
	}
	switch request.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "kanpic", "version": s.build.Version}}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": mcpTools}})
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			s.writeMCPError(w, request.ID, -32602, "도구 인수가 올바르지 않습니다.")
			return
		}
		result, err := s.callMCPTool(r, params.Name, params.Arguments)
		if err != nil {
			content, _ := json.Marshal(map[string]any{"error": err.Error()})
			writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": string(content)}}}})
			return
		}
		content, _ := json.Marshal(result)
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": string(content)}}, "structuredContent": result}})
	default:
		s.writeMCPError(w, request.ID, -32601, "지원하지 않는 MCP 메서드입니다.")
	}
}

func (s *Server) callMCPTool(r *http.Request, name string, args map[string]any) (any, error) {
	definition, found := findMCPTool(name)
	if !found {
		return nil, errors.New("unknown tool: " + name)
	}
	if principal, ok := apiPrincipal(r); ok && !principal.Allows(definition.Meta["required_scope"].(string)) {
		return nil, errors.New("insufficient scope: " + definition.Meta["required_scope"].(string))
	}
	ctx, actor := r.Context(), actorID(r)
	switch name {
	case "platform.version.get":
		return s.build, nil
	case "platform.auth.config":
		config, err := s.auth.Config(ctx)
		return map[string]any{"oidc_enabled": config.Enabled, "issuer_url": config.IssuerURL, "client_id": config.ClientID}, err
	case "spreadsheet.workbook.list":
		return s.repository.ListWorkbooks(ctx, stringArg(args, "workspace_id"))
	case "spreadsheet.workbook.get":
		return s.repository.GetWorkbook(ctx, stringArg(args, "workbook_id"))
	case "spreadsheet.workbook.create":
		var input workbook.CreateWorkbookInput
		decodeMCP(args, &input)
		input.OwnerID = actor
		return s.repository.CreateWorkbook(ctx, input)
	case "spreadsheet.workbook.update":
		var input workbook.UpdateWorkbookInput
		decodeMCP(args, &input)
		return s.repository.UpdateWorkbook(ctx, stringArg(args, "workbook_id"), input)
	case "spreadsheet.workbook.delete":
		return okResult(s.repository.DeleteWorkbook(ctx, stringArg(args, "workbook_id")))
	case "spreadsheet.sheet.list":
		wb, err := s.repository.GetWorkbook(ctx, stringArg(args, "workbook_id"))
		return wb.Sheets, err
	case "spreadsheet.sheet.create":
		var input workbook.CreateSheetInput
		decodeMCP(args, &input)
		return s.repository.CreateSheet(ctx, stringArg(args, "workbook_id"), input)
	case "spreadsheet.sheet.update":
		var input workbook.UpdateSheetInput
		decodeMCP(args, &input)
		return s.repository.UpdateSheet(ctx, stringArg(args, "sheet_id"), input)
	case "spreadsheet.sheet.delete":
		return okResult(s.repository.DeleteSheet(ctx, stringArg(args, "sheet_id")))
	case "spreadsheet.range.read":
		selected, err := cellrange.Parse(stringArg(args, "range"))
		if err != nil {
			return nil, err
		}
		return s.repository.ReadRange(ctx, stringArg(args, "sheet_id"), selected)
	case "spreadsheet.range.write":
		var input struct {
			SheetID        string               `json:"sheet_id"`
			BaseVersion    int64                `json:"base_version"`
			IdempotencyKey string               `json:"idempotency_key"`
			ClientID       string               `json:"client_id"`
			Cells          []workbook.CellInput `json:"cells"`
		}
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: input.SheetID, ActorID: actor, BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, ClientID: input.ClientID, Cells: input.Cells})
	case "spreadsheet.formula.set":
		var input struct {
			SheetID        string `json:"sheet_id"`
			Row            int    `json:"row"`
			Column         int    `json:"column"`
			Formula        string `json:"formula"`
			BaseVersion    int64  `json:"base_version"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: input.SheetID, ActorID: actor, BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, Cells: []workbook.CellInput{{Row: input.Row, Column: input.Column, Formula: input.Formula}}})
	case "spreadsheet.formula.evaluate":
		cells, _ := args["cells"].(map[string]any)
		return s.formula.Evaluate(stringArg(args, "formula"), cells), nil
	case "spreadsheet.version.create":
		return s.repository.CreateVersion(ctx, stringArg(args, "workbook_id"), stringArg(args, "name"), actor)
	case "spreadsheet.version.list":
		return s.repository.ListVersions(ctx, stringArg(args, "workbook_id"))
	case "spreadsheet.version.restore":
		return s.repository.RestoreVersion(ctx, stringArg(args, "version_id"), actor)
	case "profile.preferences.get":
		return s.settings.GetPreferences(ctx, actor)
	case "profile.preferences.update":
		values, _ := args["values"].(map[string]any)
		return s.settings.PutPreferences(ctx, actor, values)
	case "profile.api_key.list":
		return s.keys.List(ctx, actor, false)
	case "profile.api_key.create":
		var input apikey.CreateInput
		decodeMCP(args, &input)
		return s.keys.Create(ctx, actor, input)
	case "profile.api_key.update":
		var input apikey.UpdateInput
		decodeMCP(args, &input)
		return s.keys.Update(ctx, stringArg(args, "key_id"), actor, input, false)
	case "profile.api_key.revoke":
		return okResult(s.keys.Revoke(ctx, stringArg(args, "key_id"), actor, false))
	case "profile.api_key.rotate":
		return s.keys.Rotate(ctx, stringArg(args, "key_id"), actor, false)
	case "admin.settings.list":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.List(ctx, false)
	case "admin.settings.upsert":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		var input settings.Setting
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.settings.Put(ctx, input, actor)
	case "admin.settings.delete":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return okResult(s.settings.Delete(ctx, stringArg(args, "key"), actor))
	case "admin.settings.validate":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.Validate(ctx)
	case "admin.settings.test":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.Test(ctx)
	case "admin.settings.version.list":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.Versions(ctx)
	case "admin.settings.version.restore":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		revision, err := numberArg(args, "revision")
		if err != nil {
			return nil, err
		}
		return okResult(s.settings.Restore(ctx, revision, actor))
	case "admin.logs.list":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		limit, _ := numberArg(args, "limit")
		return s.logs.List(ctx, stringArg(args, "level"), stringArg(args, "query"), int(limit))
	case "admin.logs.purge":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		before, err := time.Parse(time.RFC3339, stringArg(args, "before"))
		if err != nil {
			return nil, err
		}
		count, err := s.logs.PurgeBefore(ctx, before)
		return map[string]any{"deleted": count}, err
	}
	return nil, errors.New("tool is not implemented")
}

func (s *Server) requireMCPAdmin(r *http.Request) error {
	if principal, ok := apiPrincipal(r); ok {
		if principal.Allows("admin.*") {
			return nil
		}
		return errors.New("admin scope is required")
	}
	if user, ok := sessionUser(r); ok && s.auth.IsAdmin(r.Context(), user) {
		return nil
	}
	if actorID(r) == "local-user" {
		return nil
	}
	return errors.New("administrator permission is required")
}

func (s *Server) writeMCPError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func tool(name, description, scope string, schema map[string]any) mcpTool {
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return mcpTool{Name: name, Description: description, InputSchema: schema, Meta: map[string]any{"required_scope": scope}}
}
func props(name, kind string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{name: map[string]any{"type": kind}}}
}
func requiredProps(name, kind string) map[string]any {
	schema := props(name, kind)
	schema["required"] = []string{name}
	return schema
}
func requiredProps2(a, ak, b, bk string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{a: map[string]any{"type": ak}, b: map[string]any{"type": bk}}, "required": []string{a, b}}
}
func findMCPTool(name string) (mcpTool, bool) {
	for _, item := range mcpTools {
		if item.Name == name {
			return item, true
		}
	}
	return mcpTool{}, false
}
func decodeMCP(args map[string]any, target any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func stringArg(args map[string]any, key string) string { value, _ := args[key].(string); return value }
func numberArg(args map[string]any, key string) (int64, error) {
	switch value := args[key].(type) {
	case float64:
		return int64(value), nil
	case json.Number:
		return value.Int64()
	case string:
		return strconv.ParseInt(value, 10, 64)
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}
func okResult(err error) (any, error) { return map[string]any{"ok": err == nil}, err }

var _ = strings.TrimSpace
