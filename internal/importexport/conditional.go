package importexport

import (
	"encoding/json"
	"strings"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
)

// Excel writes colours without the leading hash a CSS colour carries.
func hexColor(value string) string { return strings.TrimPrefix(strings.TrimSpace(value), "#") }

var conditionalCriteria = map[string]string{
	"equals":           "equal to",
	"not_equals":       "not equal to",
	"greater_than":     "greater than",
	"greater_or_equal": "greater than or equal to",
	"less_than":        "less than",
	"less_or_equal":    "less than or equal to",
	"between":          "between",
	"not_between":      "not between",
	"contains":         "containing",
	"not_contains":     "not containing",
}

// exportConditionalFormat maps one kanpic rule onto the XLSX equivalent. A rule
// Excel has no faithful equivalent for is skipped: a sheet that highlights the
// wrong cells is worse than one that highlights none.
func exportConditionalFormat(rule workbook.ConditionalFormat, styleFor func(json.RawMessage) *int) *excelize.ConditionalFormatOptions {
	switch rule.RuleType {
	case "value":
		criteria, known := conditionalCriteria[rule.Operator]
		if !known {
			// Blank tests are a formula in Excel rather than a cell comparison.
			switch rule.Operator {
			case "is_blank":
				return &excelize.ConditionalFormatOptions{Type: "blanks", Format: styleFor(rule.Style), StopIfTrue: rule.StopIfTrue}
			case "not_blank":
				return &excelize.ConditionalFormatOptions{Type: "no_blanks", Format: styleFor(rule.Style), StopIfTrue: rule.StopIfTrue}
			}
			return nil
		}
		value := conditionalValueText(rule.Value)
		if value == "" {
			return nil
		}
		if rule.Operator == "between" || rule.Operator == "not_between" {
			second := conditionalValueText(rule.Value2)
			if second == "" {
				return nil
			}
			value += "," + second
		}
		kind := "cell"
		if rule.Operator == "contains" || rule.Operator == "not_contains" {
			kind = "text"
		}
		return &excelize.ConditionalFormatOptions{Type: kind, Criteria: criteria, Value: value, Format: styleFor(rule.Style), StopIfTrue: rule.StopIfTrue}
	case "custom_formula":
		if rule.Formula == "" {
			return nil
		}
		return &excelize.ConditionalFormatOptions{Type: "formula", Criteria: strings.TrimPrefix(rule.Formula, "="), Format: styleFor(rule.Style), StopIfTrue: rule.StopIfTrue}
	case "duplicate":
		kind := "duplicate"
		if rule.Operator == "unique" {
			kind = "unique"
		}
		return &excelize.ConditionalFormatOptions{Type: kind, Criteria: "=", Format: styleFor(rule.Style), StopIfTrue: rule.StopIfTrue}
	case "rank":
		kind := "top"
		if rule.Operator == "bottom" || rule.Operator == "bottom_percent" {
			kind = "bottom"
		}
		return &excelize.ConditionalFormatOptions{
			Type: kind, Criteria: "=", Value: conditionalValueText(rule.Value),
			Percent: rule.Operator == "top_percent" || rule.Operator == "bottom_percent",
			Format:  styleFor(rule.Style), StopIfTrue: rule.StopIfTrue,
		}
	case "color_scale":
		options := &excelize.ConditionalFormatOptions{
			Type: "2_color_scale", Criteria: "=",
			MinType: "min", MinColor: hexColor(rule.MinColor),
			MaxType: "max", MaxColor: hexColor(rule.MaxColor),
		}
		if rule.MidColor != "" {
			options.Type, options.MidType, options.MidValue, options.MidColor = "3_color_scale", "percentile", "50", hexColor(rule.MidColor)
		}
		return options
	case "data_bar":
		return &excelize.ConditionalFormatOptions{
			Type: "data_bar", Criteria: "=",
			MinType: "min", MaxType: "max", BarColor: hexColor(rule.BarColor), BarSolid: true,
		}
	default:
		return nil
	}
}

// conditionalValueText renders the comparison value the way Excel stores it:
// numbers bare, text quoted.
func conditionalValueText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		if typed == "" {
			return ""
		}
		return "\"" + strings.ReplaceAll(typed, "\"", "\"\"") + "\""
	default:
		return validationText(raw)
	}
}
