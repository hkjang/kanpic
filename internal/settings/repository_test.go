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
