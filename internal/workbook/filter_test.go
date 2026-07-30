package workbook

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEvaluateFilterCombinesValueConditionAndColorCriteria(t *testing.T) {
	view := FilterView{
		ID: "filter-1", Name: "영업 필터", Range: "A1:C5", HeaderRows: 1,
		Criteria: []FilterCriterion{
			{Column: 1, Operator: "values", Values: []json.RawMessage{json.RawMessage(`"서울"`), json.RawMessage(`"부산"`)}},
			{Column: 2, Operator: "greater_or_equal", Value: json.RawMessage(`10`)},
			{Column: 3, Operator: "background_color", Color: "#FEF3C7"},
		},
	}
	cells := []Cell{
		{Row: 1, Column: 1, Value: json.RawMessage(`"지역"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"서울"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`12`)}, {Row: 2, Column: 3, Value: json.RawMessage(`"A"`), Style: json.RawMessage(`{"background":"#fef3c7"}`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"부산"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`7`)}, {Row: 3, Column: 3, Style: json.RawMessage(`{"background":"#fef3c7"}`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"대전"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`20`)}, {Row: 4, Column: 3, Style: json.RawMessage(`{"background":"#fef3c7"}`)},
		{Row: 5, Column: 1, Value: json.RawMessage(`"서울"`)}, {Row: 5, Column: 2, Value: json.RawMessage(`15`)}, {Row: 5, Column: 3, Style: json.RawMessage(`{"background":"#ffffff"}`)},
	}
	result, err := EvaluateFilter(view, cells)
	if err != nil {
		t.Fatal(err)
	}
	if result.VisibleCount != 1 || result.HiddenCount != 3 || result.TotalCount != 4 || !reflect.DeepEqual(result.HiddenRows, []int{3, 4, 5}) {
		t.Fatalf("filter result: %#v", result)
	}
}

func TestEvaluateFilterSupportsBlankTextAndCaseSensitivity(t *testing.T) {
	cells := []Cell{{Row: 1, Column: 1, Value: json.RawMessage(`"Header"`)}, {Row: 2, Column: 1, Value: json.RawMessage(`"Alpha"`)}, {Row: 3, Column: 1, Value: json.RawMessage(`"alphabet"`)}}
	tests := []struct {
		name      string
		criterion FilterCriterion
		hidden    []int
	}{
		{name: "case insensitive contains", criterion: FilterCriterion{Column: 1, Operator: "contains", Value: json.RawMessage(`"ALPHA"`)}, hidden: []int{4}},
		{name: "case sensitive contains", criterion: FilterCriterion{Column: 1, Operator: "contains", Value: json.RawMessage(`"ALPHA"`), CaseSensitive: true}, hidden: []int{2, 3, 4}},
		{name: "blank", criterion: FilterCriterion{Column: 1, Operator: "is_blank"}, hidden: []int{2, 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := EvaluateFilter(FilterView{Name: "test", Range: "A1:A4", HeaderRows: 1, Criteria: []FilterCriterion{test.criterion}}, cells)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.HiddenRows, test.hidden) {
				t.Fatalf("hidden rows = %#v", result.HiddenRows)
			}
		})
	}
}

func TestNormalizeFilterViewRejectsInvalidDefinitions(t *testing.T) {
	tests := []FilterView{
		{Name: "", Range: "A1:B3"},
		{Name: "x", Range: "not-a-range"},
		{Name: "x", Range: "A1:B3", HeaderRows: 3},
		{Name: "x", Range: "A1:B3", Criteria: []FilterCriterion{{Column: 3, Operator: "equals", Value: json.RawMessage(`1`)}}},
		{Name: "x", Range: "A1:B3", Criteria: []FilterCriterion{{Column: 1, Operator: "unknown"}}},
		{Name: "x", Range: "A1:B3", Criteria: []FilterCriterion{{Column: 1, Operator: "values"}}},
		{Name: "x", Range: "A1:B3", Criteria: []FilterCriterion{{Column: 1, Operator: "background_color", Color: "red"}}},
		{Name: "x", Range: "A1:K100000"},
	}
	for index, view := range tests {
		if _, _, err := NormalizeFilterView(view); err == nil {
			t.Fatalf("case %d should fail: %#v", index, view)
		}
	}
}
