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

// spreadsheet.write is the name a ticket or a Keycloak client scope uses for
// "may change cells"; it stands for kanpic's two cell scopes and nothing more.
func TestSpreadsheetScopeAliasesCoverCellReadsAndWrites(t *testing.T) {
	writer := NewPrincipal("u", []string{"spreadsheet.write"})
	for _, scope := range []string{"range.write", "formula.write"} {
		if !writer.Allows(scope) {
			t.Fatalf("spreadsheet.write must allow %s", scope)
		}
	}
	for _, scope := range []string{"format.write", "workbook.write", "range.read", "admin.*"} {
		if writer.Allows(scope) {
			t.Fatalf("spreadsheet.write must not allow %s", scope)
		}
	}
	reader := NewPrincipal("u", []string{"spreadsheet.read"})
	if !reader.Allows("range.read") || !reader.Allows("workbook.read") || reader.Allows("range.write") {
		t.Fatal("spreadsheet.read covers reads only")
	}
	if !NewPrincipal("u", []string{"spreadsheet.*"}).Allows("range.write") {
		t.Fatal("spreadsheet.* covers both aliases")
	}
}

// Widening a principal for one call must not change the one it came from.
func TestWithScopesCopies(t *testing.T) {
	original := NewPrincipal("u", []string{"workbook.read"})
	widened := original.WithScopes("range.write", " ", "format.write")
	if !widened.Allows("range.write") || !widened.Allows("format.write") || !widened.Allows("workbook.read") {
		t.Fatalf("widened=%v", widened.Scopes)
	}
	if original.Allows("range.write") || len(original.Scopes) != 1 {
		t.Fatalf("original changed: %v", original.Scopes)
	}
	if widened.UserID != "u" {
		t.Fatal("user must carry over")
	}
}
