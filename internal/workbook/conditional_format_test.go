package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
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

// A custom formula rule is written for the top-left cell of its range and
// moves with each cell, so one rule can highlight whole rows from a column the
// rule does not even cover.
func TestMemoryConditionalCustomFormulaHighlightsRowsByAnotherColumn(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "맞춤 수식", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "custom-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"연필"`)}, {Row: 1, Column: 3, Value: json.RawMessage(`"완료"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"공책"`)}, {Row: 2, Column: 3, Value: json.RawMessage(`"진행"`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"지우개"`)}, {Row: 3, Column: 3, Value: json.RawMessage(`"완료"`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateConditionalFormat(ctx, sheet, "alice", CreateConditionalFormatInput{
		IdempotencyKey: "custom-rule", Range: "A1:B3", RuleType: "custom_formula", Formula: `=$C1="완료"`,
		Style: json.RawMessage(`{"background":"#dcfce7"}`),
	}); err != nil {
		t.Fatal(err)
	}
	evaluation, err := repository.EvaluateConditionalFormats(ctx, sheet, cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 3, Column: 2}})
	if err != nil {
		t.Fatal(err)
	}
	matched := make(map[string]bool, len(evaluation.Items))
	for _, item := range evaluation.Items {
		matched[cellrange.Address(item.Row, item.Column)] = true
	}
	for _, address := range []string{"A1", "B1", "A3", "B3"} {
		if !matched[address] {
			t.Fatalf("%s should be highlighted: %#v", address, matched)
		}
	}
	for _, address := range []string{"A2", "B2"} {
		if matched[address] {
			t.Fatalf("%s must stay plain: %#v", address, matched)
		}
	}
}

func TestConditionalCustomFormulaValidation(t *testing.T) {
	_, _, err := NormalizeConditionalFormat(ConditionalFormat{SheetID: "s", Range: "A1:B2", RuleType: "custom_formula", Formula: "C1"})
	if err == nil {
		t.Fatal("a formula without = must be refused")
	}
	_, _, err = NormalizeConditionalFormat(ConditionalFormat{SheetID: "s", Range: "A1:B2", RuleType: "custom_formula", Formula: "=SUM("})
	if err == nil {
		t.Fatal("an unparseable formula must be refused")
	}
	// Switching a rule to another type must not leave the old formula behind.
	rule, _, err := NormalizeConditionalFormat(ConditionalFormat{SheetID: "s", Range: "A1:B2", RuleType: "value", Operator: "greater_than", Value: json.RawMessage(`10`), Formula: `=$C1="완료"`})
	if err != nil || rule.Formula != "" {
		t.Fatalf("value rule kept a formula: %#v, %v", rule, err)
	}
}

// 상위 N개는 한 칸만 봐서는 답할 수 없다. 범위 전체의 순위를 알아야 하고,
// 문턱에 걸친 값이 여럿이면 모두 들어가야 한다 — 3등이 둘이면 상위 3개는
// 넷이 아니라 둘까지가 아니라, 그 둘을 다 세는 것이 스프레드시트의 답이다.
func TestConditionalRankHighlightsTheTopAndBottomOfItsRange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "rank", OwnerID: "alice"})
	sheet := book.Sheets[0]
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`10`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`50`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`30`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`50`)},
		{Row: 5, Column: 1, Value: json.RawMessage(`20`)},
		{Row: 6, Column: 1, Value: json.RawMessage(`"글자"`)},
	}}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A1:A6")
	matched := func(key, operator string, count int) []int {
		t.Helper()
		rule, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{
			IdempotencyKey: key, Name: key, Range: "A1:A6", RuleType: "rank", Operator: operator,
			Value: json.RawMessage(strconv.Itoa(count)), Style: json.RawMessage(`{"background":"#dcfce7"}`), Priority: 1})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		evaluation, err := repository.EvaluateConditionalFormats(ctx, sheet.ID, selected)
		if err != nil {
			t.Fatal(err)
		}
		rows := make([]int, 0, len(evaluation.Items))
		for _, item := range evaluation.Items {
			rows = append(rows, item.Row)
		}
		sort.Ints(rows)
		if err := repository.DeleteConditionalFormat(ctx, rule.ID, "alice", nil); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	// 50 이 둘이므로 상위 2개는 그 둘이다.
	if rows := matched("top2", "top", 2); !reflect.DeepEqual(rows, []int{2, 4}) {
		t.Fatalf("top 2 = %v", rows)
	}
	if rows := matched("bottom2", "bottom", 2); !reflect.DeepEqual(rows, []int{1, 5}) {
		t.Fatalf("bottom 2 = %v", rows)
	}
	// 숫자 다섯 개의 40%는 두 개다.
	if rows := matched("top40", "top_percent", 40); !reflect.DeepEqual(rows, []int{2, 4}) {
		t.Fatalf("top 40%% = %v", rows)
	}
	// 25%는 1.25개이고, 올림해 두 개다. 열 개 중 상위 15%가 한 개면 너무 적다.
	if rows := matched("bottom25", "bottom_percent", 25); !reflect.DeepEqual(rows, []int{1, 5}) {
		t.Fatalf("bottom 25%% = %v", rows)
	}
	// 범위가 가진 것보다 많이 물으면 숫자 전부다. 글자는 순위에 들지 않는다.
	if rows := matched("top99", "top", 99); !reflect.DeepEqual(rows, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("top 99 = %v", rows)
	}
}

func TestConditionalRankValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "rank rules", OwnerID: "alice"})
	sheet := book.Sheets[0]
	for name, input := range map[string]CreateConditionalFormatInput{
		"unknown operator": {Operator: "middle", Value: json.RawMessage(`2`)},
		"zero":             {Operator: "top", Value: json.RawMessage(`0`)},
		"fraction":         {Operator: "top", Value: json.RawMessage(`2.5`)},
		"over a hundred":   {Operator: "top_percent", Value: json.RawMessage(`150`)},
		"missing":          {Operator: "top"},
		"text":             {Operator: "top", Value: json.RawMessage(`"둘"`)},
	} {
		input.IdempotencyKey, input.Name, input.Range, input.RuleType, input.Priority = name, name, "A1:A6", "rank", 1
		if _, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
	// 기본값은 상위이고, 색을 주지 않으면 알아볼 수 있는 색이 붙는다.
	rule, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{
		IdempotencyKey: "default", Name: "기본", Range: "A1:A6", RuleType: "rank", Value: json.RawMessage(`3`), Priority: 1})
	if err != nil || rule.Operator != "top" || len(rule.Style) == 0 {
		t.Fatalf("default rank rule = %#v, %v", rule, err)
	}
}

func TestConditionalIconSetSplitsTheRangeLikeExcel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "icons", OwnerID: "alice"})
	sheet := book.Sheets[0]
	// 0 부터 100 까지 고르게 놓으면 각 값이 곧 백분율이다. 엑셀은 3개짜리를
	// 33%, 67% 에서 나누므로 33 은 가운데, 32 는 아래 아이콘이어야 한다.
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`0`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`32`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`33`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`66`)},
		{Row: 5, Column: 1, Value: json.RawMessage(`67`)},
		{Row: 6, Column: 1, Value: json.RawMessage(`100`)},
		{Row: 7, Column: 1, Value: json.RawMessage(`"글자"`)},
	}}); err != nil {
		t.Fatal(err)
	}
	selected, _ := cellrange.Parse("A1:A7")
	icons := func(key, style string, reverse bool) map[int]int {
		t.Helper()
		rule, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{
			IdempotencyKey: key, Name: key, Range: "A1:A7", RuleType: "icon_set", IconStyle: style, IconReverse: reverse, Priority: 1})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		evaluation, err := repository.EvaluateConditionalFormats(ctx, sheet.ID, selected)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int]int{}
		for _, item := range evaluation.Items {
			if item.Icon == nil {
				t.Fatalf("%s: row %d matched without an icon", key, item.Row)
			}
			if item.Icon.Style != style {
				t.Fatalf("%s: row %d carries style %q", key, item.Row, item.Icon.Style)
			}
			got[item.Row] = item.Icon.Index
		}
		if err := repository.DeleteConditionalFormat(ctx, rule.ID, "alice", nil); err != nil {
			t.Fatal(err)
		}
		return got
	}
	// 글자는 아이콘을 받지 않는다 — 7행이 빠진 여섯 칸만 나온다.
	if got := icons("three", "3TrafficLights1", false); !reflect.DeepEqual(got, map[int]int{1: 0, 2: 0, 3: 1, 4: 1, 5: 2, 6: 2}) {
		t.Fatalf("three icons = %v", got)
	}
	// 뒤집으면 큰 값이 첫 아이콘을 받는다. 오류가 적을수록 좋은 열에 쓴다.
	if got := icons("reversed", "3TrafficLights1", true); !reflect.DeepEqual(got, map[int]int{1: 2, 2: 2, 3: 1, 4: 1, 5: 0, 6: 0}) {
		t.Fatalf("reversed icons = %v", got)
	}
	// 5개짜리는 20/40/60/80 에서 나뉜다.
	if got := icons("five", "5Arrows", false); !reflect.DeepEqual(got, map[int]int{1: 0, 2: 1, 3: 1, 4: 3, 5: 3, 6: 4}) {
		t.Fatalf("five icons = %v", got)
	}
}

func TestConditionalIconSetValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "icon rules", OwnerID: "alice"})
	sheet := book.Sheets[0]
	if _, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{
		IdempotencyKey: "unknown", Name: "unknown", Range: "A1:A6", RuleType: "icon_set", IconStyle: "3Flags", Priority: 1,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an icon set kanpic cannot draw was accepted: %v", err)
	}
	// 종류를 고르지 않으면 신호등이다. 색 눈금이나 막대의 설정은 남지 않는다.
	rule, err := repository.CreateConditionalFormat(ctx, sheet.ID, "alice", CreateConditionalFormatInput{
		IdempotencyKey: "default", Name: "기본", Range: "A1:A6", RuleType: "icon_set", Priority: 1,
		BarColor: "#38a3a5", MinColor: "#dcfce7", Style: json.RawMessage(`{"background":"#fee2e2"}`), StopIfTrue: true})
	if err != nil {
		t.Fatal(err)
	}
	if rule.IconStyle != "3TrafficLights1" || rule.BarColor != "" || rule.MinColor != "" || len(rule.Style) != 0 || rule.StopIfTrue {
		t.Fatalf("icon set rule kept fields it does not use: %+v", rule)
	}
	// 규칙 종류를 바꾸면 아이콘 설정은 사라진다.
	changed, err := repository.UpdateConditionalFormat(ctx, rule.ID, "alice", UpdateConditionalFormatInput{
		RuleType: stringPointer("data_bar"), ExpectedRevision: &rule.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if changed.IconStyle != "" || changed.IconReverse {
		t.Fatalf("data bar rule kept the icon set: %+v", changed)
	}
}
