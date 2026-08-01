package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestMemoryConditionalFormatsEvaluateAndManageRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "conditional", OwnerID: "alice"})
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`10`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`20`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`20`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`30`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(key, kind, operator string, priority int, style string) ConditionalFormat {
		rule, createErr := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{IdempotencyKey: key, Name: key, Range: "A1:A5", RuleType: kind, Operator: operator, Value: json.RawMessage(`15`), Style: json.RawMessage(style), MinColor: "#000000", MaxColor: "#ffffff", BarColor: "#2563eb", Priority: priority, StopIfTrue: kind == "value" && operator == "greater_than"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return rule
	}
	scale := create("scale", "color_scale", "", 1, "")
	duplicate := create("duplicate", "duplicate", "duplicate", 2, `{"bold":true}`)
	threshold := create("threshold", "value", "greater_than", 3, `{"color":"#dc2626"}`)
	bar := create("bar", "data_bar", "", 4, "")
	blank := create("blank", "value", "is_blank", 5, `{"italic":true}`)
	if scale.WorkbookVersion != seed.ServerVersion+1 || threshold.Revision != 1 {
		t.Fatalf("created rules have unexpected versions: %#v %#v", scale, threshold)
	}
	idempotent, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{IdempotencyKey: "scale", Name: "ignored", Range: "B1", RuleType: "data_bar"})
	if err != nil || idempotent.ID != scale.ID || idempotent.Name != scale.Name {
		t.Fatalf("idempotent create=%#v err=%v", idempotent, err)
	}
	selected, _ := cellrange.Parse("A1:A5")
	evaluation, err := repository.EvaluateConditionalFormats(ctx, sheet.ID, selected)
	if err != nil || len(evaluation.Items) != 5 {
		t.Fatalf("evaluation=%#v err=%v", evaluation, err)
	}
	byRow := map[int]ConditionalFormatCell{}
	for _, item := range evaluation.Items {
		byRow[item.Row] = item
	}
	assertConditionalStyle(t, byRow[1].Style, "background", "#000000")
	if byRow[1].DataBar == nil || byRow[1].DataBar.Ratio != 0 {
		t.Fatalf("row 1 data bar=%#v", byRow[1].DataBar)
	}
	assertConditionalStyle(t, byRow[2].Style, "background", "#808080")
	assertConditionalStyle(t, byRow[2].Style, "bold", true)
	assertConditionalStyle(t, byRow[2].Style, "color", "#dc2626")
	if byRow[2].DataBar != nil || len(byRow[2].MatchedRuleIDs) != 3 {
		t.Fatalf("stop-if-true should suppress later data bar: %#v", byRow[2])
	}
	assertConditionalStyle(t, byRow[5].Style, "italic", true)
	if len(byRow[5].MatchedRuleIDs) != 1 || byRow[5].MatchedRuleIDs[0] != blank.ID {
		t.Fatalf("blank rule=%#v", byRow[5])
	}
	name := "renamed"
	updated, err := repository.UpdateConditionalFormat(ctx, duplicate.ID, "bob", UpdateConditionalFormatInput{Name: &name, ExpectedRevision: &duplicate.Revision})
	if err != nil || updated.Name != name || updated.Revision != 2 || updated.UpdatedBy != "bob" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := repository.UpdateConditionalFormat(ctx, duplicate.ID, "bob", UpdateConditionalFormatInput{Name: &name, ExpectedRevision: &duplicate.Revision}); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale update error=%v", err)
	}
	wrong := int64(2)
	if err := repository.DeleteConditionalFormat(ctx, bar.ID, "alice", &wrong); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale delete error=%v", err)
	}
	if err := repository.DeleteConditionalFormat(ctx, bar.ID, "alice", &bar.Revision); err != nil {
		t.Fatal(err)
	}
	items, _ := repository.ListConditionalFormats(ctx, sheet.ID)
	if len(items) != 4 || items[0].ID != scale.ID {
		t.Fatalf("rules=%#v", items)
	}
}

func TestConditionalFormatValidation(t *testing.T) {
	t.Parallel()
	base := ConditionalFormat{SheetID: "sheet", Name: "rule", Range: "A1:A2", RuleType: "value", Operator: "greater_than", Value: json.RawMessage(`1`)}
	if normalized, selected, err := NormalizeConditionalFormat(base); err != nil || normalized.Priority != 1 || selected.End.Row != 2 || len(normalized.Style) == 0 {
		t.Fatalf("normalized=%#v range=%#v err=%v", normalized, selected, err)
	}
	for name, mutate := range map[string]func(*ConditionalFormat){
		"large range":       func(item *ConditionalFormat) { item.Range = "A1:XFD1048576" },
		"bad operator":      func(item *ConditionalFormat) { item.Operator = "unknown" },
		"bad between":       func(item *ConditionalFormat) { item.Operator, item.Value2 = "between", json.RawMessage(`0`) },
		"bad style":         func(item *ConditionalFormat) { item.Style = json.RawMessage(`{"background":"red"}`) },
		"bad scale color":   func(item *ConditionalFormat) { item.RuleType, item.MinColor = "color_scale", "red" },
		"bad data bar":      func(item *ConditionalFormat) { item.RuleType, item.BarColor = "data_bar", "blue" },
		"unknown rule type": func(item *ConditionalFormat) { item.RuleType = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			item := base
			mutate(&item)
			if _, _, err := NormalizeConditionalFormat(item); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid input, got %v", err)
			}
		})
	}
}

func TestConditionalNotBetweenOnlyMatchesNumericValues(t *testing.T) {
	t.Parallel()
	rule, _, err := NormalizeConditionalFormat(ConditionalFormat{SheetID: "sheet", Range: "A1", RuleType: "value", Operator: "not_between", Value: json.RawMessage(`10`), Value2: json.RawMessage(`20`)})
	if err != nil {
		t.Fatal(err)
	}
	if conditionalValueMatches("not-a-number", rule) || !conditionalValueMatches(5.0, rule) || conditionalValueMatches(15.0, rule) {
		t.Fatal("not_between must reject nonnumeric cells and match only numbers outside the interval")
	}
}

func TestConditionalThreeColorScaleAndUniqueValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "three color", OwnerID: "alice"})
	sheet := book.Sheets[0]
	seeded, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "three-color-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`0`)}, {Row: 2, Column: 1, Value: json.RawMessage(`50`)}, {Row: 3, Column: 1, Value: json.RawMessage(`100`)}, {Row: 4, Column: 1, Value: json.RawMessage(`100`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{IdempotencyKey: "three-color", Range: "A1:A3", RuleType: "color_scale", MinColor: "#000000", MidColor: "#ff0000", MaxColor: "#ffffff", Priority: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{IdempotencyKey: "unique", Range: "A1:A4", RuleType: "duplicate", Operator: "unique", Style: json.RawMessage(`{"bold":true}`), Priority: 2}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A1:A4")
	evaluated, err := repository.EvaluateConditionalFormats(ctx, sheet.ID, selected)
	if err != nil || evaluated.WorkbookVersion != seeded.ServerVersion+2 || len(evaluated.Items) != 3 {
		t.Fatalf("three-color evaluation=%#v err=%v", evaluated, err)
	}
	byRow := map[int]ConditionalFormatCell{}
	for _, item := range evaluated.Items {
		byRow[item.Row] = item
	}
	assertConditionalStyle(t, byRow[1].Style, "background", "#000000")
	assertConditionalStyle(t, byRow[2].Style, "background", "#ff0000")
	assertConditionalStyle(t, byRow[3].Style, "background", "#ffffff")
	assertConditionalStyle(t, byRow[1].Style, "bold", true)
	if len(byRow[3].MatchedRuleIDs) != 1 || len(byRow[4].MatchedRuleIDs) != 0 {
		t.Fatalf("unique matching=%#v", byRow)
	}
}

func TestConditionalEvaluationLimit(t *testing.T) {
	t.Parallel()
	selected, _ := cellrange.Parse("A1:Z1000")
	if _, err := EvaluateConditionalFormats("sheet", 1, selected, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected evaluation limit error, got %v", err)
	}
}

func assertConditionalStyle(t *testing.T, raw json.RawMessage, key string, expected any) {
	t.Helper()
	var style map[string]any
	if err := json.Unmarshal(raw, &style); err != nil || style[key] != expected {
		t.Fatalf("style %s=%#v, want %#v; raw=%s err=%v", key, style[key], expected, raw, err)
	}
}
