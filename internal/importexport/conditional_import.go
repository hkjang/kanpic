package importexport

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
)

var importCriteria = map[string]string{
	"equal to":                 "equals",
	"not equal to":             "not_equals",
	"greater than":             "greater_than",
	"greater than or equal to": "greater_or_equal",
	"less than":                "less_than",
	"less than or equal to":    "less_or_equal",
	"between":                  "between",
	"not between":              "not_between",
	"containing":               "contains",
	"not containing":           "not_contains",
}

// importConditionalFormats maps the rules of an XLSX sheet back to kanpic ones.
// Excel expresses far more than kanpic does — date periods, above-average —
// so anything without a faithful equivalent is left out rather than turned into
// the nearest rule that happens to compile.
// nearestIconStyle keeps a rule kanpic cannot draw exactly rather than dropping
// it: an unknown set becomes the one with the same number of icons, so a
// three-flag rule still shows three steps instead of vanishing on import.
func nearestIconStyle(style string) string {
	switch {
	case workbook.SupportedIconStyle(style):
		return style
	case strings.HasPrefix(style, "3"):
		return "3TrafficLights1"
	case strings.HasPrefix(style, "4"):
		return "4Arrows"
	case strings.HasPrefix(style, "5"):
		return "5Arrows"
	}
	return ""
}

func importConditionalFormats(file *excelize.File, name string) []workbook.ImportConditionalFormat {
	found, err := file.GetConditionalFormats(name)
	if err != nil || len(found) == 0 {
		return nil
	}
	result := make([]workbook.ImportConditionalFormat, 0, len(found))
	for area, options := range found {
		for _, item := range options {
			rule := workbook.ImportConditionalFormat{Range: strings.ToUpper(area), StopIfTrue: item.StopIfTrue}
			if item.Format != nil {
				if style, styleErr := file.GetStyle(*item.Format); styleErr == nil {
					rule.Style = canonicalStyleFromXLSX(style)
				}
			}
			switch item.Type {
			case "cell", "text":
				operator, known := importCriteria[item.Criteria]
				if !known {
					continue
				}
				rule.RuleType, rule.Operator = "value", operator
				first, second := splitConditionalValue(item.Value)
				rule.Value, rule.Value2 = first, second
				if len(rule.Value) == 0 {
					continue
				}
			case "blanks":
				rule.RuleType, rule.Operator = "value", "is_blank"
			case "no_blanks":
				rule.RuleType, rule.Operator = "value", "not_blank"
			case "formula":
				if strings.TrimSpace(item.Criteria) == "" {
					continue
				}
				rule.RuleType, rule.Formula = "custom_formula", "="+strings.TrimPrefix(strings.TrimSpace(item.Criteria), "=")
			case "duplicate", "unique":
				rule.RuleType, rule.Operator = "duplicate", item.Type
			case "top", "bottom":
				count, countErr := strconv.Atoi(strings.TrimSpace(item.Value))
				if countErr != nil || count < 1 {
					continue
				}
				rule.RuleType, rule.Operator = "rank", item.Type
				if item.Percent {
					rule.Operator = item.Type + "_percent"
					if count > 100 {
						continue
					}
				}
				rule.Value = json.RawMessage(strconv.Itoa(count))
			case "2_color_scale", "3_color_scale":
				rule.RuleType = "color_scale"
				rule.MinColor, rule.MaxColor = canonicalColor(item.MinColor), canonicalColor(item.MaxColor)
				if item.Type == "3_color_scale" {
					rule.MidColor = canonicalColor(item.MidColor)
				}
				if rule.MinColor == "" || rule.MaxColor == "" {
					continue
				}
			case "icon_set":
				rule.RuleType, rule.IconStyle, rule.IconReverse = "icon_set", nearestIconStyle(item.IconStyle), item.ReverseIcons
				if rule.IconStyle == "" {
					continue
				}
			case "data_bar":
				rule.RuleType, rule.BarColor = "data_bar", canonicalColor(item.BarColor)
				if rule.BarColor == "" {
					continue
				}
			default:
				continue
			}
			result = append(result, rule)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// splitConditionalValue reads the one or two comparison values Excel stores in
// a single field, keeping numbers as numbers and unquoting text.
func splitConditionalValue(value string) (json.RawMessage, json.RawMessage) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.SplitN(trimmed, ",", 2)
	first := conditionalRawValue(parts[0])
	if len(parts) == 1 {
		return first, nil
	}
	return first, conditionalRawValue(parts[1])
}

func conditionalRawValue(text string) json.RawMessage {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		unquoted := strings.ReplaceAll(strings.Trim(trimmed, "\""), "\"\"", "\"")
		encoded, _ := json.Marshal(unquoted)
		return encoded
	}
	var number float64
	if err := json.Unmarshal([]byte(trimmed), &number); err == nil {
		encoded, _ := json.Marshal(number)
		return encoded
	}
	encoded, _ := json.Marshal(trimmed)
	return encoded
}
