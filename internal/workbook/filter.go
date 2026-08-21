package workbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"kanpic/pkg/cellrange"
	"time"

	"kanpic/pkg/identity"
)

const (
	MaxFilterRows    = 100_000
	MaxFilterColumns = 100
	MaxFilterCells   = 1_000_000
)

var filterOperators = map[string]struct{}{
	"values": {}, "equals": {}, "not_equals": {}, "contains": {}, "not_contains": {},
	"starts_with": {}, "ends_with": {}, "greater_than": {}, "greater_or_equal": {},
	"less_than": {}, "less_or_equal": {}, "is_blank": {}, "is_not_blank": {},
	"background_color": {}, "text_color": {},
}

// filterViewFromInput builds the filter view a create request describes. Both
// repositories call it so a new field cannot reach one and not the other.
func filterViewFromInput(sheetID, actorID string, now time.Time, input CreateFilterViewInput) (FilterView, cellrange.Range, error) {
	return NormalizeFilterView(FilterView{
		ID: identity.New(), SheetID: sheetID, ActorID: actorID, CreateKey: input.IdempotencyKey,
		Name: input.Name, Range: input.Range, HeaderRows: input.HeaderRows,
		Criteria: cloneFilterCriteria(input.Criteria), Active: input.Active, CreatedAt: now, UpdatedAt: now,
	})
}

func NormalizeFilterView(view FilterView) (FilterView, cellrange.Range, error) {
	view.Name = strings.TrimSpace(view.Name)
	view.Range = strings.ToUpper(strings.TrimSpace(view.Range))
	if view.Name == "" || len([]rune(view.Name)) > 128 {
		return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: filter view name must contain 1 to 128 characters", ErrInvalid)
	}
	selected, err := cellrange.Parse(view.Range)
	if err != nil {
		return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: invalid filter range", ErrInvalid)
	}
	rows := selected.End.Row - selected.Start.Row + 1
	columns := selected.End.Column - selected.Start.Column + 1
	if rows < 2 || rows > MaxFilterRows || columns > MaxFilterColumns || rows > MaxFilterCells/columns || view.HeaderRows < 0 || view.HeaderRows >= rows {
		return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: filter range supports up to %d cells, %d rows, %d columns, and valid header rows", ErrInvalid, MaxFilterCells, MaxFilterRows, MaxFilterColumns)
	}
	seen := make(map[int]bool, len(view.Criteria))
	for index := range view.Criteria {
		criterion := &view.Criteria[index]
		criterion.Operator = strings.ToLower(strings.TrimSpace(criterion.Operator))
		criterion.Color = strings.ToLower(strings.TrimSpace(criterion.Color))
		if criterion.Column < selected.Start.Column || criterion.Column > selected.End.Column || seen[criterion.Column] {
			return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: filter criterion columns must be unique and inside the range", ErrInvalid)
		}
		seen[criterion.Column] = true
		if _, ok := filterOperators[criterion.Operator]; !ok {
			return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: unsupported filter operator %q", ErrInvalid, criterion.Operator)
		}
		switch criterion.Operator {
		case "values":
			if len(criterion.Values) == 0 || len(criterion.Values) > 1_000 {
				return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: values filter requires 1 to 1000 values", ErrInvalid)
			}
			for _, value := range criterion.Values {
				if _, err := decodeFilterValue(value); err != nil {
					return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: invalid filter value", ErrInvalid)
				}
			}
		case "equals", "not_equals", "contains", "not_contains", "starts_with", "ends_with", "greater_than", "greater_or_equal", "less_than", "less_or_equal":
			if len(bytes.TrimSpace(criterion.Value)) == 0 {
				return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: filter operator %s requires value", ErrInvalid, criterion.Operator)
			}
			if _, err := decodeFilterValue(criterion.Value); err != nil {
				return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: invalid filter value", ErrInvalid)
			}
		case "background_color", "text_color":
			if !validHexColor(criterion.Color) {
				return FilterView{}, cellrange.Range{}, fmt.Errorf("%w: color filter requires a #RRGGBB color", ErrInvalid)
			}
		}
	}
	return view, selected, nil
}

func EvaluateFilter(view FilterView, cells []Cell) (FilterResult, error) {
	normalized, selected, err := NormalizeFilterView(view)
	if err != nil {
		return FilterResult{}, err
	}
	byCoordinate := make(map[string]Cell, len(cells))
	for _, cell := range cells {
		if selected.Contains(cell.Row, cell.Column) {
			byCoordinate[coordinateKey(cell.Row, cell.Column)] = cell
		}
	}
	dataStart := selected.Start.Row + normalized.HeaderRows
	result := FilterResult{FilterViewID: normalized.ID, Range: normalized.Range, HiddenRows: make([]int, 0), TotalCount: selected.End.Row - dataStart + 1}
	for row := dataStart; row <= selected.End.Row; row++ {
		matches := true
		for _, criterion := range normalized.Criteria {
			if !matchesFilterCriterion(byCoordinate[coordinateKey(row, criterion.Column)], criterion) {
				matches = false
				break
			}
		}
		if matches {
			result.VisibleCount++
		} else {
			result.HiddenRows = append(result.HiddenRows, row)
		}
	}
	result.HiddenCount = len(result.HiddenRows)
	return result, nil
}

func matchesFilterCriterion(cell Cell, criterion FilterCriterion) bool {
	actual, _ := decodeFilterValue(cell.Value)
	switch criterion.Operator {
	case "is_blank":
		return actual == nil
	case "is_not_blank":
		return actual != nil
	case "background_color", "text_color":
		var style map[string]any
		_ = json.Unmarshal(cell.Style, &style)
		key := "background"
		if criterion.Operator == "text_color" {
			key = "color"
		}
		color, _ := style[key].(string)
		return strings.EqualFold(color, criterion.Color)
	case "values":
		for _, raw := range criterion.Values {
			expected, _ := decodeFilterValue(raw)
			if compareFilterValues(actual, expected, criterion.CaseSensitive) == 0 {
				return true
			}
		}
		return false
	}
	expected, _ := decodeFilterValue(criterion.Value)
	comparison := compareFilterValues(actual, expected, criterion.CaseSensitive)
	switch criterion.Operator {
	case "equals":
		return comparison == 0
	case "not_equals":
		return comparison != 0
	case "greater_than":
		return comparison > 0
	case "greater_or_equal":
		return comparison >= 0
	case "less_than":
		return comparison < 0
	case "less_or_equal":
		return comparison <= 0
	}
	actualText, expectedText := filterText(actual), filterText(expected)
	if !criterion.CaseSensitive {
		actualText, expectedText = strings.ToLower(actualText), strings.ToLower(expectedText)
	}
	switch criterion.Operator {
	case "contains":
		return strings.Contains(actualText, expectedText)
	case "not_contains":
		return !strings.Contains(actualText, expectedText)
	case "starts_with":
		return strings.HasPrefix(actualText, expectedText)
	case "ends_with":
		return strings.HasSuffix(actualText, expectedText)
	}
	return true
}

func decodeFilterValue(raw json.RawMessage) (any, error) {
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

func compareFilterValues(left, right any, caseSensitive bool) int {
	if left == nil || right == nil {
		if left == nil && right == nil {
			return 0
		}
		if left == nil {
			return -1
		}
		return 1
	}
	leftNumber, leftNumeric := filterNumber(left)
	rightNumber, rightNumeric := filterNumber(right)
	if leftNumeric && rightNumeric {
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
		return 0
	}
	leftText, rightText := filterText(left), filterText(right)
	if !caseSensitive {
		leftText, rightText = strings.ToLower(leftText), strings.ToLower(rightText)
	}
	return strings.Compare(leftText, rightText)
}

func filterNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		return number, err == nil
	case float64:
		return typed, true
	default:
		return 0, false
	}
}

func filterText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
