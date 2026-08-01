package apikey

import (
	"encoding/json"
	"testing"
)

func TestUpdateInputDistinguishesExpirationOmissionAndClear(t *testing.T) {
	var omitted UpdateInput
	if err := json.Unmarshal([]byte(`{"name":"agent"}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.ExpiresAt != nil {
		t.Fatalf("omitted expiration should not be updated: %#v", omitted.ExpiresAt)
	}
	var cleared UpdateInput
	if err := json.Unmarshal([]byte(`{"expires_at":null}`), &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.ExpiresAt == nil || *cleared.ExpiresAt != nil {
		t.Fatalf("explicit null should clear expiration: %#v", cleared.ExpiresAt)
	}
	var set UpdateInput
	if err := json.Unmarshal([]byte(`{"expires_at":"2030-01-02T03:04:05Z"}`), &set); err != nil {
		t.Fatal(err)
	}
	if set.ExpiresAt == nil || *set.ExpiresAt == nil || (*set.ExpiresAt).Year() != 2030 {
		t.Fatalf("timestamp should set expiration: %#v", set.ExpiresAt)
	}
}
