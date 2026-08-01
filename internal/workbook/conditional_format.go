package workbook

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

const (
	MaxConditionalFormats         = 100
	MaxConditionalFormatCells     = 100_000
	MaxConditionalEvaluationCells = 10_000
)

var conditionalValueOperators = map[string]struct{}{
	"equals": {}, "not_equals": {}, "greater_than": {}, "greater_or_equal": {},
	"less_than": {}, "less_or_equal": {}, "between": {}, "not_between": {},
	"contains": {}, "not_contains": {}, "is_blank": {}, "not_blank": {},
}

type ConditionalFormat struct {
	ID              string          `json:"id"`
	WorkbookID      string          `json:"workbook_id"`
	WorkbookVersion int64           `json:"workbook_version"`
	SheetID         string          `json:"sheet_id"`
	CreateKey       string          `json:"-"`
	Name            string          `json:"name"`
	Range           string          `json:"range"`
	RuleType        string          `json:"rule_type"`
	Operator        string          `json:"operator,omitempty"`
	Value           json.RawMessage `json:"value,omitempty"`
	Value2          json.RawMessage `json:"value2,omitempty"`
	Style           json.RawMessage `json:"style,omitempty"`
	MinColor        string          `json:"min_color,omitempty"`
	MidColor        string          `json:"mid_color,omitempty"`
	MaxColor        string          `json:"max_color,omitempty"`
	BarColor        string          `json:"bar_color,omitempty"`
	Priority        int             `json:"priority"`
	StopIfTrue      bool            `json:"stop_if_true"`
	Revision        int64           `json:"revision"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type CreateConditionalFormatInput struct {
	IdempotencyKey string          `json:"idempotency_key"`
	Name           string          `json:"name"`
	Range          string          `json:"range"`
	RuleType       string          `json:"rule_type"`
	Operator       string          `json:"operator,omitempty"`
	Value          json.RawMessage `json:"value,omitempty"`
	Value2         json.RawMessage `json:"value2,omitempty"`
	Style          json.RawMessage `json:"style,omitempty"`
	MinColor       string          `json:"min_color,omitempty"`
	MidColor       string          `json:"mid_color,omitempty"`
	MaxColor       string          `json:"max_color,omitempty"`
	BarColor       string          `json:"bar_color,omitempty"`
	Priority       int             `json:"priority,omitempty"`
	StopIfTrue     bool            `json:"stop_if_true,omitempty"`
}

type UpdateConditionalFormatInput struct {
	Name             *string          `json:"name,omitempty"`
	Range            *string          `json:"range,omitempty"`
	RuleType         *string          `json:"rule_type,omitempty"`
	Operator         *string          `json:"operator,omitempty"`
	Value            *json.RawMessage `json:"value,omitempty"`
	Value2           *json.RawMessage `json:"value2,omitempty"`
	Style            *json.RawMessage `json:"style,omitempty"`
	MinColor         *string          `json:"min_color,omitempty"`
	MidColor         *string          `json:"mid_color,omitempty"`
	MaxColor         *string          `json:"max_color,omitempty"`
	BarColor         *string          `json:"bar_color,omitempty"`
	Priority         *int             `json:"priority,omitempty"`
	StopIfTrue       *bool            `json:"stop_if_true,omitempty"`
	ExpectedRevision *int64           `json:"expected_revision,omitempty"`
}

type ConditionalDataBar struct {
	Color string  `json:"color"`
	Ratio float64 `json:"ratio"`
}

type ConditionalFormatCell struct {
	Row            int                 `json:"row"`
	Column         int                 `json:"column"`
	Style          json.RawMessage     `json:"style,omitempty"`
	DataBar        *ConditionalDataBar `json:"data_bar,omitempty"`
	MatchedRuleIDs []string            `json:"matched_rule_ids"`
}

type ConditionalFormatEvaluation struct {
	WorkbookVersion int64                   `json:"workbook_version"`
	SheetID         string                  `json:"sheet_id"`
	Range           string                  `json:"range"`
	Items           []ConditionalFormatCell `json:"items"`
}

type conditionalFormatSource struct {
	Rule  ConditionalFormat
	Cells []Cell
}

func NewConditionalFormat(sheetID, actor string, input CreateConditionalFormatInput) (ConditionalFormat, cellrange.Range, error) {
	rule := ConditionalFormat{
		SheetID: sheetID, CreateKey: strings.TrimSpace(input.IdempotencyKey), Name: input.Name, Range: input.Range,
		RuleType: input.RuleType, Operator: input.Operator, Value: cloneJSON(input.Value), Value2: cloneJSON(input.Value2),
		Style: cloneJSON(input.Style), MinColor: input.MinColor, MidColor: input.MidColor, MaxColor: input.MaxColor,
		BarColor: input.BarColor, Priority: input.Priority, StopIfTrue: input.StopIfTrue, CreatedBy: actor, UpdatedBy: actor,
	}
	if rule.CreateKey == "" {
		return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	return NormalizeConditionalFormat(rule)
}

func NormalizeConditionalFormat(rule ConditionalFormat) (ConditionalFormat, cellrange.Range, error) {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Range = strings.ToUpper(strings.TrimSpace(rule.Range))
	rule.RuleType = strings.ToLower(strings.TrimSpace(rule.RuleType))
	rule.Operator = strings.ToLower(strings.TrimSpace(rule.Operator))
	rule.MinColor = strings.ToLower(strings.TrimSpace(rule.MinColor))
	rule.MidColor = strings.ToLower(strings.TrimSpace(rule.MidColor))
	rule.MaxColor = strings.ToLower(strings.TrimSpace(rule.MaxColor))
	rule.BarColor = strings.ToLower(strings.TrimSpace(rule.BarColor))
	selected, err := cellrange.Parse(rule.Range)
	if err != nil {
		return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: invalid conditional format range", ErrInvalid)
	}
	cellCount := int64(selected.End.Row-selected.Start.Row+1) * int64(selected.End.Column-selected.Start.Column+1)
	if cellCount > MaxConditionalFormatCells {
		return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: conditional format range exceeds %d cells", ErrInvalid, MaxConditionalFormatCells)
	}
	rule.Range = cellrange.Address(selected.Start.Row, selected.Start.Column) + ":" + cellrange.Address(selected.End.Row, selected.End.Column)
	if rule.Name == "" {
		rule.Name = rule.Range + " 조건부 서식"
	}
	if len([]rune(rule.Name)) > 200 {
		return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: conditional format name is too long", ErrInvalid)
	}
	if rule.Priority == 0 {
		rule.Priority = 1
	}
	if rule.Priority < 1 || rule.Priority > 1000 {
		return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: priority must be between 1 and 1000", ErrInvalid)
	}
	switch rule.RuleType {
	case "value":
		if _, found := conditionalValueOperators[rule.Operator]; !found {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: invalid conditional value operator", ErrInvalid)
		}
		if rule.Operator == "is_blank" || rule.Operator == "not_blank" {
			rule.Value = nil
		} else {
			value, decodeErr := decodeValidationValue(rule.Value)
			if decodeErr != nil || value == nil || !validationScalar(value) {
				return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: conditional value must be a JSON scalar", ErrInvalid)
			}
		}
		if rule.Operator == "between" || rule.Operator == "not_between" {
			first, firstOK := validationNumberRaw(rule.Value)
			second, secondOK := validationNumberRaw(rule.Value2)
			if !firstOK || !secondOK || first > second {
				return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: between requires ordered numeric value and value2", ErrInvalid)
			}
		} else {
			rule.Value2 = nil
		}
		if len(rule.Style) == 0 {
			rule.Style = json.RawMessage(`{"background":"#fee2e2","color":"#991b1b"}`)
		}
		if err := ValidateStylePatch(rule.Style); err != nil {
			return ConditionalFormat{}, cellrange.Range{}, err
		}
		rule.MinColor, rule.MidColor, rule.MaxColor, rule.BarColor = "", "", "", ""
	case "duplicate":
		if rule.Operator == "" {
			rule.Operator = "duplicate"
		}
		if rule.Operator != "duplicate" && rule.Operator != "unique" {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: duplicate rule operator must be duplicate or unique", ErrInvalid)
		}
		if len(rule.Style) == 0 {
			rule.Style = json.RawMessage(`{"background":"#fef3c7","color":"#92400e"}`)
		}
		if err := ValidateStylePatch(rule.Style); err != nil {
			return ConditionalFormat{}, cellrange.Range{}, err
		}
		rule.Value, rule.Value2 = nil, nil
		rule.MinColor, rule.MidColor, rule.MaxColor, rule.BarColor = "", "", "", ""
	case "color_scale":
		if rule.MinColor == "" {
			rule.MinColor = "#dcfce7"
		}
		if rule.MaxColor == "" {
			rule.MaxColor = "#ef4444"
		}
		if !validHexColor(rule.MinColor) || !validHexColor(rule.MaxColor) || rule.MidColor != "" && !validHexColor(rule.MidColor) {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: color scale colors must be #RRGGBB", ErrInvalid)
		}
		rule.Operator, rule.Value, rule.Value2, rule.Style, rule.BarColor = "", nil, nil, nil, ""
		rule.StopIfTrue = false
	case "data_bar":
		if rule.BarColor == "" {
			rule.BarColor = "#38a3a5"
		}
		if !validHexColor(rule.BarColor) {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: data bar color must be #RRGGBB", ErrInvalid)
		}
		rule.Operator, rule.Value, rule.Value2, rule.Style = "", nil, nil, nil
		rule.MinColor, rule.MidColor, rule.MaxColor = "", "", ""
		rule.StopIfTrue = false
	default:
		return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: rule_type must be value, duplicate, color_scale, or data_bar", ErrInvalid)
	}
	return rule, selected, nil
}

func ApplyConditionalFormatUpdate(current ConditionalFormat, actor string, input UpdateConditionalFormatInput) (ConditionalFormat, cellrange.Range, error) {
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return ConditionalFormat{}, cellrange.Range{}, ErrRevision
	}
	updated := cloneConditionalFormat(current)
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.RuleType != nil {
		updated.RuleType = *input.RuleType
	}
	if input.Operator != nil {
		updated.Operator = *input.Operator
	}
	if input.Value != nil {
		updated.Value = cloneJSON(*input.Value)
	}
	if input.Value2 != nil {
		updated.Value2 = cloneJSON(*input.Value2)
	}
	if input.Style != nil {
		updated.Style = cloneJSON(*input.Style)
	}
	if input.MinColor != nil {
		updated.MinColor = *input.MinColor
	}
	if input.MidColor != nil {
		updated.MidColor = *input.MidColor
	}
	if input.MaxColor != nil {
		updated.MaxColor = *input.MaxColor
	}
	if input.BarColor != nil {
		updated.BarColor = *input.BarColor
	}
	if input.Priority != nil {
		updated.Priority = *input.Priority
	}
	if input.StopIfTrue != nil {
		updated.StopIfTrue = *input.StopIfTrue
	}
	updated.UpdatedBy = actor
	return NormalizeConditionalFormat(updated)
}

func cloneConditionalFormat(rule ConditionalFormat) ConditionalFormat {
	rule.Value, rule.Value2, rule.Style = cloneJSON(rule.Value), cloneJSON(rule.Value2), cloneJSON(rule.Style)
	return rule
}

func transformConditionalFormatForStructure(rule ConditionalFormat, input StructuralMutation, actor string, now time.Time) (ConditionalFormat, bool, error) {
	transformed, exists, err := transformRangeAddress(rule.Range, input)
	if err != nil {
		return ConditionalFormat{}, false, fmt.Errorf("%w: conditional format range exceeds spreadsheet bounds", ErrInvalid)
	}
	if !exists {
		return ConditionalFormat{}, false, nil
	}
	if transformed == rule.Range {
		return rule, true, nil
	}
	rule.Range, rule.Revision, rule.UpdatedBy, rule.UpdatedAt = transformed, rule.Revision+1, actor, now
	normalized, _, err := NormalizeConditionalFormat(rule)
	return normalized, err == nil, err
}

func transformRangeAddress(value string, input StructuralMutation) (string, bool, error) {
	return formula.TransformRangeAddress(value, formulaStructuralChange(input, "", ""))
}

func EvaluateConditionalFormats(sheetID string, workbookVersion int64, requested cellrange.Range, sources []conditionalFormatSource) (ConditionalFormatEvaluation, error) {
	count := int64(requested.End.Row-requested.Start.Row+1) * int64(requested.End.Column-requested.Start.Column+1)
	if count > MaxConditionalEvaluationCells {
		return ConditionalFormatEvaluation{}, fmt.Errorf("%w: conditional format evaluation exceeds %d cells", ErrInvalid, MaxConditionalEvaluationCells)
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Rule.Priority == sources[j].Rule.Priority {
			if sources[i].Rule.CreatedAt.Equal(sources[j].Rule.CreatedAt) {
				return sources[i].Rule.ID < sources[j].Rule.ID
			}
			return sources[i].Rule.CreatedAt.Before(sources[j].Rule.CreatedAt)
		}
		return sources[i].Rule.Priority < sources[j].Rule.Priority
	})
	items := make(map[cellKey]ConditionalFormatCell)
	stopped := make(map[cellKey]bool)
	for _, source := range sources {
		rule, ruleRange := source.Rule, mustConditionalRange(source.Rule.Range)
		intersection, intersects := conditionalIntersection(requested, ruleRange)
		if !intersects {
			continue
		}
		values := make(map[cellKey]any, len(source.Cells))
		duplicates := make(map[string]int)
		minimum, maximum, hasNumber := 0.0, 0.0, false
		for _, cell := range source.Cells {
			value := conditionalCellValue(cell)
			values[cellKey{cell.Row, cell.Column}] = value
			if !conditionalBlank(value) {
				duplicates[validationCanonical(value)]++
			}
			if number, ok := numericChartValue(value); ok {
				if !hasNumber || number < minimum {
					minimum = number
				}
				if !hasNumber || number > maximum {
					maximum = number
				}
				hasNumber = true
			}
		}
		for row := intersection.Start.Row; row <= intersection.End.Row; row++ {
			for column := intersection.Start.Column; column <= intersection.End.Column; column++ {
				key := cellKey{row, column}
				if stopped[key] {
					continue
				}
				value := values[key]
				matched, patch, bar := evaluateConditionalRule(rule, value, duplicates, minimum, maximum, hasNumber)
				if !matched {
					continue
				}
				item := items[key]
				item.Row, item.Column = row, column
				if len(patch) > 0 {
					merged, mergeErr := mergeStylePatch(item.Style, patch)
					if mergeErr != nil {
						return ConditionalFormatEvaluation{}, mergeErr
					}
					item.Style = merged
				}
				if bar != nil {
					item.DataBar = bar
				}
				item.MatchedRuleIDs = append(item.MatchedRuleIDs, rule.ID)
				items[key] = item
				if rule.StopIfTrue {
					stopped[key] = true
				}
			}
		}
	}
	result := ConditionalFormatEvaluation{WorkbookVersion: workbookVersion, SheetID: sheetID, Range: cellrange.Address(requested.Start.Row, requested.Start.Column) + ":" + cellrange.Address(requested.End.Row, requested.End.Column), Items: make([]ConditionalFormatCell, 0, len(items))}
	for _, item := range items {
		if item.MatchedRuleIDs == nil {
			item.MatchedRuleIDs = []string{}
		}
		result.Items = append(result.Items, item)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].Row == result.Items[j].Row {
			return result.Items[i].Column < result.Items[j].Column
		}
		return result.Items[i].Row < result.Items[j].Row
	})
	return result, nil
}

func evaluateConditionalRule(rule ConditionalFormat, value any, duplicates map[string]int, minimum, maximum float64, hasNumber bool) (bool, json.RawMessage, *ConditionalDataBar) {
	switch rule.RuleType {
	case "value":
		matched := conditionalValueMatches(value, rule)
		return matched, cloneJSON(rule.Style), nil
	case "duplicate":
		if conditionalBlank(value) {
			return false, nil, nil
		}
		count := duplicates[validationCanonical(value)]
		matched := rule.Operator == "duplicate" && count > 1 || rule.Operator == "unique" && count == 1
		return matched, cloneJSON(rule.Style), nil
	case "color_scale":
		number, ok := numericChartValue(value)
		if !ok || !hasNumber {
			return false, nil, nil
		}
		ratio := conditionalRatio(number, minimum, maximum)
		color := interpolateConditionalScale(rule.MinColor, rule.MidColor, rule.MaxColor, ratio)
		patch, _ := json.Marshal(map[string]string{"background": color})
		return true, patch, nil
	case "data_bar":
		number, ok := numericChartValue(value)
		if !ok || !hasNumber {
			return false, nil, nil
		}
		return true, nil, &ConditionalDataBar{Color: rule.BarColor, Ratio: conditionalRatio(number, minimum, maximum)}
	default:
		return false, nil, nil
	}
}

func conditionalValueMatches(value any, rule ConditionalFormat) bool {
	if rule.Operator == "is_blank" {
		return conditionalBlank(value)
	}
	if rule.Operator == "not_blank" {
		return !conditionalBlank(value)
	}
	reference, _ := decodeValidationValue(rule.Value)
	leftNumber, leftOK := conditionalNumber(value)
	rightNumber, rightOK := conditionalNumber(reference)
	compare := strings.Compare(strings.ToLower(validationText(value)), strings.ToLower(validationText(reference)))
	if leftOK && rightOK {
		compare = 0
		if leftNumber < rightNumber {
			compare = -1
		} else if leftNumber > rightNumber {
			compare = 1
		}
	}
	switch rule.Operator {
	case "equals":
		return compare == 0
	case "not_equals":
		return compare != 0
	case "greater_than":
		return compare > 0
	case "greater_or_equal":
		return compare >= 0
	case "less_than":
		return compare < 0
	case "less_or_equal":
		return compare <= 0
	case "contains":
		return strings.Contains(strings.ToLower(validationText(value)), strings.ToLower(validationText(reference)))
	case "not_contains":
		return !strings.Contains(strings.ToLower(validationText(value)), strings.ToLower(validationText(reference)))
	case "between", "not_between":
		second, _ := validationNumberRaw(rule.Value2)
		if !leftOK || !rightOK {
			return false
		}
		matched := leftNumber >= rightNumber && leftNumber <= second
		if rule.Operator == "not_between" {
			return !matched
		}
		return matched
	default:
		return false
	}
}

func conditionalNumber(value any) (float64, bool) {
	if number, ok := validationNumber(value); ok {
		return number, true
	}
	return numericChartValue(value)
}

func conditionalCellValue(cell Cell) any {
	if len(cell.Value) == 0 || string(cell.Value) == "null" {
		return nil
	}
	var value any
	if json.Unmarshal(cell.Value, &value) != nil {
		return nil
	}
	return value
}

func conditionalBlank(value any) bool {
	return value == nil || strings.TrimSpace(validationText(value)) == ""
}

func conditionalRatio(value, minimum, maximum float64) float64 {
	if maximum <= minimum {
		return 1
	}
	return math.Max(0, math.Min(1, (value-minimum)/(maximum-minimum)))
}

func interpolateConditionalScale(minColor, midColor, maxColor string, ratio float64) string {
	if midColor == "" {
		return interpolateConditionalColor(minColor, maxColor, ratio)
	}
	if ratio <= .5 {
		return interpolateConditionalColor(minColor, midColor, ratio*2)
	}
	return interpolateConditionalColor(midColor, maxColor, (ratio-.5)*2)
}

func interpolateConditionalColor(left, right string, ratio float64) string {
	parse := func(value string) [3]int {
		result := [3]int{}
		for index := range result {
			parsed, _ := strconv.ParseInt(value[1+index*2:3+index*2], 16, 32)
			result[index] = int(parsed)
		}
		return result
	}
	first, second := parse(left), parse(right)
	return fmt.Sprintf("#%02x%02x%02x", int(math.Round(float64(first[0])+(float64(second[0]-first[0])*ratio))), int(math.Round(float64(first[1])+(float64(second[1]-first[1])*ratio))), int(math.Round(float64(first[2])+(float64(second[2]-first[2])*ratio))))
}

func conditionalIntersection(left, right cellrange.Range) (cellrange.Range, bool) {
	result := cellrange.Range{
		Start: cellrange.Position{Row: max(left.Start.Row, right.Start.Row), Column: max(left.Start.Column, right.Start.Column)},
		End:   cellrange.Position{Row: min(left.End.Row, right.End.Row), Column: min(left.End.Column, right.End.Column)},
	}
	return result, result.Start.Row <= result.End.Row && result.Start.Column <= result.End.Column
}

func mustConditionalRange(value string) cellrange.Range {
	selected, _ := cellrange.Parse(value)
	return selected
}
