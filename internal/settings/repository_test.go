package settings

import (
	"encoding/json"
	"testing"
)

func TestSchedulerPollSettingValidation(t *testing.T) {
	for _, value := range []string{"5", "15", "300"} {
		item := Setting{Key: "automation.scheduler_poll_seconds", Value: json.RawMessage(value), ValueType: "number"}
		if issue := validateValue(item); issue != "" {
			t.Fatalf("value %s issue=%q", value, issue)
		}
	}
	for _, value := range []string{"4", "301"} {
		item := Setting{Key: "automation.scheduler_poll_seconds", Value: json.RawMessage(value), ValueType: "number"}
		if issue := validateValue(item); issue == "" {
			t.Fatalf("value %s unexpectedly passed validation", value)
		}
	}
}

// mcp.owner_scopes widens what an agent may do on its user's own workbooks.
// Administration, keys, sharing and deleting must never be widened that way,
// whatever an administrator types into the list.
func TestMCPOwnerScopesOnlyAcceptWorkbookContentScopes(t *testing.T) {
	for _, item := range defaults {
		if item.Key == "mcp.owner_scopes" {
			if issue := validateValue(item); issue != "" {
				t.Fatalf("default is invalid: %s", issue)
			}
		}
	}
	accepted := Setting{Key: "mcp.owner_scopes", ValueType: "string_list", Value: json.RawMessage(`["range.write","format.*","automation.run","workbook.read"]`)}
	if issue := validateValue(accepted); issue != "" {
		t.Fatalf("content scopes rejected: %s", issue)
	}
	for _, scope := range []string{"admin.*", "*", "workbook.write", "api_keys.write", "profile.write", "ai.use", "mcp.use", "import.write", "automation.webhook.invoke"} {
		item := Setting{Key: "mcp.owner_scopes", ValueType: "string_list", Value: json.RawMessage(`["range.write","` + scope + `"]`)}
		if issue := validateValue(item); issue == "" {
			t.Fatalf("%s accepted as an owner scope", scope)
		}
	}
}
