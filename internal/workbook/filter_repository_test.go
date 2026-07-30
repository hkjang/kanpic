package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestMemoryFilterViewsAreActorScopedActiveAndCRUDComplete(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "filters"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	first, err := repository.CreateFilterView(ctx, sheetID, "alice", CreateFilterViewInput{IdempotencyKey: "filter-first", Name: "서울", Range: "A1:B10", HeaderRows: 1, Active: true, Criteria: []FilterCriterion{{Column: 1, Operator: "equals", Value: json.RawMessage(`"서울"`)}}})
	if err != nil || !first.Active {
		t.Fatalf("create first: %#v, %v", first, err)
	}
	duplicate, err := repository.CreateFilterView(ctx, sheetID, "alice", CreateFilterViewInput{IdempotencyKey: "filter-first", Name: "", Range: "invalid"})
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("idempotent create: %#v, %v", duplicate, err)
	}
	second, err := repository.CreateFilterView(ctx, sheetID, "alice", CreateFilterViewInput{IdempotencyKey: "filter-second", Name: "10 이상", Range: "A1:B10", HeaderRows: 1, Active: true, Criteria: []FilterCriterion{{Column: 2, Operator: "greater_or_equal", Value: json.RawMessage(`10`)}}})
	if err != nil || !second.Active {
		t.Fatalf("create second: %#v, %v", second, err)
	}
	items, err := repository.ListFilterViews(ctx, sheetID, "alice")
	if err != nil || len(items) != 2 || items[0].ID != second.ID || !items[0].Active || items[1].Active {
		t.Fatalf("alice filters: %#v, %v", items, err)
	}
	if bob, err := repository.ListFilterViews(ctx, sheetID, "bob"); err != nil || len(bob) != 0 {
		t.Fatalf("bob saw alice filters: %#v, %v", bob, err)
	}
	if _, err := repository.GetFilterView(ctx, first.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user read error = %v", err)
	}
	name, active := "부산만", true
	criteria := []FilterCriterion{{Column: 1, Operator: "values", Values: []json.RawMessage{json.RawMessage(`"부산"`)}}}
	updated, err := repository.UpdateFilterView(ctx, first.ID, "alice", UpdateFilterViewInput{Name: &name, Criteria: &criteria, Active: &active})
	if err != nil || updated.Name != name || !updated.Active || len(updated.Criteria) != 1 {
		t.Fatalf("updated filter: %#v, %v", updated, err)
	}
	second, err = repository.GetFilterView(ctx, second.ID, "alice")
	if err != nil || second.Active {
		t.Fatalf("previous active filter: %#v, %v", second, err)
	}
	if _, err := repository.CreateFilterView(ctx, sheetID, "alice", CreateFilterViewInput{IdempotencyKey: "filter-duplicate-name", Name: "부산만", Range: "A1:B10"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name error = %v", err)
	}
	if err := repository.DeleteFilterView(ctx, first.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetFilterView(ctx, first.ID, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted filter error = %v", err)
	}
}
