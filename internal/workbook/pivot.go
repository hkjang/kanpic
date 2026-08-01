package workbook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const (
	MaxPivotsPerWorkbook = 50
	MaxPivotSourceCells  = 100_000
	MaxPivotResultCells  = 20_000
)

type PivotCustomGroup struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type PivotDimension struct {
	Column       int                `json:"column"`
	Name         string             `json:"name,omitempty"`
	Group        string             `json:"group,omitempty"`
	Interval     float64            `json:"interval,omitempty"`
	CustomGroups []PivotCustomGroup `json:"custom_groups,omitempty"`
}

type PivotValueField struct {
	Column      int    `json:"column"`
	Name        string `json:"name,omitempty"`
	Aggregation string `json:"aggregation"`
}

type PivotFilter struct {
	Column   int      `json:"column"`
	Operator string   `json:"operator"`
	Value    string   `json:"value,omitempty"`
	Values   []string `json:"values,omitempty"`
}

type PivotCalculatedField struct {
	Name    string `json:"name"`
	Formula string `json:"formula"`
}

type Pivot struct {
	ID               string                 `json:"id"`
	WorkbookID       string                 `json:"workbook_id"`
	WorkbookVersion  int64                  `json:"workbook_version"`
	SheetID          string                 `json:"sheet_id"`
	SourceSheetID    string                 `json:"source_sheet_id,omitempty"`
	CreateKey        string                 `json:"-"`
	Name             string                 `json:"name"`
	SourceRange      string                 `json:"source_range"`
	FirstRowHeaders  bool                   `json:"first_row_headers"`
	Rows             []PivotDimension       `json:"rows"`
	Columns          []PivotDimension       `json:"columns"`
	Values           []PivotValueField      `json:"values"`
	Filters          []PivotFilter          `json:"filters,omitempty"`
	CalculatedFields []PivotCalculatedField `json:"calculated_fields,omitempty"`
	RefreshMode      string                 `json:"refresh_mode"`
	SourceVersion    int64                  `json:"source_version"`
	LastRefreshedAt  *time.Time             `json:"last_refreshed_at,omitempty"`
	Revision         int64                  `json:"revision"`
	CreatedBy        string                 `json:"created_by"`
	UpdatedBy        string                 `json:"updated_by"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

type CreatePivotInput struct {
	IdempotencyKey   string                 `json:"idempotency_key"`
	SheetID          string                 `json:"sheet_id"`
	SourceSheetID    string                 `json:"source_sheet_id"`
	Name             string                 `json:"name"`
	SourceRange      string                 `json:"source_range"`
	FirstRowHeaders  *bool                  `json:"first_row_headers,omitempty"`
	Rows             []PivotDimension       `json:"rows,omitempty"`
	Columns          []PivotDimension       `json:"columns,omitempty"`
	Values           []PivotValueField      `json:"values"`
	Filters          []PivotFilter          `json:"filters,omitempty"`
	CalculatedFields []PivotCalculatedField `json:"calculated_fields,omitempty"`
	RefreshMode      string                 `json:"refresh_mode,omitempty"`
}

type UpdatePivotInput struct {
	SheetID          *string                 `json:"sheet_id,omitempty"`
	SourceSheetID    *string                 `json:"source_sheet_id,omitempty"`
	Name             *string                 `json:"name,omitempty"`
	SourceRange      *string                 `json:"source_range,omitempty"`
	FirstRowHeaders  *bool                   `json:"first_row_headers,omitempty"`
	Rows             *[]PivotDimension       `json:"rows,omitempty"`
	Columns          *[]PivotDimension       `json:"columns,omitempty"`
	Values           *[]PivotValueField      `json:"values,omitempty"`
	Filters          *[]PivotFilter          `json:"filters,omitempty"`
	CalculatedFields *[]PivotCalculatedField `json:"calculated_fields,omitempty"`
	RefreshMode      *string                 `json:"refresh_mode,omitempty"`
	ExpectedRevision *int64                  `json:"expected_revision,omitempty"`
}

type PivotResultColumn struct {
	Key        string   `json:"key"`
	Labels     []string `json:"labels"`
	ValueName  string   `json:"value_name"`
	ValueIndex int      `json:"value_index,omitempty"`
	Calculated bool     `json:"calculated,omitempty"`
}

type PivotResultRow struct {
	Key         string   `json:"key"`
	Labels      []string `json:"labels"`
	Values      []any    `json:"values"`
	SourceCount int      `json:"source_count"`
}

type PivotData struct {
	Pivot           Pivot               `json:"pivot"`
	WorkbookVersion int64               `json:"workbook_version"`
	SourceVersion   int64               `json:"source_version"`
	SourceHeaders   []string            `json:"source_headers"`
	Columns         []PivotResultColumn `json:"columns"`
	Rows            []PivotResultRow    `json:"rows"`
	GrandTotals     []any               `json:"grand_totals"`
	SourceRowCount  int                 `json:"source_row_count"`
	GeneratedAt     time.Time           `json:"generated_at"`
	Cached          bool                `json:"cached"`
	Warning         string              `json:"warning,omitempty"`
}

type PivotDrilldownInput struct {
	RowKey    string `json:"row_key,omitempty"`
	ColumnKey string `json:"column_key,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type PivotDrilldownRow struct {
	SourceRow int   `json:"source_row"`
	Values    []any `json:"values"`
}

type PivotDrilldownResult struct {
	PivotID     string              `json:"pivot_id"`
	Headers     []string            `json:"headers"`
	Rows        []PivotDrilldownRow `json:"rows"`
	Total       int                 `json:"total"`
	NextOffset  *int                `json:"next_offset,omitempty"`
	SourceRange string              `json:"source_range"`
}

var pivotGroups = map[string]struct{}{"none": {}, "year": {}, "quarter": {}, "month": {}, "day": {}, "number": {}, "custom": {}}
var pivotAggregations = map[string]struct{}{"sum": {}, "average": {}, "count": {}, "min": {}, "max": {}}
var pivotFilterOperators = map[string]struct{}{"equals": {}, "not_equals": {}, "contains": {}, "greater_than": {}, "greater_or_equal": {}, "less_than": {}, "less_or_equal": {}, "in": {}, "is_blank": {}, "not_blank": {}}

func normalizePivot(item Pivot, allowBroken bool) (Pivot, error) {
	item.SheetID, item.SourceSheetID = strings.TrimSpace(item.SheetID), strings.TrimSpace(item.SourceSheetID)
	item.Name, item.SourceRange = strings.TrimSpace(item.Name), strings.ToUpper(strings.TrimSpace(item.SourceRange))
	item.RefreshMode = strings.ToLower(strings.TrimSpace(item.RefreshMode))
	if item.SheetID == "" || item.Name == "" {
		return Pivot{}, fmt.Errorf("%w: sheet_id and pivot name are required", ErrInvalid)
	}
	if len([]rune(item.Name)) > 200 {
		return Pivot{}, fmt.Errorf("%w: pivot name is too long", ErrInvalid)
	}
	if item.SourceSheetID == "" && !(allowBroken && item.SourceRange == "#REF!") {
		return Pivot{}, fmt.Errorf("%w: source_sheet_id is required", ErrInvalid)
	}
	width := 0
	if item.SourceRange != "#REF!" {
		selected, err := cellrange.Parse(item.SourceRange)
		if err != nil {
			return Pivot{}, fmt.Errorf("%w: source_range must be a valid A1 range", ErrInvalid)
		}
		count := int64(selected.End.Row-selected.Start.Row+1) * int64(selected.End.Column-selected.Start.Column+1)
		if count > MaxPivotSourceCells {
			return Pivot{}, fmt.Errorf("%w: pivot source may contain at most %d cells", ErrInvalid, MaxPivotSourceCells)
		}
		width = selected.End.Column - selected.Start.Column + 1
		item.SourceRange = cellrange.Address(selected.Start.Row, selected.Start.Column) + ":" + cellrange.Address(selected.End.Row, selected.End.Column)
	}
	if item.RefreshMode == "" {
		item.RefreshMode = "auto"
	}
	if item.RefreshMode != "auto" && item.RefreshMode != "manual" {
		return Pivot{}, fmt.Errorf("%w: refresh_mode must be auto or manual", ErrInvalid)
	}
	if len(item.Rows)+len(item.Columns) > 6 || len(item.Values) == 0 || len(item.Values) > 10 || len(item.Filters) > 10 || len(item.CalculatedFields) > 5 {
		return Pivot{}, fmt.Errorf("%w: pivot supports 6 dimensions, 10 values, 10 filters, and 5 calculated fields", ErrInvalid)
	}
	seenDimensions := map[int]struct{}{}
	for index := range item.Rows {
		if err := normalizePivotDimension(&item.Rows[index], width, seenDimensions); err != nil {
			return Pivot{}, err
		}
	}
	for index := range item.Columns {
		if err := normalizePivotDimension(&item.Columns[index], width, seenDimensions); err != nil {
			return Pivot{}, err
		}
	}
	for index := range item.Values {
		field := &item.Values[index]
		field.Name, field.Aggregation = strings.TrimSpace(field.Name), strings.ToLower(strings.TrimSpace(field.Aggregation))
		if field.Column < 1 || (width > 0 && field.Column > width) {
			return Pivot{}, fmt.Errorf("%w: pivot value column is outside source range", ErrInvalid)
		}
		if _, found := pivotAggregations[field.Aggregation]; !found {
			return Pivot{}, fmt.Errorf("%w: aggregation must be sum, average, count, min, or max", ErrInvalid)
		}
		if field.Name == "" {
			field.Name = strings.ToUpper(field.Aggregation) + " 열 " + strconv.Itoa(field.Column)
		}
		if len([]rune(field.Name)) > 100 {
			return Pivot{}, fmt.Errorf("%w: pivot value name is too long", ErrInvalid)
		}
	}
	for index := range item.Filters {
		filter := &item.Filters[index]
		filter.Operator, filter.Value = strings.ToLower(strings.TrimSpace(filter.Operator)), strings.TrimSpace(filter.Value)
		if filter.Column < 1 || (width > 0 && filter.Column > width) {
			return Pivot{}, fmt.Errorf("%w: pivot filter column is outside source range", ErrInvalid)
		}
		if _, found := pivotFilterOperators[filter.Operator]; !found {
			return Pivot{}, fmt.Errorf("%w: invalid pivot filter operator", ErrInvalid)
		}
		for valueIndex := range filter.Values {
			filter.Values[valueIndex] = strings.TrimSpace(filter.Values[valueIndex])
		}
	}
	evaluator := formula.New()
	for index := range item.CalculatedFields {
		field := &item.CalculatedFields[index]
		field.Name, field.Formula = strings.TrimSpace(field.Name), strings.TrimSpace(field.Formula)
		if field.Name == "" || field.Formula == "" || len([]rune(field.Name)) > 100 || len(field.Formula) > 500 {
			return Pivot{}, fmt.Errorf("%w: calculated field name and formula are required", ErrInvalid)
		}
		dependencies, formulaErr := evaluator.Dependencies(field.Formula)
		if formulaErr != nil {
			return Pivot{}, fmt.Errorf("%w: invalid calculated field formula: %s", ErrInvalid, formulaErr.Code)
		}
		for _, dependency := range dependencies {
			if !validPivotValueReference(dependency, len(item.Values)) {
				return Pivot{}, fmt.Errorf("%w: calculated fields may reference V1 through V%d", ErrInvalid, len(item.Values))
			}
		}
	}
	return item, nil
}

func normalizePivotDimension(dimension *PivotDimension, width int, seen map[int]struct{}) error {
	dimension.Name, dimension.Group = strings.TrimSpace(dimension.Name), strings.ToLower(strings.TrimSpace(dimension.Group))
	if dimension.Group == "" {
		dimension.Group = "none"
	}
	if dimension.Column < 1 || (width > 0 && dimension.Column > width) {
		return fmt.Errorf("%w: pivot dimension column is outside source range", ErrInvalid)
	}
	if _, found := seen[dimension.Column]; found {
		return fmt.Errorf("%w: a column may only be used once as a row or column dimension", ErrInvalid)
	}
	seen[dimension.Column] = struct{}{}
	if _, found := pivotGroups[dimension.Group]; !found {
		return fmt.Errorf("%w: invalid pivot grouping", ErrInvalid)
	}
	if dimension.Group == "number" && (dimension.Interval <= 0 || math.IsNaN(dimension.Interval) || math.IsInf(dimension.Interval, 0)) {
		return fmt.Errorf("%w: numeric grouping requires a positive interval", ErrInvalid)
	}
	if dimension.Name == "" {
		dimension.Name = "열 " + strconv.Itoa(dimension.Column)
	}
	if len([]rune(dimension.Name)) > 100 {
		return fmt.Errorf("%w: pivot dimension name is too long", ErrInvalid)
	}
	if dimension.Group != "custom" {
		dimension.CustomGroups = nil
		return nil
	}
	if len(dimension.CustomGroups) == 0 {
		return fmt.Errorf("%w: custom grouping requires at least one group", ErrInvalid)
	}
	seenNames := map[string]struct{}{}
	seenValues := map[string]struct{}{}
	for groupIndex := range dimension.CustomGroups {
		group := &dimension.CustomGroups[groupIndex]
		group.Name = strings.TrimSpace(group.Name)
		if group.Name == "" {
			return fmt.Errorf("%w: custom group name is required", ErrInvalid)
		}
		nameKey := strings.ToLower(group.Name)
		if _, found := seenNames[nameKey]; found {
			return fmt.Errorf("%w: custom group names must be unique", ErrInvalid)
		}
		seenNames[nameKey] = struct{}{}
		if len(group.Values) == 0 {
			return fmt.Errorf("%w: custom group values are required", ErrInvalid)
		}
		for valueIndex := range group.Values {
			group.Values[valueIndex] = strings.TrimSpace(group.Values[valueIndex])
			if group.Values[valueIndex] == "" {
				return fmt.Errorf("%w: custom group values are required", ErrInvalid)
			}
			valueKey := strings.ToLower(group.Values[valueIndex])
			if _, found := seenValues[valueKey]; found {
				return fmt.Errorf("%w: custom group values must be unique", ErrInvalid)
			}
			seenValues[valueKey] = struct{}{}
		}
	}
	return nil
}

func validPivotValueReference(dependency string, count int) bool {
	dependency = strings.ToUpper(strings.TrimSpace(dependency))
	if !strings.HasPrefix(dependency, "V") {
		return false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(dependency, "V"))
	return err == nil && index >= 1 && index <= count
}

func clonePivot(item Pivot) Pivot {
	data, _ := json.Marshal(item)
	var result Pivot
	_ = json.Unmarshal(data, &result)
	result.CreateKey = item.CreateKey
	return result
}

func clonePivotData(item PivotData) PivotData {
	data, _ := json.Marshal(item)
	var result PivotData
	_ = json.Unmarshal(data, &result)
	return result
}

type pivotSourceRow struct {
	SourceRow int
	Values    []any
}

func pivotSource(chartRange string, firstRowHeaders bool, cells []Cell) ([]string, []pivotSourceRow, error) {
	selected, err := cellrange.Parse(chartRange)
	if err != nil {
		return nil, nil, err
	}
	rowCount, columnCount := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
	matrix := make([][]any, rowCount)
	for row := range matrix {
		matrix[row] = make([]any, columnCount)
	}
	for _, cell := range cells {
		if !selected.Contains(cell.Row, cell.Column) {
			continue
		}
		var value any
		if len(cell.Value) > 0 && string(cell.Value) != "null" {
			_ = json.Unmarshal(cell.Value, &value)
		}
		matrix[cell.Row-selected.Start.Row][cell.Column-selected.Start.Column] = value
	}
	headers := make([]string, columnCount)
	for column := 0; column < columnCount; column++ {
		headers[column] = "열 " + strconv.Itoa(column+1)
		if firstRowHeaders && len(matrix) > 0 {
			if value := displayChartValue(matrix[0][column]); value != "" {
				headers[column] = value
			}
		}
	}
	start := 0
	if firstRowHeaders {
		start = 1
	}
	rows := make([]pivotSourceRow, 0, len(matrix)-start)
	for row := start; row < len(matrix); row++ {
		rows = append(rows, pivotSourceRow{SourceRow: selected.Start.Row + row, Values: matrix[row]})
	}
	return headers, rows, nil
}

func groupPivotValue(value any, dimension PivotDimension) string {
	display := displayChartValue(value)
	if display == "" {
		display = "(빈 값)"
	}
	switch dimension.Group {
	case "year", "quarter", "month", "day":
		parsed, ok := parsePivotDate(value)
		if !ok {
			return "(날짜 아님) " + display
		}
		switch dimension.Group {
		case "year":
			return parsed.Format("2006")
		case "quarter":
			return fmt.Sprintf("%d Q%d", parsed.Year(), (int(parsed.Month())-1)/3+1)
		case "month":
			return parsed.Format("2006-01")
		default:
			return parsed.Format("2006-01-02")
		}
	case "number":
		number, ok := numericChartValue(value)
		if !ok {
			return "(숫자 아님) " + display
		}
		start := math.Floor(number/dimension.Interval) * dimension.Interval
		end := start + dimension.Interval
		return fmt.Sprintf("%g – %g", start, end)
	case "custom":
		for _, group := range dimension.CustomGroups {
			for _, candidate := range group.Values {
				if display == candidate {
					return group.Name
				}
			}
		}
		return "기타"
	default:
		return display
	}
}

func parsePivotDate(value any) (time.Time, bool) {
	text := strings.TrimSpace(displayChartValue(value))
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006/01/02", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func pivotGroupLabels(row pivotSourceRow, dimensions []PivotDimension) []string {
	labels := make([]string, len(dimensions))
	for index, dimension := range dimensions {
		labels[index] = groupPivotValue(row.Values[dimension.Column-1], dimension)
	}
	return labels
}

func pivotKey(labels []string) string {
	data, _ := json.Marshal(labels)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodePivotKey(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return []string{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid pivot key", ErrInvalid)
	}
	var labels []string
	if err := json.Unmarshal(data, &labels); err != nil {
		return nil, fmt.Errorf("%w: invalid pivot key", ErrInvalid)
	}
	return labels, nil
}

func pivotFilterMatches(value any, filter PivotFilter) bool {
	display := displayChartValue(value)
	left, leftNumber := numericChartValue(value)
	right, rightErr := strconv.ParseFloat(filter.Value, 64)
	compare := strings.Compare(strings.ToLower(display), strings.ToLower(filter.Value))
	if leftNumber && rightErr == nil {
		compare = 0
		if left < right {
			compare = -1
		} else if left > right {
			compare = 1
		}
	}
	switch filter.Operator {
	case "equals":
		return compare == 0
	case "not_equals":
		return compare != 0
	case "contains":
		return strings.Contains(strings.ToLower(display), strings.ToLower(filter.Value))
	case "greater_than":
		return compare > 0
	case "greater_or_equal":
		return compare >= 0
	case "less_than":
		return compare < 0
	case "less_or_equal":
		return compare <= 0
	case "in":
		for _, candidate := range filter.Values {
			if strings.EqualFold(display, candidate) {
				return true
			}
		}
		return false
	case "is_blank":
		return strings.TrimSpace(display) == ""
	case "not_blank":
		return strings.TrimSpace(display) != ""
	default:
		return false
	}
}

func filterPivotRows(rows []pivotSourceRow, filters []PivotFilter) []pivotSourceRow {
	result := make([]pivotSourceRow, 0, len(rows))
	for _, row := range rows {
		matched := true
		for _, filter := range filters {
			if !pivotFilterMatches(row.Values[filter.Column-1], filter) {
				matched = false
				break
			}
		}
		if matched {
			result = append(result, row)
		}
	}
	return result
}

type pivotAccumulator struct {
	count        int
	numericCount int
	sum          float64
	min          float64
	max          float64
}

func (accumulator *pivotAccumulator) add(value any) {
	if displayChartValue(value) != "" {
		accumulator.count++
	}
	if number, ok := numericChartValue(value); ok {
		if accumulator.numericCount == 0 || number < accumulator.min {
			accumulator.min = number
		}
		if accumulator.numericCount == 0 || number > accumulator.max {
			accumulator.max = number
		}
		accumulator.numericCount++
		accumulator.sum += number
	}
}

func (accumulator pivotAccumulator) value(aggregation string) any {
	switch aggregation {
	case "count":
		return accumulator.count
	case "sum":
		if accumulator.numericCount == 0 {
			return nil
		}
		return accumulator.sum
	case "average":
		if accumulator.numericCount == 0 {
			return nil
		}
		return accumulator.sum / float64(accumulator.numericCount)
	case "min":
		if accumulator.numericCount == 0 {
			return nil
		}
		return accumulator.min
	case "max":
		if accumulator.numericCount == 0 {
			return nil
		}
		return accumulator.max
	default:
		return nil
	}
}

func evaluatePivotCalculated(fields []PivotCalculatedField, base []any) []any {
	if len(fields) == 0 {
		return nil
	}
	cells := make(map[string]any, len(base))
	for index, value := range base {
		cells["V"+strconv.Itoa(index+1)] = value
	}
	result := make([]any, 0, len(fields))
	evaluator := formula.New()
	for _, field := range fields {
		evaluated := evaluator.Evaluate(field.Formula, cells)
		if evaluated.Error != nil {
			result = append(result, evaluated.Error.Code)
		} else {
			result = append(result, evaluated.Value)
		}
	}
	return result
}

func buildPivotData(item Pivot, workbookVersion int64, cells []Cell, generatedAt time.Time) (PivotData, error) {
	if item.SourceRange == "#REF!" || item.SourceSheetID == "" {
		return PivotData{Pivot: item, WorkbookVersion: workbookVersion, SourceVersion: workbookVersion, SourceHeaders: []string{}, Columns: []PivotResultColumn{}, Rows: []PivotResultRow{}, GrandTotals: []any{}, GeneratedAt: generatedAt, Warning: "#REF!"}, nil
	}
	headers, sourceRows, err := pivotSource(item.SourceRange, item.FirstRowHeaders, cells)
	if err != nil {
		return PivotData{}, err
	}
	sourceRows = filterPivotRows(sourceRows, item.Filters)
	type group struct {
		labels []string
		rows   []pivotSourceRow
	}
	rowGroups, columnGroups := map[string]*group{}, map[string]*group{}
	rowOrder, columnOrder := make([]string, 0), make([]string, 0)
	for _, row := range sourceRows {
		rowLabels, columnLabels := pivotGroupLabels(row, item.Rows), pivotGroupLabels(row, item.Columns)
		rowKey, columnKey := pivotKey(rowLabels), pivotKey(columnLabels)
		if rowGroups[rowKey] == nil {
			rowGroups[rowKey] = &group{labels: rowLabels}
			rowOrder = append(rowOrder, rowKey)
		}
		if columnGroups[columnKey] == nil {
			columnGroups[columnKey] = &group{labels: columnLabels}
			columnOrder = append(columnOrder, columnKey)
		}
		rowGroups[rowKey].rows = append(rowGroups[rowKey].rows, row)
		columnGroups[columnKey].rows = append(columnGroups[columnKey].rows, row)
	}
	if len(rowOrder) == 0 {
		key := pivotKey([]string{})
		rowOrder, rowGroups[key] = []string{key}, &group{labels: []string{}}
	}
	if len(columnOrder) == 0 {
		key := pivotKey([]string{})
		columnOrder, columnGroups[key] = []string{key}, &group{labels: []string{}}
	}
	columnCount := len(columnOrder) * (len(item.Values) + len(item.CalculatedFields))
	if len(rowOrder)*columnCount > MaxPivotResultCells {
		return PivotData{}, fmt.Errorf("%w: pivot result may contain at most %d cells", ErrInvalid, MaxPivotResultCells)
	}
	columns := make([]PivotResultColumn, 0, columnCount)
	for _, columnKey := range columnOrder {
		for valueIndex, value := range item.Values {
			columns = append(columns, PivotResultColumn{Key: columnKey, Labels: append([]string{}, columnGroups[columnKey].labels...), ValueName: value.Name, ValueIndex: valueIndex + 1})
		}
		for _, calculated := range item.CalculatedFields {
			columns = append(columns, PivotResultColumn{Key: columnKey, Labels: append([]string{}, columnGroups[columnKey].labels...), ValueName: calculated.Name, Calculated: true})
		}
	}
	resultRows := make([]PivotResultRow, 0, len(rowOrder))
	for _, rowKey := range rowOrder {
		values := make([]any, 0, columnCount)
		for _, columnKey := range columnOrder {
			matching := intersectPivotRows(rowGroups[rowKey].rows, columnGroups[columnKey].labels, item.Columns)
			base := aggregatePivotRows(matching, item.Values)
			values = append(values, base...)
			values = append(values, evaluatePivotCalculated(item.CalculatedFields, base)...)
		}
		resultRows = append(resultRows, PivotResultRow{Key: rowKey, Labels: append([]string{}, rowGroups[rowKey].labels...), Values: values, SourceCount: len(rowGroups[rowKey].rows)})
	}
	grandTotals := make([]any, 0, columnCount)
	for _, columnKey := range columnOrder {
		base := aggregatePivotRows(columnGroups[columnKey].rows, item.Values)
		grandTotals = append(grandTotals, base...)
		grandTotals = append(grandTotals, evaluatePivotCalculated(item.CalculatedFields, base)...)
	}
	return PivotData{Pivot: item, WorkbookVersion: workbookVersion, SourceVersion: workbookVersion, SourceHeaders: headers, Columns: columns, Rows: resultRows, GrandTotals: grandTotals, SourceRowCount: len(sourceRows), GeneratedAt: generatedAt}, nil
}

func intersectPivotRows(rows []pivotSourceRow, columnLabels []string, dimensions []PivotDimension) []pivotSourceRow {
	if len(dimensions) == 0 {
		return rows
	}
	result := make([]pivotSourceRow, 0, len(rows))
	for _, row := range rows {
		if equalStringSlices(pivotGroupLabels(row, dimensions), columnLabels) {
			result = append(result, row)
		}
	}
	return result
}

func aggregatePivotRows(rows []pivotSourceRow, fields []PivotValueField) []any {
	result := make([]any, len(fields))
	for index, field := range fields {
		var accumulator pivotAccumulator
		for _, row := range rows {
			accumulator.add(row.Values[field.Column-1])
		}
		result[index] = accumulator.value(field.Aggregation)
	}
	return result
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func buildPivotDrilldown(item Pivot, cells []Cell, input PivotDrilldownInput) (PivotDrilldownResult, error) {
	rowLabels, err := decodePivotKey(input.RowKey)
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	columnLabels, err := decodePivotKey(input.ColumnKey)
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	headers, rows, err := pivotSource(item.SourceRange, item.FirstRowHeaders, cells)
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	rows = filterPivotRows(rows, item.Filters)
	matched := make([]pivotSourceRow, 0)
	for _, row := range rows {
		if equalStringSlices(pivotGroupLabels(row, item.Rows), rowLabels) && equalStringSlices(pivotGroupLabels(row, item.Columns), columnLabels) {
			matched = append(matched, row)
		}
	}
	if input.Offset < 0 {
		return PivotDrilldownResult{}, fmt.Errorf("%w: offset cannot be negative", ErrInvalid)
	}
	if input.Limit <= 0 {
		input.Limit = 100
	}
	if input.Limit > 500 {
		return PivotDrilldownResult{}, fmt.Errorf("%w: drilldown limit cannot exceed 500", ErrInvalid)
	}
	end := input.Offset + input.Limit
	if end > len(matched) {
		end = len(matched)
	}
	if input.Offset > len(matched) {
		input.Offset = len(matched)
	}
	result := PivotDrilldownResult{PivotID: item.ID, Headers: headers, Rows: []PivotDrilldownRow{}, Total: len(matched), SourceRange: item.SourceRange}
	for _, row := range matched[input.Offset:end] {
		result.Rows = append(result.Rows, PivotDrilldownRow{SourceRow: row.SourceRow, Values: row.Values})
	}
	if end < len(matched) {
		next := end
		result.NextOffset = &next
	}
	return result, nil
}

func transformPivotForStructure(item Pivot, targetSheetID string, input StructuralMutation, actor string, now time.Time) (Pivot, error) {
	if item.SourceSheetID != targetSheetID || item.SourceRange == "#REF!" {
		return item, nil
	}
	transformed, exists, err := formula.TransformRangeAddress(item.SourceRange, formulaStructuralChange(input, "", ""))
	if err != nil {
		return Pivot{}, fmt.Errorf("%w: pivot source range exceeds spreadsheet bounds", ErrInvalid)
	}
	if !exists {
		transformed = "#REF!"
	}
	if transformed != item.SourceRange {
		item.SourceRange, item.Revision, item.UpdatedBy, item.UpdatedAt = transformed, item.Revision+1, actor, now
	}
	return item, nil
}

func validatePivotSheets(state *workbookState, item Pivot) error {
	if _, found := state.sheets[item.SheetID]; !found {
		return fmt.Errorf("%w: pivot sheet does not belong to workbook", ErrInvalid)
	}
	if item.SourceRange != "#REF!" {
		if _, found := state.sheets[item.SourceSheetID]; !found {
			return fmt.Errorf("%w: pivot source sheet does not belong to workbook", ErrInvalid)
		}
	}
	return nil
}

func (r *MemoryRepository) CreatePivot(_ context.Context, workbookID, actor string, input CreatePivotInput) (Pivot, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return Pivot{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return Pivot{}, ErrNotFound
	}
	for _, item := range r.pivots {
		if item.WorkbookID == workbookID && item.CreatedBy == actor && item.CreateKey == key {
			item.WorkbookVersion = state.workbook.Version
			return clonePivot(item), nil
		}
	}
	if len(r.pivotsForWorkbookLocked(workbookID, "")) >= MaxPivotsPerWorkbook {
		return Pivot{}, fmt.Errorf("%w: a workbook may contain at most %d pivots", ErrInvalid, MaxPivotsPerWorkbook)
	}
	headers := true
	if input.FirstRowHeaders != nil {
		headers = *input.FirstRowHeaders
	}
	item, err := normalizePivot(Pivot{WorkbookID: workbookID, SheetID: input.SheetID, SourceSheetID: input.SourceSheetID, CreateKey: key, Name: input.Name, SourceRange: input.SourceRange, FirstRowHeaders: headers, Rows: input.Rows, Columns: input.Columns, Values: input.Values, Filters: input.Filters, CalculatedFields: input.CalculatedFields, RefreshMode: input.RefreshMode, CreatedBy: actor, UpdatedBy: actor}, false)
	if err != nil {
		return Pivot{}, err
	}
	if err := validatePivotSheets(state, item); err != nil {
		return Pivot{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.pivots[item.ID] = item
	return clonePivot(item), nil
}

func (r *MemoryRepository) ListPivots(_ context.Context, workbookID, sheetID string) ([]Pivot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return nil, ErrNotFound
	}
	items := r.pivotsForWorkbookLocked(workbookID, strings.TrimSpace(sheetID))
	for index := range items {
		items[index].WorkbookVersion = state.workbook.Version
	}
	return items, nil
}

func (r *MemoryRepository) GetPivot(_ context.Context, id string) (Pivot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.pivots[id]
	if !found {
		return Pivot{}, ErrNotFound
	}
	state, found := r.workbooks[item.WorkbookID]
	if !found {
		return Pivot{}, ErrNotFound
	}
	item.WorkbookVersion = state.workbook.Version
	return clonePivot(item), nil
}

func (r *MemoryRepository) GetPivotData(_ context.Context, id string) (PivotData, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, state, cells, err := r.memoryPivotSourceLocked(id)
	if err != nil {
		return PivotData{}, err
	}
	if item.RefreshMode == "manual" {
		if cached, found := r.pivotCache[id]; found {
			cached.Pivot.WorkbookVersion, cached.WorkbookVersion, cached.Cached = state.workbook.Version, state.workbook.Version, true
			return clonePivotData(cached), nil
		}
	}
	data, err := buildPivotData(item, state.workbook.Version, cells, r.now())
	if err != nil {
		return PivotData{}, err
	}
	if item.RefreshMode == "manual" {
		now := data.GeneratedAt
		item.SourceVersion, item.LastRefreshedAt = state.workbook.Version, &now
		r.pivots[id], data.Pivot, data.Cached = item, item, true
		r.pivotCache[id] = clonePivotData(data)
	}
	return data, nil
}

func (r *MemoryRepository) RefreshPivot(_ context.Context, id, actor string) (PivotData, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, state, cells, err := r.memoryPivotSourceLocked(id)
	if err != nil {
		return PivotData{}, err
	}
	now := r.now()
	data, err := buildPivotData(item, state.workbook.Version, cells, now)
	if err != nil {
		return PivotData{}, err
	}
	item.SourceVersion, item.LastRefreshedAt, item.UpdatedBy, item.UpdatedAt = state.workbook.Version, &now, actor, now
	r.pivots[id], data.Pivot = item, item
	if item.RefreshMode == "manual" {
		data.Cached = true
		r.pivotCache[id] = clonePivotData(data)
	}
	return data, nil
}

func (r *MemoryRepository) PivotDrilldown(_ context.Context, id string, input PivotDrilldownInput) (PivotDrilldownResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, _, cells, err := r.memoryPivotSourceLocked(id)
	if err != nil {
		return PivotDrilldownResult{}, err
	}
	if item.SourceRange == "#REF!" {
		return PivotDrilldownResult{}, fmt.Errorf("%w: pivot source is unavailable", ErrInvalid)
	}
	return buildPivotDrilldown(item, cells, input)
}

func (r *MemoryRepository) UpdatePivot(_ context.Context, id, actor string, input UpdatePivotInput) (Pivot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.pivots[id]
	if !found {
		return Pivot{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return Pivot{}, ErrRevision
	}
	state, found := r.workbooks[current.WorkbookID]
	if !found {
		return Pivot{}, ErrNotFound
	}
	updated := current
	if input.SheetID != nil {
		updated.SheetID = *input.SheetID
	}
	if input.SourceSheetID != nil {
		updated.SourceSheetID = *input.SourceSheetID
	}
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.SourceRange != nil {
		updated.SourceRange = *input.SourceRange
	}
	if input.FirstRowHeaders != nil {
		updated.FirstRowHeaders = *input.FirstRowHeaders
	}
	if input.Rows != nil {
		updated.Rows = *input.Rows
	}
	if input.Columns != nil {
		updated.Columns = *input.Columns
	}
	if input.Values != nil {
		updated.Values = *input.Values
	}
	if input.Filters != nil {
		updated.Filters = *input.Filters
	}
	if input.CalculatedFields != nil {
		updated.CalculatedFields = *input.CalculatedFields
	}
	if input.RefreshMode != nil {
		updated.RefreshMode = *input.RefreshMode
	}
	updated, err := normalizePivot(updated, false)
	if err != nil {
		return Pivot{}, err
	}
	if err := validatePivotSheets(state, updated); err != nil {
		return Pivot{}, err
	}
	updated.Revision, updated.UpdatedBy, updated.UpdatedAt, updated.SourceVersion, updated.LastRefreshedAt = current.Revision+1, actor, r.now(), 0, nil
	delete(r.pivotCache, id)
	r.bump(state)
	updated.WorkbookVersion = state.workbook.Version
	r.pivots[id] = updated
	return clonePivot(updated), nil
}

func (r *MemoryRepository) DeletePivot(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, found := r.pivots[id]
	if !found {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != item.Revision {
		return ErrRevision
	}
	state, found := r.workbooks[item.WorkbookID]
	if !found {
		return ErrNotFound
	}
	delete(r.pivots, id)
	delete(r.pivotCache, id)
	r.bump(state)
	return nil
}

func (r *MemoryRepository) memoryPivotSourceLocked(id string) (Pivot, *workbookState, []Cell, error) {
	item, found := r.pivots[id]
	if !found {
		return Pivot{}, nil, nil, ErrNotFound
	}
	state, found := r.workbooks[item.WorkbookID]
	if !found {
		return Pivot{}, nil, nil, ErrNotFound
	}
	item.WorkbookVersion = state.workbook.Version
	if item.SourceRange == "#REF!" || item.SourceSheetID == "" {
		return item, state, nil, nil
	}
	selected, _ := cellrange.Parse(item.SourceRange)
	cells := make([]Cell, 0)
	for _, cell := range state.cells[item.SourceSheetID] {
		if selected.Contains(cell.Row, cell.Column) {
			cells = append(cells, cloneCell(cell))
		}
	}
	return item, state, cells, nil
}

func (r *MemoryRepository) pivotsForWorkbookLocked(workbookID, sheetID string) []Pivot {
	items := make([]Pivot, 0)
	for _, item := range r.pivots {
		if item.WorkbookID == workbookID && (sheetID == "" || item.SheetID == sheetID) {
			items = append(items, clonePivot(item))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}

func clonePivotsForWorkbook(source map[string]Pivot, workbookID string) map[string]Pivot {
	result := make(map[string]Pivot)
	for id, item := range source {
		if item.WorkbookID == workbookID {
			result[id] = clonePivot(item)
		}
	}
	return result
}

func clonePivotMap(source map[string]Pivot) map[string]Pivot {
	result := make(map[string]Pivot, len(source))
	for id, item := range source {
		result[id] = clonePivot(item)
	}
	return result
}
