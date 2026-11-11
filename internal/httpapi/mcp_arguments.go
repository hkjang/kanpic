package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"kanpic/pkg/identity"
)

// Tool arguments rarely arrive at /mcp straight from the model. A company MCP
// gateway in between re-encodes them, and each one has its habits: numbers
// travel as strings, an array as its JSON text, the whole arguments object as a
// string, snake_case keys turn into camelCase, or the object is wrapped once
// more under "arguments". Before this layer every one of those failed the same
// way for the person at the other end — "cells must contain 1 to 1000 entries",
// a formula written to row 0 — although the values were all there.
//
// The tool's inputSchema says what each argument is, so the server puts the
// arguments back into that shape before the handler reads them. Nothing here
// guesses beyond the schema: a property the schema does not type is left
// exactly as sent, which keeps a text value "0012" a text value.

// errMCPArguments is the one way decoding can fail: the arguments are not an
// object even after unwrapping a string.
var errMCPArguments = errors.New("params.arguments 는 JSON 객체여야 합니다")

// decodeMCPArguments reads params.arguments. Some gateways send the object as a
// JSON string, so a string holding an object is accepted as that object.
func decodeMCPArguments(raw json.RawMessage) (map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return map[string]any{}, nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, errMCPArguments
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return map[string]any{}, nil
		}
		raw = []byte(text)
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, errMCPArguments
	}
	return args, nil
}

// mcpArgumentWrappers are the keys gateways wrap arguments under.
var mcpArgumentWrappers = map[string]bool{"arguments": true, "input": true, "params": true, "parameters": true, "args": true}

// normalizeMCPArguments reshapes args to the tool's inputSchema and fills in an
// idempotency key the gateway did not pass on.
func normalizeMCPArguments(r *http.Request, definition mcpTool, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	schema := definition.InputSchema
	args = unwrapMCPArguments(schema, args)
	args = conformToSchema(schema, args)
	if requiresIdempotencyKey(schema) {
		if key, _ := args["idempotency_key"].(string); strings.TrimSpace(key) == "" {
			// A gateway that drops the key usually passes the HTTP header on, so
			// that is the caller's key. Without either, the call still runs once
			// under a key of its own: refusing it would fail every write that
			// came through such a gateway, and a fresh key is exactly what an
			// unkeyed request means.
			if header := strings.TrimSpace(r.Header.Get("Idempotency-Key")); header != "" {
				args["idempotency_key"] = header
			} else {
				args["idempotency_key"] = "mcp-" + identity.New()
			}
		}
	}
	return args
}

// unwrapMCPArguments takes the arguments out of a single wrapper key, but only
// when the schema does not itself have that key and nothing the schema needs is
// at the top level already.
func unwrapMCPArguments(schema map[string]any, args map[string]any) map[string]any {
	if len(args) != 1 {
		return args
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, value := range args {
		if !mcpArgumentWrappers[key] {
			return args
		}
		if _, declared := properties[key]; declared {
			return args
		}
		switch inner := value.(type) {
		case map[string]any:
			return inner
		case string:
			var decoded map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(inner)), &decoded); err == nil && decoded != nil {
				return decoded
			}
		}
	}
	return args
}

// conformToSchema renames camelCase keys to the schema's snake_case names and
// converts values whose schema type says what they must be. It recurses into
// object properties and into the items of arrays of objects, which is where a
// cell's row and column live.
func conformToSchema(schema map[string]any, args map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		return args
	}
	for name, definition := range properties {
		if _, present := args[name]; !present {
			for _, alias := range argumentAliases(name) {
				if value, found := args[alias]; found {
					args[name] = value
					delete(args, alias)
					break
				}
			}
		}
		value, present := args[name]
		if !present {
			continue
		}
		property, _ := definition.(map[string]any)
		args[name] = conformValue(property, value)
	}
	return args
}

func conformValue(property map[string]any, value any) any {
	kind, _ := property["type"].(string)
	switch kind {
	case "integer", "number":
		if text, ok := value.(string); ok {
			if number, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
				return number
			}
		}
	case "boolean":
		if text, ok := value.(string); ok {
			switch strings.ToLower(strings.TrimSpace(text)) {
			case "true":
				return true
			case "false":
				return false
			}
		}
	case "array":
		if text, ok := value.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "[") {
			var decoded []any
			if err := json.Unmarshal([]byte(text), &decoded); err == nil {
				value = decoded
			}
		}
		items, _ := property["items"].(map[string]any)
		if list, ok := value.([]any); ok && items != nil {
			for index, item := range list {
				list[index] = conformValue(items, item)
			}
			return list
		}
	case "object":
		if text, ok := value.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "{") {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(text), &decoded); err == nil {
				value = decoded
			}
		}
		if object, ok := value.(map[string]any); ok {
			return conformToSchema(property, object)
		}
	}
	return value
}

// argumentAliases lists the spellings a gateway may have turned a snake_case
// name into: sheetId, SheetId and sheet-id for sheet_id.
func argumentAliases(name string) []string {
	if !strings.Contains(name, "_") {
		return nil
	}
	parts := strings.Split(name, "_")
	var camel strings.Builder
	camel.WriteString(parts[0])
	for _, part := range parts[1:] {
		camel.WriteString(capitalize(part))
	}
	return []string{camel.String(), capitalize(camel.String()), strings.ReplaceAll(name, "_", "-")}
}

func capitalize(word string) string {
	if word == "" {
		return word
	}
	runes := []rune(word)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func requiresIdempotencyKey(schema map[string]any) bool {
	required, _ := schema["required"].([]string)
	for _, name := range required {
		if name == "idempotency_key" {
			return true
		}
	}
	return false
}
