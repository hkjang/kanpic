package importexport

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
)

// MaxExportedValidationOptions bounds a drop list written into XLSX. Excel
// stores an inline list as a single formula string with a hard length limit,
// so a very long list is exported without its options rather than corrupting
// the file.
const MaxExportedValidationOptions = 100

func validationText(raw json.RawMessage) string {
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
		return typed
	case bool:
		if typed {
			return "TRUE"
		}
		return "FALSE"
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.10f", typed), "0"), ".")
	default:
		return fmt.Sprint(typed)
	}
}

var exportOperators = map[string]excelize.DataValidationOperator{
	"between":          excelize.DataValidationOperatorBetween,
	"not_between":      excelize.DataValidationOperatorNotBetween,
	"equal":            excelize.DataValidationOperatorEqual,
	"not_equal":        excelize.DataValidationOperatorNotEqual,
	"greater_than":     excelize.DataValidationOperatorGreaterThan,
	"greater_or_equal": excelize.DataValidationOperatorGreaterThanOrEqual,
	"less_than":        excelize.DataValidationOperatorLessThan,
	"less_or_equal":    excelize.DataValidationOperatorLessThanOrEqual,
}

// exportValidation turns one kanpic rule into the XLSX equivalent. A rule that
// Excel cannot express is skipped rather than approximated: a dropdown that
// offers the wrong values is worse than no dropdown.
func exportValidation(rule workbook.DataValidation) *excelize.DataValidation {
	dv := excelize.NewDataValidation(rule.AllowBlank)
	dv.SetSqref(rule.Range)
	dv.ShowErrorMessage = rule.RejectInput
	if rule.HelpText != "" {
		dv.SetError(excelize.DataValidationErrorStyleStop, "입력값 확인", rule.HelpText)
	}
	switch rule.RuleType {
	case "list", "checkbox":
		options := make([]string, 0, len(rule.Options))
		for _, option := range rule.Options {
			text := option.Label
			if text == "" {
				text = validationText(option.Value)
			}
			if text != "" {
				options = append(options, text)
			}
		}
		if len(options) == 0 || len(options) > MaxExportedValidationOptions {
			return nil
		}
		if err := dv.SetDropList(options); err != nil {
			return nil
		}
	case "list_range":
		if rule.SourceRange == "" {
			return nil
		}
		dv.SetSqrefDropList(rule.SourceRange)
	case "number", "date":
		operator, known := exportOperators[rule.Operator]
		if !known {
			return nil
		}
		kind := excelize.DataValidationTypeDecimal
		if rule.RuleType == "date" {
			kind = excelize.DataValidationTypeDate
		}
		first, second := validationText(rule.Value), validationText(rule.Value2)
		if first == "" {
			return nil
		}
		if err := dv.SetRange(first, second, kind, operator); err != nil {
			return nil
		}
	case "custom_formula":
		if rule.Formula == "" {
			return nil
		}
		dv.Type = "custom"
		dv.Formula1 = strings.TrimPrefix(rule.Formula, "=")
	default:
		return nil
	}
	return dv
}

// importValidations maps the validations of an XLSX sheet back to kanpic rules.
// Excel expresses far more than kanpic does, so anything without a faithful
// equivalent is left out instead of being bent into the nearest rule.
func importValidations(file *excelize.File, name string) []workbook.ImportValidation {
	found, err := file.GetDataValidations(name)
	if err != nil || len(found) == 0 {
		return nil
	}
	result := make([]workbook.ImportValidation, 0, len(found))
	for _, dv := range found {
		if dv == nil || strings.TrimSpace(dv.Sqref) == "" {
			continue
		}
		// Excel writes several ranges in one rule; kanpic stores one range per
		// rule, so each is imported on its own.
		for _, area := range strings.Fields(strings.ReplaceAll(dv.Sqref, ",", " ")) {
			rule := workbook.ImportValidation{Range: area, AllowBlank: dv.AllowBlank, RejectInput: dv.ShowErrorMessage}
			if dv.Error != nil {
				rule.HelpText = *dv.Error
			}
			switch dv.Type {
			case "list":
				formula := strings.TrimPrefix(strings.TrimSpace(dv.Formula1), "=")
				if strings.HasPrefix(formula, "\"") {
					rule.RuleType, rule.Options = "list", splitDropList(formula)
					if len(rule.Options) == 0 {
						continue
					}
				} else if formula != "" {
					rule.RuleType, rule.SourceRange = "list_range", strings.ReplaceAll(formula, "$", "")
				} else {
					continue
				}
			case "whole", "decimal", "date":
				operator := importOperator(dv.Operator)
				if operator == "" {
					continue
				}
				rule.RuleType, rule.Operator = "number", operator
				if dv.Type == "date" {
					rule.RuleType = "date"
				}
				rule.Value, rule.Value2 = strings.TrimPrefix(dv.Formula1, "="), strings.TrimPrefix(dv.Formula2, "=")
				if rule.Value == "" {
					continue
				}
			case "custom":
				formula := strings.TrimSpace(dv.Formula1)
				if formula == "" {
					continue
				}
				rule.RuleType, rule.Formula = "custom_formula", "="+strings.TrimPrefix(formula, "=")
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

// splitDropList reads the quoted, comma separated list Excel stores inline.
func splitDropList(formula string) []string {
	trimmed := strings.Trim(formula, "\"")
	if trimmed == "" {
		return nil
	}
	options := make([]string, 0, 8)
	for _, item := range strings.Split(trimmed, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			options = append(options, item)
		}
	}
	return options
}

func importOperator(operator string) string {
	switch operator {
	case "between":
		return "between"
	case "notBetween":
		return "not_between"
	case "equal":
		return "equal"
	case "notEqual":
		return "not_equal"
	case "greaterThan":
		return "greater_than"
	case "greaterThanOrEqual":
		return "greater_or_equal"
	case "lessThan":
		return "less_than"
	case "lessThanOrEqual":
		return "less_or_equal"
	default:
		return ""
	}
}
