package workbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

const (
	MaxValidationRows    = 1_048_576
	MaxValidationColumns = 16_384
	MaxValidationOptions = 500
	maxValidationFormula = 2_000
	maxValidationHelp    = 500
	maxEvaluationDetails = 100
)

var comparisonOperators = map[string]struct{}{
	"between": {}, "not_between": {}, "equal": {}, "not_equal": {},
	"greater_than": {}, "greater_or_equal": {}, "less_than": {}, "less_or_equal": {},
}

type ValidationFailure struct {
	Violations []ValidationViolation
}

func (e *ValidationFailure) Error() string {
	if len(e.Violations) == 0 {
		return ErrValidation.Error()
	}
	first := e.Violations[0]
	return fmt.Sprintf("%s: %s (%s)", ErrValidation, first.Message, cellrange.Address(first.Row, first.Column))
}

func (e *ValidationFailure) Unwrap() error { return ErrValidation }

func NewDataValidation(sheetID, actor string, input CreateDataValidationInput) (DataValidation, cellrange.Range, error) {
	allowBlank, rejectInput, showDropdown := true, true, true
	if input.AllowBlank != nil {
		allowBlank = *input.AllowBlank
	}
	if input.RejectInput != nil {
		rejectInput = *input.RejectInput
	}
	if input.ShowDropdown != nil {
		showDropdown = *input.ShowDropdown
	}
	rule := DataValidation{
		SheetID: sheetID, CreateKey: strings.TrimSpace(input.IdempotencyKey), Range: input.Range,
		RuleType: input.RuleType, Operator: input.Operator, Options: cloneValidationOptions(input.Options),
		Value: cloneJSON(input.Value), Value2: cloneJSON(input.Value2), Formula: input.Formula,
		AllowBlank: allowBlank, RejectInput: rejectInput, ShowDropdown: showDropdown,
		DisplayStyle: input.DisplayStyle, HelpText: input.HelpText, CreatedBy: actor, UpdatedBy: actor,
	}
	if rule.CreateKey == "" {
		return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	return NormalizeDataValidation(rule)
}

func NormalizeDataValidation(rule DataValidation) (DataValidation, cellrange.Range, error) {
	rule.Range = strings.ToUpper(strings.TrimSpace(rule.Range))
	rule.RuleType = strings.ToLower(strings.TrimSpace(rule.RuleType))
	rule.Operator = strings.ToLower(strings.TrimSpace(rule.Operator))
	rule.Formula = strings.TrimSpace(rule.Formula)
	rule.DisplayStyle = strings.ToLower(strings.TrimSpace(rule.DisplayStyle))
	rule.HelpText = strings.TrimSpace(rule.HelpText)
	selected, err := cellrange.Parse(rule.Range)
	if err != nil || selected.End.Row > MaxValidationRows || selected.End.Column > MaxValidationColumns {
		return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: invalid data validation range", ErrInvalid)
	}
	if len([]rune(rule.HelpText)) > maxValidationHelp {
		return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: validation help text exceeds %d characters", ErrInvalid, maxValidationHelp)
	}
	if rule.DisplayStyle == "" {
		rule.DisplayStyle = "chip"
	}
	if rule.DisplayStyle != "chip" && rule.DisplayStyle != "arrow" && rule.DisplayStyle != "plain" {
		return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: display_style must be chip, arrow, or plain", ErrInvalid)
	}
	switch rule.RuleType {
	case "list":
		if rule.Operator == "" {
			rule.Operator = "in_list"
		}
		if rule.Operator != "in_list" || len(rule.Options) == 0 || len(rule.Options) > MaxValidationOptions {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: list validation requires 1 to %d options", ErrInvalid, MaxValidationOptions)
		}
		seen := make(map[string]struct{}, len(rule.Options))
		for index := range rule.Options {
			option := &rule.Options[index]
			option.Label = strings.TrimSpace(option.Label)
			option.Color = strings.ToLower(strings.TrimSpace(option.Color))
			value, err := decodeValidationValue(option.Value)
			if err != nil || value == nil || !validationScalar(value) {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: list option values must be non-null JSON scalars", ErrInvalid)
			}
			if len([]rune(option.Label)) > 128 {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: list option label exceeds 128 characters", ErrInvalid)
			}
			if option.Color != "" && !validHexColor(option.Color) {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: list option color must be #RRGGBB", ErrInvalid)
			}
			canonical := validationCanonical(value)
			if _, duplicate := seen[canonical]; duplicate {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: list option values must be unique", ErrInvalid)
			}
			seen[canonical] = struct{}{}
			if option.Label == "" {
				option.Label = validationText(value)
			}
		}
		rule.Value, rule.Value2, rule.Formula = nil, nil, ""
	case "number":
		if _, ok := comparisonOperators[rule.Operator]; !ok {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: unsupported number validation operator", ErrInvalid)
		}
		if _, ok := validationNumberRaw(rule.Value); !ok {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: number validation requires a numeric value", ErrInvalid)
		}
		if rule.Operator == "between" || rule.Operator == "not_between" {
			first, _ := validationNumberRaw(rule.Value)
			second, ok := validationNumberRaw(rule.Value2)
			if !ok || first > second {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: number range requires ordered value and value2", ErrInvalid)
			}
		} else {
			rule.Value2 = nil
		}
		rule.Options, rule.Formula, rule.ShowDropdown, rule.DisplayStyle = nil, "", false, "plain"
	case "date":
		if _, ok := comparisonOperators[rule.Operator]; !ok {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: unsupported date validation operator", ErrInvalid)
		}
		first, ok := validationDateRaw(rule.Value)
		if !ok {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: date validation requires an ISO date value", ErrInvalid)
		}
		if rule.Operator == "between" || rule.Operator == "not_between" {
			second, ok := validationDateRaw(rule.Value2)
			if !ok || first.After(second) {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: date range requires ordered value and value2", ErrInvalid)
			}
		} else {
			rule.Value2 = nil
		}
		rule.Options, rule.Formula, rule.ShowDropdown, rule.DisplayStyle = nil, "", false, "plain"
	case "checkbox":
		rule.Operator = "in_list"
		// A checkbox is TRUE/FALSE unless the sheet uses its own pair of values,
		// which is how a "예/아니오" column stays a checkbox.
		if len(rule.Options) == 0 {
			checked, _ := json.Marshal(true)
			unchecked, _ := json.Marshal(false)
			rule.Options = []ValidationOption{{Value: checked}, {Value: unchecked}}
		}
		if len(rule.Options) != 2 {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: checkbox validation needs exactly two values: checked and unchecked", ErrInvalid)
		}
		for index := range rule.Options {
			option := &rule.Options[index]
			option.Label = strings.TrimSpace(option.Label)
			option.Color = strings.ToLower(strings.TrimSpace(option.Color))
			value, err := decodeValidationValue(option.Value)
			if err != nil || value == nil || !validationScalar(value) {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: checkbox values must be non-null JSON scalars", ErrInvalid)
			}
			if option.Label == "" {
				option.Label = validationText(value)
			}
			if option.Color != "" && !validHexColor(option.Color) {
				return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: checkbox colors must be #RRGGBB", ErrInvalid)
			}
		}
		if validationCanonical(mustDecodeValidationValue(rule.Options[0].Value)) == validationCanonical(mustDecodeValidationValue(rule.Options[1].Value)) {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: the checked and unchecked values must differ", ErrInvalid)
		}
		rule.Value, rule.Value2, rule.Formula = nil, nil, ""
		rule.ShowDropdown, rule.DisplayStyle = false, "plain"
	case "custom_formula":
		if rule.Operator == "" {
			rule.Operator = "custom"
		}
		if rule.Operator != "custom" || !strings.HasPrefix(rule.Formula, "=") || len([]rune(rule.Formula)) > maxValidationFormula {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: custom validation requires a formula up to %d characters", ErrInvalid, maxValidationFormula)
		}
		if _, formulaError := formula.New().Dependencies(rule.Formula); formulaError != nil {
			return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: invalid validation formula: %s", ErrInvalid, formulaError.Message)
		}
		rule.Options, rule.Value, rule.Value2, rule.ShowDropdown, rule.DisplayStyle = nil, nil, nil, false, "plain"
	default:
		return DataValidation{}, cellrange.Range{}, fmt.Errorf("%w: rule_type must be list, checkbox, number, date, or custom_formula", ErrInvalid)
	}
	return rule, selected, nil
}

func ApplyDataValidationUpdate(current DataValidation, actor string, input UpdateDataValidationInput) (DataValidation, cellrange.Range, error) {
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return DataValidation{}, cellrange.Range{}, ErrRevision
	}
	updated := cloneDataValidation(current)
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.RuleType != nil {
		updated.RuleType = *input.RuleType
	}
	if input.Operator != nil {
		updated.Operator = *input.Operator
	}
	if input.Options != nil {
		updated.Options = cloneValidationOptions(*input.Options)
	}
	if input.Value != nil {
		updated.Value = cloneJSON(*input.Value)
	}
	if input.Value2 != nil {
		updated.Value2 = cloneJSON(*input.Value2)
	}
	if input.Formula != nil {
		updated.Formula = *input.Formula
	}
	if input.AllowBlank != nil {
		updated.AllowBlank = *input.AllowBlank
	}
	if input.RejectInput != nil {
		updated.RejectInput = *input.RejectInput
	}
	if input.ShowDropdown != nil {
		updated.ShowDropdown = *input.ShowDropdown
	}
	if input.DisplayStyle != nil {
		updated.DisplayStyle = *input.DisplayStyle
	}
	if input.HelpText != nil {
		updated.HelpText = *input.HelpText
	}
	updated.UpdatedBy = actor
	return NormalizeDataValidation(updated)
}

func ValidationRangesOverlap(left, right cellrange.Range) bool {
	return left.Start.Row <= right.End.Row && right.Start.Row <= left.End.Row && left.Start.Column <= right.End.Column && right.Start.Column <= left.End.Column
}

func ValidateCellInputs(rules []DataValidation, existing map[cellKey]Cell, expanded, submitted []CellInput) ([]ValidationViolation, error) {
	prospective := make(map[cellKey]Cell, len(existing)+len(expanded))
	for key, cell := range existing {
		prospective[key] = cloneCell(cell)
	}
	for _, input := range expanded {
		key := cellKey{input.Row, input.Column}
		cell := Cell{Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), SpillSource: input.SpillSource}
		if isEmptyCell(cell) {
			delete(prospective, key)
		} else {
			prospective[key] = cell
		}
	}
	cells := validationFormulaCells(prospective)
	warnings, failures := make([]ValidationViolation, 0), make([]ValidationViolation, 0)
	for _, input := range submitted {
		cell := prospective[cellKey{input.Row, input.Column}]
		for _, rule := range rules {
			normalized, selected, err := NormalizeDataValidation(rule)
			if err != nil {
				return nil, err
			}
			if !selected.Contains(input.Row, input.Column) {
				continue
			}
			valid, message := validateCellValue(normalized, selected, input.Row, input.Column, cell.Value, cells)
			if valid {
				continue
			}
			violation := ValidationViolation{ValidationID: normalized.ID, Row: input.Row, Column: input.Column, Message: message}
			if normalized.RejectInput {
				failures = append(failures, violation)
			} else {
				warnings = append(warnings, violation)
			}
		}
	}
	if len(failures) > 0 {
		return warnings, &ValidationFailure{Violations: failures}
	}
	return warnings, nil
}

func EvaluateDataValidation(rule DataValidation, cells []Cell) (ValidationEvaluation, error) {
	normalized, selected, err := NormalizeDataValidation(rule)
	if err != nil {
		return ValidationEvaluation{}, err
	}
	byCoordinate := make(map[cellKey]Cell, len(cells))
	for _, cell := range cells {
		if selected.Contains(cell.Row, cell.Column) {
			byCoordinate[cellKey{cell.Row, cell.Column}] = cloneCell(cell)
		}
	}
	formulaCells := validationFormulaCells(byCoordinate)
	result := ValidationEvaluation{ValidationID: normalized.ID, Range: normalized.Range, InvalidCells: make([]ValidationViolation, 0)}
	for row := selected.Start.Row; row <= selected.End.Row; row++ {
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			result.CheckedCells++
			valid, message := validateCellValue(normalized, selected, row, column, byCoordinate[cellKey{row, column}].Value, formulaCells)
			if valid {
				result.ValidCells++
				continue
			}
			if len(result.InvalidCells) < maxEvaluationDetails {
				result.InvalidCells = append(result.InvalidCells, ValidationViolation{ValidationID: normalized.ID, Row: row, Column: column, Message: message})
			} else {
				result.Truncated = true
			}
		}
	}
	return result, nil
}

func validateCellValue(rule DataValidation, selected cellrange.Range, row, column int, raw json.RawMessage, cells map[string]any) (bool, string) {
	actual, err := decodeValidationValue(raw)
	if err != nil {
		return false, "셀 값이 올바른 JSON 값이 아닙니다."
	}
	if validationBlank(actual) {
		if rule.AllowBlank {
			return true, ""
		}
		return false, validationMessage(rule, "빈 값은 허용되지 않습니다.")
	}
	valid := false
	switch rule.RuleType {
	case "list", "checkbox":
		for _, option := range rule.Options {
			expected, _ := decodeValidationValue(option.Value)
			if compareFilterValues(actual, expected, true) == 0 {
				valid = true
				break
			}
		}
	case "number":
		number, ok := validationNumber(actual)
		if ok {
			first, _ := validationNumberRaw(rule.Value)
			second, _ := validationNumberRaw(rule.Value2)
			valid = compareValidation(number, first, second, rule.Operator)
		}
	case "date":
		date, ok := validationDate(actual)
		if ok {
			first, _ := validationDateRaw(rule.Value)
			second, _ := validationDateRaw(rule.Value2)
			valid = compareValidation(float64(date.Unix()), float64(first.Unix()), float64(second.Unix()), rule.Operator)
		}
	case "custom_formula":
		shifted := formula.ShiftReferences(rule.Formula, row-selected.Start.Row, column-selected.Start.Column)
		result := formula.New().Evaluate(shifted, cells)
		valid = result.Error == nil && validationTruthy(result.Value)
	}
	if valid {
		return true, ""
	}
	return false, validationMessage(rule, defaultValidationMessage(rule))
}

func compareValidation(actual, first, second float64, operator string) bool {
	switch operator {
	case "between":
		return actual >= first && actual <= second
	case "not_between":
		return actual < first || actual > second
	case "equal":
		return actual == first
	case "not_equal":
		return actual != first
	case "greater_than":
		return actual > first
	case "greater_or_equal":
		return actual >= first
	case "less_than":
		return actual < first
	case "less_or_equal":
		return actual <= first
	default:
		return false
	}
}

func validationMessage(rule DataValidation, fallback string) string {
	if rule.HelpText != "" {
		return rule.HelpText
	}
	return fallback
}

func defaultValidationMessage(rule DataValidation) string {
	switch rule.RuleType {
	case "list":
		return "목록에 있는 값을 선택해야 합니다."
	case "checkbox":
		return "체크 상태를 나타내는 두 값 중 하나여야 합니다."
	case "number":
		return "숫자 검증 조건을 만족하지 않습니다."
	case "date":
		return "날짜 검증 조건을 만족하지 않습니다."
	case "custom_formula":
		return "사용자 지정 수식 조건을 만족하지 않습니다."
	default:
		return "데이터 검증 조건을 만족하지 않습니다."
	}
}

// mustDecodeValidationValue is used where the value has already been checked,
// so a decoding failure would be a programming error rather than bad input.
func mustDecodeValidationValue(raw json.RawMessage) any {
	value, _ := decodeValidationValue(raw)
	return value
}

func decodeValidationValue(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func validationScalar(value any) bool {
	switch value.(type) {
	case string, bool, json.Number, float64:
		return true
	default:
		return false
	}
}

func validationBlank(value any) bool {
	text, ok := value.(string)
	return value == nil || ok && text == ""
}

func validationNumberRaw(raw json.RawMessage) (float64, bool) {
	value, err := decodeValidationValue(raw)
	if err != nil {
		return 0, false
	}
	return validationNumber(value)
}
func validationNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func validationDateRaw(raw json.RawMessage) (time.Time, bool) {
	value, err := decodeValidationValue(raw)
	if err != nil {
		return time.Time{}, false
	}
	return validationDate(value)
}
func validationDate(value any) (time.Time, bool) {
	if number, ok := validationNumber(value); ok {
		if number < 0 || number > 2_958_465 {
			return time.Time{}, false
		}
		return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).Add(time.Duration(number * float64(24*time.Hour))), true
	}
	text, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func validationTruthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed != 0
	case json.Number:
		number, _ := typed.Float64()
		return number != 0
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func validationFormulaCells(cells map[cellKey]Cell) map[string]any {
	result := make(map[string]any, len(cells))
	for key, cell := range cells {
		value, _ := decodeValidationValue(cell.Value)
		result[cellrange.Address(key.row, key.column)] = value
	}
	return result
}

func validationCanonical(value any) string {
	encoded, _ := json.Marshal(value)
	return fmt.Sprintf("%T:%s", value, encoded)
}
func validationText(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func cloneValidationOptions(options []ValidationOption) []ValidationOption {
	result := make([]ValidationOption, len(options))
	for index, option := range options {
		result[index] = option
		result[index].Value = cloneJSON(option.Value)
	}
	return result
}

func cloneDataValidation(rule DataValidation) DataValidation {
	rule.Options = cloneValidationOptions(rule.Options)
	rule.Value = cloneJSON(rule.Value)
	rule.Value2 = cloneJSON(rule.Value2)
	return rule
}
