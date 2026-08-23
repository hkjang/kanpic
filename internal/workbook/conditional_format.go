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
	// MaxConditionalFormula matches the custom data validation limit: both are
	// one expression a person types into a dialog.
	MaxConditionalFormula = 2_000
	// MaxRankCount bounds a top/bottom N rule. Excel stops at 1,000 and a
	// larger N than the range holds simply matches everything anyway.
	MaxRankCount = 1_000
)

// conditionalIconCuts are the percent cut-offs Excel writes for its own icon
// set presets. Three icons change at 33 and 67 rather than at exact thirds, so
// a cell sitting on 33% has to land on the middle icon here too.
var conditionalIconCuts = map[int][]float64{
	3: {33, 67},
	4: {25, 50, 75},
	5: {20, 40, 60, 80},
}

// conditionalIconCounts lists the icon sets kanpic can draw, with how many
// icons each one holds. The names match the ones Excel writes into a workbook,
// so a rule made here survives a round trip through XLSX.
var conditionalIconCounts = map[string]int{
	"3Arrows": 3, "3TrafficLights1": 3, "3Symbols": 3,
	"4Arrows": 4, "5Arrows": 5, "5Quarters": 5,
}

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
	Formula         string          `json:"formula,omitempty"`
	Value           json.RawMessage `json:"value,omitempty"`
	Value2          json.RawMessage `json:"value2,omitempty"`
	Style           json.RawMessage `json:"style,omitempty"`
	MinColor        string          `json:"min_color,omitempty"`
	MidColor        string          `json:"mid_color,omitempty"`
	MaxColor        string          `json:"max_color,omitempty"`
	BarColor        string          `json:"bar_color,omitempty"`
	IconStyle       string          `json:"icon_style,omitempty"`
	IconReverse     bool            `json:"icon_reverse,omitempty"`
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
	Formula        string          `json:"formula,omitempty"`
	Value          json.RawMessage `json:"value,omitempty"`
	Value2         json.RawMessage `json:"value2,omitempty"`
	Style          json.RawMessage `json:"style,omitempty"`
	MinColor       string          `json:"min_color,omitempty"`
	MidColor       string          `json:"mid_color,omitempty"`
	MaxColor       string          `json:"max_color,omitempty"`
	BarColor       string          `json:"bar_color,omitempty"`
	IconStyle      string          `json:"icon_style,omitempty"`
	IconReverse    bool            `json:"icon_reverse,omitempty"`
	Priority       int             `json:"priority,omitempty"`
	StopIfTrue     bool            `json:"stop_if_true,omitempty"`
}

type UpdateConditionalFormatInput struct {
	Name             *string          `json:"name,omitempty"`
	Range            *string          `json:"range,omitempty"`
	RuleType         *string          `json:"rule_type,omitempty"`
	Operator         *string          `json:"operator,omitempty"`
	Formula          *string          `json:"formula,omitempty"`
	Value            *json.RawMessage `json:"value,omitempty"`
	Value2           *json.RawMessage `json:"value2,omitempty"`
	Style            *json.RawMessage `json:"style,omitempty"`
	MinColor         *string          `json:"min_color,omitempty"`
	MidColor         *string          `json:"mid_color,omitempty"`
	MaxColor         *string          `json:"max_color,omitempty"`
	BarColor         *string          `json:"bar_color,omitempty"`
	IconStyle        *string          `json:"icon_style,omitempty"`
	IconReverse      *bool            `json:"icon_reverse,omitempty"`
	Priority         *int             `json:"priority,omitempty"`
	StopIfTrue       *bool            `json:"stop_if_true,omitempty"`
	ExpectedRevision *int64           `json:"expected_revision,omitempty"`
}

type ConditionalDataBar struct {
	Color string  `json:"color"`
	Ratio float64 `json:"ratio"`
}

// ConditionalIcon is one icon out of a set. Index counts from the lowest
// icon up, so the client can pick the glyph without knowing the thresholds.
type ConditionalIcon struct {
	Style string `json:"style"`
	Index int    `json:"index"`
	Count int    `json:"count"`
}

type ConditionalFormatCell struct {
	Row            int                 `json:"row"`
	Column         int                 `json:"column"`
	Style          json.RawMessage     `json:"style,omitempty"`
	DataBar        *ConditionalDataBar `json:"data_bar,omitempty"`
	Icon           *ConditionalIcon    `json:"icon,omitempty"`
	MatchedRuleIDs []string            `json:"matched_rule_ids"`
}

type ConditionalFormatEvaluation struct {
	WorkbookVersion int64                   `json:"workbook_version"`
	SheetID         string                  `json:"sheet_id"`
	Range           string                  `json:"range"`
	Items           []ConditionalFormatCell `json:"items"`
}

// conditionalSourceRange is the block a rule has to read to be evaluated. For
// most rules that is its own range; a custom formula may name cells outside it
// — a status column two columns over, a threshold in a corner — so the block
// grows to cover everything the formula can reach as it shifts across the
// range. Reading a superset is safe; reading too little would silently make
// the formula see blanks.
func conditionalSourceRange(rule ConditionalFormat) cellrange.Range {
	selected, err := cellrange.Parse(rule.Range)
	if err != nil {
		return cellrange.Range{Start: cellrange.Position{Row: 1, Column: 1}, End: cellrange.Position{Row: 1, Column: 1}}
	}
	if rule.RuleType != "custom_formula" {
		return selected
	}
	height := selected.End.Row - selected.Start.Row
	width := selected.End.Column - selected.Start.Column
	dependencies, _ := formula.New().Dependencies(rule.Formula)
	for _, dependency := range dependencies {
		_, address, ok := formula.SplitCellKey(dependency)
		if !ok {
			continue
		}
		referenced, parseErr := cellrange.Parse(address)
		if parseErr != nil {
			continue
		}
		selected = conditionalUnion(selected, cellrange.Range{
			Start: referenced.Start,
			End:   cellrange.Position{Row: min(formula.MaxRows, referenced.End.Row+height), Column: min(formula.MaxColumns, referenced.End.Column+width)},
		})
	}
	return selected
}

func conditionalUnion(first, second cellrange.Range) cellrange.Range {
	return cellrange.Range{
		Start: cellrange.Position{Row: min(first.Start.Row, second.Start.Row), Column: min(first.Start.Column, second.Start.Column)},
		End:   cellrange.Position{Row: max(first.End.Row, second.End.Row), Column: max(first.End.Column, second.End.Column)},
	}
}

type conditionalFormatSource struct {
	Rule  ConditionalFormat
	Cells []Cell
}

// importedConditionalInput turns the rule an imported file described into the
// create input the normal path validates. Files go through the same door as
// requests.
func importedConditionalInput(imported ImportConditionalFormat, index int) (CreateConditionalFormatInput, bool) {
	input := CreateConditionalFormatInput{
		IdempotencyKey: fmt.Sprintf("import-conditional-%d", index),
		Range:          strings.ToUpper(strings.TrimSpace(imported.Range)),
		RuleType:       imported.RuleType,
		Operator:       imported.Operator,
		Formula:        imported.Formula,
		Value:          imported.Value,
		Value2:         imported.Value2,
		Style:          imported.Style,
		MinColor:       imported.MinColor,
		MidColor:       imported.MidColor,
		MaxColor:       imported.MaxColor,
		BarColor:       imported.BarColor,
		IconStyle:      imported.IconStyle,
		IconReverse:    imported.IconReverse,
		StopIfTrue:     imported.StopIfTrue,
	}
	if input.Range == "" || input.RuleType == "" {
		return CreateConditionalFormatInput{}, false
	}
	return input, true
}

func NewConditionalFormat(sheetID, actor string, input CreateConditionalFormatInput) (ConditionalFormat, cellrange.Range, error) {
	rule := ConditionalFormat{
		SheetID: sheetID, CreateKey: strings.TrimSpace(input.IdempotencyKey), Name: input.Name, Range: input.Range,
		RuleType: input.RuleType, Operator: input.Operator, Formula: input.Formula, Value: cloneJSON(input.Value), Value2: cloneJSON(input.Value2),
		Style: cloneJSON(input.Style), MinColor: input.MinColor, MidColor: input.MidColor, MaxColor: input.MaxColor,
		BarColor: input.BarColor, IconStyle: input.IconStyle, IconReverse: input.IconReverse,
		Priority: input.Priority, StopIfTrue: input.StopIfTrue, CreatedBy: actor, UpdatedBy: actor,
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
	if rule.RuleType != "icon_set" {
		rule.IconStyle, rule.IconReverse = "", false
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
		rule.MinColor, rule.MidColor, rule.MaxColor, rule.BarColor, rule.Formula = "", "", "", "", ""
	case "custom_formula":
		rule.Formula = strings.TrimSpace(rule.Formula)
		if !strings.HasPrefix(rule.Formula, "=") || len([]rune(rule.Formula)) > MaxConditionalFormula {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: custom conditional format requires a formula starting with = and up to %d characters", ErrInvalid, MaxConditionalFormula)
		}
		if _, formulaErr := formula.New().Dependencies(rule.Formula); formulaErr != nil {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: %s", ErrInvalid, formulaErr.Message)
		}
		if len(rule.Style) == 0 {
			rule.Style = json.RawMessage(`{"background":"#dbeafe","color":"#1e3a8a"}`)
		}
		if err := ValidateStylePatch(rule.Style); err != nil {
			return ConditionalFormat{}, cellrange.Range{}, err
		}
		rule.Operator, rule.Value, rule.Value2 = "", nil, nil
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
		rule.MinColor, rule.MidColor, rule.MaxColor, rule.BarColor, rule.Formula = "", "", "", "", ""
	case "rank":
		if rule.Operator == "" {
			rule.Operator = "top"
		}
		if rule.Operator != "top" && rule.Operator != "bottom" && rule.Operator != "top_percent" && rule.Operator != "bottom_percent" {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: rank rule operator must be top, bottom, top_percent, or bottom_percent", ErrInvalid)
		}
		wanted, ok := validationNumberRaw(rule.Value)
		percent := rule.Operator == "top_percent" || rule.Operator == "bottom_percent"
		limit := float64(MaxRankCount)
		if percent {
			limit = 100
		}
		if !ok || wanted < 1 || wanted > limit || wanted != math.Trunc(wanted) {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: rank rule needs a whole number from 1 to %d", ErrInvalid, int(limit))
		}
		if len(rule.Style) == 0 {
			rule.Style = json.RawMessage(`{"background":"#dcfce7","color":"#14532d"}`)
		}
		if err := ValidateStylePatch(rule.Style); err != nil {
			return ConditionalFormat{}, cellrange.Range{}, err
		}
		rule.Value2 = nil
		rule.MinColor, rule.MidColor, rule.MaxColor, rule.BarColor, rule.Formula = "", "", "", "", ""
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
		rule.Operator, rule.Value, rule.Value2, rule.Style, rule.BarColor, rule.Formula = "", nil, nil, nil, "", ""
		rule.StopIfTrue = false
	case "data_bar":
		if rule.BarColor == "" {
			rule.BarColor = "#38a3a5"
		}
		if !validHexColor(rule.BarColor) {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: data bar color must be #RRGGBB", ErrInvalid)
		}
		rule.Operator, rule.Value, rule.Value2, rule.Style, rule.Formula = "", nil, nil, nil, ""
		rule.MinColor, rule.MidColor, rule.MaxColor = "", "", ""
		rule.StopIfTrue = false
	case "icon_set":
		if rule.IconStyle == "" {
			rule.IconStyle = "3TrafficLights1"
		}
		if _, ok := conditionalIconCounts[rule.IconStyle]; !ok {
			return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: icon_style must be one of %s", ErrInvalid, conditionalIconStyleList())
		}
		rule.Operator, rule.Value, rule.Value2, rule.Style, rule.Formula = "", nil, nil, nil, ""
		rule.MinColor, rule.MidColor, rule.MaxColor, rule.BarColor = "", "", "", ""
		rule.StopIfTrue = false
	default:
		return ConditionalFormat{}, cellrange.Range{}, fmt.Errorf("%w: rule_type must be value, custom_formula, duplicate, rank, color_scale, data_bar, or icon_set", ErrInvalid)
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
	if input.Formula != nil {
		updated.Formula = *input.Formula
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
	if input.IconStyle != nil {
		updated.IconStyle = *input.IconStyle
	}
	if input.IconReverse != nil {
		updated.IconReverse = *input.IconReverse
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
		formulaCells := make(map[string]any)
		if rule.RuleType == "custom_formula" {
			formulaCells = make(map[string]any, len(source.Cells))
		}
		duplicates := make(map[string]int)
		minimum, maximum, hasNumber := 0.0, 0.0, false
		// 순위 규칙은 한 칸만 봐서는 답할 수 없다. 범위 전체의 숫자를 모아
		// 문턱값을 한 번 구해 두고 칸마다 그것과 견준다.
		ranked := make([]float64, 0)
		for _, cell := range source.Cells {
			value := conditionalCellValue(cell)
			values[cellKey{cell.Row, cell.Column}] = value
			if rule.RuleType == "custom_formula" {
				formulaCells[cellrange.Address(cell.Row, cell.Column)] = value
			}
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
				if rule.RuleType == "rank" {
					ranked = append(ranked, number)
				}
			}
		}
		threshold := rankThreshold(rule, ranked)
		for row := intersection.Start.Row; row <= intersection.End.Row; row++ {
			for column := intersection.Start.Column; column <= intersection.End.Column; column++ {
				key := cellKey{row, column}
				if stopped[key] {
					continue
				}
				value := values[key]
				matched, patch, bar, icon := false, json.RawMessage(nil), (*ConditionalDataBar)(nil), (*ConditionalIcon)(nil)
				if rule.RuleType == "custom_formula" {
					// The formula is written for the top-left cell of the range
					// and moves with each cell, which is what makes one rule
					// able to highlight a whole table by its own columns.
					shifted := formula.ShiftReferences(rule.Formula, row-ruleRange.Start.Row, column-ruleRange.Start.Column)
					evaluated := formula.New().Evaluate(shifted, formulaCells)
					matched = evaluated.Error == nil && conditionalTruthy(evaluated.Value)
					patch = cloneJSON(rule.Style)
				} else {
					matched, patch, bar, icon = evaluateConditionalRule(rule, value, duplicates, minimum, maximum, hasNumber, threshold)
				}
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
				if icon != nil {
					item.Icon = icon
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

func evaluateConditionalRule(rule ConditionalFormat, value any, duplicates map[string]int, minimum, maximum float64, hasNumber bool, threshold *float64) (bool, json.RawMessage, *ConditionalDataBar, *ConditionalIcon) {
	switch rule.RuleType {
	case "value":
		matched := conditionalValueMatches(value, rule)
		return matched, cloneJSON(rule.Style), nil, nil
	case "duplicate":
		if conditionalBlank(value) {
			return false, nil, nil, nil
		}
		count := duplicates[validationCanonical(value)]
		matched := rule.Operator == "duplicate" && count > 1 || rule.Operator == "unique" && count == 1
		return matched, cloneJSON(rule.Style), nil, nil
	case "rank":
		number, ok := numericChartValue(value)
		if !ok || threshold == nil {
			return false, nil, nil, nil
		}
		// 문턱값에 걸친 값은 모두 넣는다. 상위 3개를 물었는데 3등이 둘이면
		// 둘 다 상위 3개다. 엑셀도 같은 답을 낸다.
		matched := number <= *threshold
		if rule.Operator == "top" || rule.Operator == "top_percent" {
			matched = number >= *threshold
		}
		return matched, cloneJSON(rule.Style), nil, nil
	case "color_scale":
		number, ok := numericChartValue(value)
		if !ok || !hasNumber {
			return false, nil, nil, nil
		}
		ratio := conditionalRatio(number, minimum, maximum)
		color := interpolateConditionalScale(rule.MinColor, rule.MidColor, rule.MaxColor, ratio)
		patch, _ := json.Marshal(map[string]string{"background": color})
		return true, patch, nil, nil
	case "data_bar":
		number, ok := numericChartValue(value)
		if !ok || !hasNumber {
			return false, nil, nil, nil
		}
		return true, nil, &ConditionalDataBar{Color: rule.BarColor, Ratio: conditionalRatio(number, minimum, maximum)}, nil
	case "icon_set":
		number, ok := numericChartValue(value)
		if !ok || !hasNumber {
			return false, nil, nil, nil
		}
		return true, nil, nil, conditionalIconFor(rule, conditionalRatio(number, minimum, maximum))
	default:
		return false, nil, nil, nil
	}
}

// rankThreshold is the value a cell has to reach to be in the top or bottom N
// of its range. Ranking needs every number at once, so it is worked out before
// the cells are walked rather than per cell.
func rankThreshold(rule ConditionalFormat, ranked []float64) *float64 {
	if rule.RuleType != "rank" || len(ranked) == 0 {
		return nil
	}
	wanted, ok := validationNumberRaw(rule.Value)
	if !ok {
		return nil
	}
	count := int(wanted)
	if rule.Operator == "top_percent" || rule.Operator == "bottom_percent" {
		// 백분율은 올림한다. 열 개 중 상위 15%는 한 개가 아니라 두 개다.
		count = int(math.Ceil(float64(len(ranked)) * wanted / 100))
	}
	if count < 1 {
		return nil
	}
	if count > len(ranked) {
		count = len(ranked)
	}
	sorted := append([]float64(nil), ranked...)
	sort.Float64s(sorted)
	position := len(sorted) - count
	if rule.Operator == "bottom" || rule.Operator == "bottom_percent" {
		position = count - 1
	}
	return &sorted[position]
}

// conditionalTruthy reads a formula result the way a spreadsheet does: TRUE,
// a non-zero number and a non-empty string all count as a match.
func conditionalTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "TRUE")
	default:
		return false
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

// SupportedIconStyle reports whether kanpic can draw an icon set by that name.
func SupportedIconStyle(style string) bool {
	_, ok := conditionalIconCounts[style]
	return ok
}

func conditionalIconStyleList() string {
	names := make([]string, 0, len(conditionalIconCounts))
	for name := range conditionalIconCounts {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// conditionalIconFor picks the icon for one cell. The cut-offs are Excel's
// defaults - even thirds, quarters or fifths of the range - so a workbook
// exported to XLSX shows the same icons there.
func conditionalIconFor(rule ConditionalFormat, ratio float64) *ConditionalIcon {
	cuts, ok := conditionalIconCuts[conditionalIconCounts[rule.IconStyle]]
	if !ok {
		return nil
	}
	count := len(cuts) + 1
	percent := ratio * 100
	index := 0
	for _, cut := range cuts {
		if percent >= cut {
			index++
		}
	}
	if rule.IconReverse {
		index = count - 1 - index
	}
	return &ConditionalIcon{Style: rule.IconStyle, Index: index, Count: count}
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
