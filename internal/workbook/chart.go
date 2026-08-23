package workbook

import (
	"context"
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
	MaxChartsPerWorkbook = 100
	MaxChartSourceCells  = 10_000
)

var chartTypes = map[string]struct{}{"bar": {}, "line": {}, "area": {}, "pie": {}, "scatter": {}, "histogram": {},
	"stacked_bar": {}, "stacked_area": {}, "combo": {}}
var chartLegendPositions = map[string]struct{}{"none": {}, "top": {}, "right": {}, "bottom": {}, "left": {}}

type ChartPosition struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// UnmarshalJSON rounds fractional coordinates. Pointer positions in a browser
// are fractional on zoomed or high density displays, and a dragged chart must
// not fail to save because of it.
func (p *ChartPosition) UnmarshalJSON(data []byte) error {
	var raw struct {
		X      *float64 `json:"x"`
		Y      *float64 `json:"y"`
		Width  *float64 `json:"width"`
		Height *float64 `json:"height"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	round := func(value *float64, target *int) {
		if value == nil {
			return
		}
		*target = int(math.Round(*value))
	}
	round(raw.X, &p.X)
	round(raw.Y, &p.Y)
	round(raw.Width, &p.Width)
	round(raw.Height, &p.Height)
	return nil
}

type Chart struct {
	ID                string `json:"id"`
	WorkbookID        string `json:"workbook_id"`
	WorkbookVersion   int64  `json:"workbook_version"`
	SheetID           string `json:"sheet_id"`
	SourceSheetID     string `json:"source_sheet_id,omitempty"`
	CreateKey         string `json:"-"`
	Type              string `json:"type"`
	Title             string `json:"title"`
	SourceRange       string `json:"source_range"`
	FirstRowHeaders   bool   `json:"first_row_headers"`
	FirstColumnLabels bool   `json:"first_column_labels"`
	LegendPosition    string `json:"legend_position"`
	XAxisTitle        string `json:"x_axis_title,omitempty"`
	YAxisTitle        string `json:"y_axis_title,omitempty"`
	// SecondaryAxis puts a combination chart's line series on its own scale,
	// which is the only way a ratio and an amount share one picture.
	SecondaryAxis bool          `json:"secondary_axis"`
	Position      ChartPosition `json:"position"`
	Revision      int64         `json:"revision"`
	CreatedBy     string        `json:"created_by"`
	UpdatedBy     string        `json:"updated_by"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type CreateChartInput struct {
	IdempotencyKey    string         `json:"idempotency_key"`
	SheetID           string         `json:"sheet_id"`
	SourceSheetID     string         `json:"source_sheet_id"`
	Type              string         `json:"type"`
	Title             string         `json:"title,omitempty"`
	SourceRange       string         `json:"source_range"`
	FirstRowHeaders   *bool          `json:"first_row_headers,omitempty"`
	FirstColumnLabels *bool          `json:"first_column_labels,omitempty"`
	LegendPosition    string         `json:"legend_position,omitempty"`
	XAxisTitle        string         `json:"x_axis_title,omitempty"`
	YAxisTitle        string         `json:"y_axis_title,omitempty"`
	SecondaryAxis     *bool          `json:"secondary_axis,omitempty"`
	Position          *ChartPosition `json:"position,omitempty"`
}

type UpdateChartInput struct {
	SheetID           *string        `json:"sheet_id,omitempty"`
	SourceSheetID     *string        `json:"source_sheet_id,omitempty"`
	Type              *string        `json:"type,omitempty"`
	Title             *string        `json:"title,omitempty"`
	SourceRange       *string        `json:"source_range,omitempty"`
	FirstRowHeaders   *bool          `json:"first_row_headers,omitempty"`
	FirstColumnLabels *bool          `json:"first_column_labels,omitempty"`
	LegendPosition    *string        `json:"legend_position,omitempty"`
	XAxisTitle        *string        `json:"x_axis_title,omitempty"`
	SecondaryAxis     *bool          `json:"secondary_axis,omitempty"`
	YAxisTitle        *string        `json:"y_axis_title,omitempty"`
	Position          *ChartPosition `json:"position,omitempty"`
	ExpectedRevision  *int64         `json:"expected_revision,omitempty"`
}

type ChartPoint struct {
	Category string   `json:"category"`
	Value    *float64 `json:"value"`
	X        *float64 `json:"x,omitempty"`
}

type ChartSeries struct {
	Name   string       `json:"name"`
	Points []ChartPoint `json:"points"`
}

type ChartData struct {
	Chart           Chart         `json:"chart"`
	WorkbookVersion int64         `json:"workbook_version"`
	Series          []ChartSeries `json:"series"`
	Warning         string        `json:"warning,omitempty"`
}

// chartFromInput builds the chart a create request describes. Both repositories
// call it so a new field cannot reach one storage path and not the other:
// that mistake has shipped twice, and it looks like data silently reverting.
// importedChartInput 은 파일에서 읽은 차트를 만들기 요청과 같은 모습으로
// 옮긴다. 요청과 같은 길을 지나야 파일에서 온 차트라고 해서 규칙을 비켜
// 가지 않는다.
func importedChartInput(imported ImportChart, sheetID string, index int) (CreateChartInput, bool) {
	if strings.TrimSpace(imported.SourceRange) == "" {
		return CreateChartInput{}, false
	}
	if _, found := chartTypes[imported.Type]; !found {
		return CreateChartInput{}, false
	}
	// 그림이 놓여 있던 자리는 그리기 관계를 따라가야 알 수 있다. 여기서는
	// 자리 대신 **겹치지 않는 것** 을 지킨다. 모두 같은 자리에 두면 두
	// 번째 차트부터는 첫 번째 뒤에 숨어, 가져오지 못한 것처럼 보인다.
	position := defaultChartPosition()
	position.Y += index * (position.Height + chartImportGap)
	return CreateChartInput{
		IdempotencyKey: fmt.Sprintf("import-chart-%s-%d", sheetID, index),
		SheetID:        sheetID,
		SourceSheetID:  sheetID,
		Type:           imported.Type,
		Title:          imported.Title,
		SourceRange:    imported.SourceRange,
		Position:       &position,
	}, true
}

func chartFromInput(workbookID, key, actor string, input CreateChartInput) (Chart, error) {
	headers, labels := true, true
	if input.FirstRowHeaders != nil {
		headers = *input.FirstRowHeaders
	}
	if input.FirstColumnLabels != nil {
		labels = *input.FirstColumnLabels
	}
	position := defaultChartPosition()
	if input.Position != nil {
		position = *input.Position
	}
	return normalizeChart(Chart{
		WorkbookID: workbookID, SheetID: input.SheetID, SourceSheetID: input.SourceSheetID, CreateKey: key,
		Type: input.Type, Title: input.Title, SourceRange: input.SourceRange,
		FirstRowHeaders: headers, FirstColumnLabels: labels, LegendPosition: input.LegendPosition,
		XAxisTitle: input.XAxisTitle, YAxisTitle: input.YAxisTitle,
		SecondaryAxis: input.SecondaryAxis != nil && *input.SecondaryAxis,
		Position:      position, CreatedBy: actor, UpdatedBy: actor,
	}, false)
}

// chartImportGap 은 가져온 차트를 아래로 늘어놓을 때 사이에 두는 여백이다.
const chartImportGap = 24

func defaultChartPosition() ChartPosition {
	return ChartPosition{X: 24, Y: 24, Width: 560, Height: 320}
}

func normalizeChart(item Chart, allowBrokenReference bool) (Chart, error) {
	item.SheetID = strings.TrimSpace(item.SheetID)
	item.SourceSheetID = strings.TrimSpace(item.SourceSheetID)
	item.Type = strings.ToLower(strings.TrimSpace(item.Type))
	item.Title = strings.TrimSpace(item.Title)
	item.SourceRange = strings.ToUpper(strings.TrimSpace(item.SourceRange))
	item.LegendPosition = strings.ToLower(strings.TrimSpace(item.LegendPosition))
	item.XAxisTitle = strings.TrimSpace(item.XAxisTitle)
	item.YAxisTitle = strings.TrimSpace(item.YAxisTitle)
	if item.Type != "combo" {
		item.SecondaryAxis = false
	}
	if item.SheetID == "" {
		return Chart{}, fmt.Errorf("%w: sheet_id is required", ErrInvalid)
	}
	if item.SourceSheetID == "" && !(allowBrokenReference && item.SourceRange == "#REF!") {
		return Chart{}, fmt.Errorf("%w: source_sheet_id is required", ErrInvalid)
	}
	if _, found := chartTypes[item.Type]; !found {
		return Chart{}, fmt.Errorf("%w: chart type must be bar, line, area, pie, scatter, histogram, stacked_bar, stacked_area, or combo", ErrInvalid)
	}
	if len([]rune(item.Title)) > 200 || len([]rune(item.XAxisTitle)) > 100 || len([]rune(item.YAxisTitle)) > 100 {
		return Chart{}, fmt.Errorf("%w: chart title or axis title is too long", ErrInvalid)
	}
	if item.LegendPosition == "" {
		item.LegendPosition = "right"
	}
	if _, found := chartLegendPositions[item.LegendPosition]; !found {
		return Chart{}, fmt.Errorf("%w: invalid legend position", ErrInvalid)
	}
	if item.SourceRange != "#REF!" {
		selected, err := cellrange.Parse(item.SourceRange)
		if err != nil {
			return Chart{}, fmt.Errorf("%w: source_range must be a valid A1 range", ErrInvalid)
		}
		count := int64(selected.End.Row-selected.Start.Row+1) * int64(selected.End.Column-selected.Start.Column+1)
		if count > MaxChartSourceCells {
			return Chart{}, fmt.Errorf("%w: chart source may contain at most %d cells", ErrInvalid, MaxChartSourceCells)
		}
		item.SourceRange = cellrange.Address(selected.Start.Row, selected.Start.Column) + ":" + cellrange.Address(selected.End.Row, selected.End.Column)
	}
	if item.Position == (ChartPosition{}) {
		item.Position = defaultChartPosition()
	}
	if item.Position.X < 0 || item.Position.Y < 0 || item.Position.X > 100_000 || item.Position.Y > 100_000 || item.Position.Width < 240 || item.Position.Width > 1600 || item.Position.Height < 160 || item.Position.Height > 1200 {
		return Chart{}, fmt.Errorf("%w: invalid chart position or size", ErrInvalid)
	}
	return item, nil
}

func cloneChart(item Chart) Chart { return item }

func transformChartForStructure(item Chart, targetSheetID string, input StructuralMutation, actor string, now time.Time) (Chart, error) {
	if item.SourceSheetID != targetSheetID || item.SourceRange == "#REF!" {
		return item, nil
	}
	original := item.SourceRange
	transformed, exists, err := formula.TransformRangeAddress(item.SourceRange, formulaStructuralChange(input, "", ""))
	if err != nil {
		return Chart{}, fmt.Errorf("%w: chart source range exceeds spreadsheet bounds", ErrInvalid)
	}
	if !exists {
		transformed = "#REF!"
	}
	if transformed != original {
		item.SourceRange, item.Revision, item.UpdatedBy, item.UpdatedAt = transformed, item.Revision+1, actor, now
	}
	return item, nil
}

func (r *MemoryRepository) CreateChart(_ context.Context, workbookID, actor string, input CreateChartInput) (Chart, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return Chart{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return Chart{}, ErrNotFound
	}
	for _, item := range r.charts {
		if item.WorkbookID == workbookID && item.CreatedBy == actor && item.CreateKey == key {
			item.WorkbookVersion = state.workbook.Version
			return cloneChart(item), nil
		}
	}
	if len(r.chartsForWorkbookLocked(workbookID, "")) >= MaxChartsPerWorkbook {
		return Chart{}, fmt.Errorf("%w: a workbook may contain at most %d charts", ErrInvalid, MaxChartsPerWorkbook)
	}
	item, err := chartFromInput(workbookID, key, actor, input)
	if err != nil {
		return Chart{}, err
	}
	if err := validateChartSheets(state, item); err != nil {
		return Chart{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	r.bump(state)
	item.WorkbookVersion = state.workbook.Version
	r.charts[item.ID] = item
	return cloneChart(item), nil
}

func (r *MemoryRepository) ListCharts(_ context.Context, workbookID, sheetID string) ([]Chart, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, found := r.workbooks[workbookID]
	if !found {
		return nil, ErrNotFound
	}
	items := r.chartsForWorkbookLocked(workbookID, strings.TrimSpace(sheetID))
	for index := range items {
		items[index].WorkbookVersion = state.workbook.Version
	}
	return items, nil
}

func (r *MemoryRepository) GetChart(_ context.Context, id string) (Chart, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.charts[id]
	if !found {
		return Chart{}, ErrNotFound
	}
	state, found := r.workbooks[item.WorkbookID]
	if !found {
		return Chart{}, ErrNotFound
	}
	item.WorkbookVersion = state.workbook.Version
	return cloneChart(item), nil
}

func (r *MemoryRepository) GetChartData(_ context.Context, id string) (ChartData, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, found := r.charts[id]
	if !found {
		return ChartData{}, ErrNotFound
	}
	state, found := r.workbooks[item.WorkbookID]
	if !found {
		return ChartData{}, ErrNotFound
	}
	item.WorkbookVersion = state.workbook.Version
	if item.SourceRange == "#REF!" || item.SourceSheetID == "" {
		return ChartData{Chart: cloneChart(item), WorkbookVersion: state.workbook.Version, Series: []ChartSeries{}, Warning: "#REF!"}, nil
	}
	selected, _ := cellrange.Parse(item.SourceRange)
	cells := make([]Cell, 0)
	for _, cell := range state.cells[item.SourceSheetID] {
		if selected.Contains(cell.Row, cell.Column) {
			cells = append(cells, cloneCell(cell))
		}
	}
	return buildChartData(item, state.workbook.Version, cells)
}

func (r *MemoryRepository) UpdateChart(_ context.Context, id, actor string, input UpdateChartInput) (Chart, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, found := r.charts[id]
	if !found {
		return Chart{}, ErrNotFound
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return Chart{}, ErrRevision
	}
	state, found := r.workbooks[current.WorkbookID]
	if !found {
		return Chart{}, ErrNotFound
	}
	updated := current
	if input.SheetID != nil {
		updated.SheetID = *input.SheetID
	}
	if input.SourceSheetID != nil {
		updated.SourceSheetID = *input.SourceSheetID
	}
	if input.Type != nil {
		updated.Type = *input.Type
	}
	if input.Title != nil {
		updated.Title = *input.Title
	}
	if input.SourceRange != nil {
		updated.SourceRange = *input.SourceRange
	}
	if input.FirstRowHeaders != nil {
		updated.FirstRowHeaders = *input.FirstRowHeaders
	}
	if input.FirstColumnLabels != nil {
		updated.FirstColumnLabels = *input.FirstColumnLabels
	}
	if input.LegendPosition != nil {
		updated.LegendPosition = *input.LegendPosition
	}
	if input.XAxisTitle != nil {
		updated.XAxisTitle = *input.XAxisTitle
	}
	if input.YAxisTitle != nil {
		updated.YAxisTitle = *input.YAxisTitle
	}
	if input.SecondaryAxis != nil {
		updated.SecondaryAxis = *input.SecondaryAxis
	}
	if input.Position != nil {
		updated.Position = *input.Position
	}
	var err error
	updated, err = normalizeChart(updated, false)
	if err != nil {
		return Chart{}, err
	}
	if err := validateChartSheets(state, updated); err != nil {
		return Chart{}, err
	}
	updated.Revision, updated.UpdatedBy, updated.UpdatedAt = current.Revision+1, actor, r.now()
	r.bump(state)
	updated.WorkbookVersion = state.workbook.Version
	r.charts[id] = updated
	return cloneChart(updated), nil
}

func (r *MemoryRepository) DeleteChart(_ context.Context, id, _ string, expectedRevision *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, found := r.charts[id]
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
	delete(r.charts, id)
	r.bump(state)
	return nil
}

func validateChartSheets(state *workbookState, item Chart) error {
	if _, found := state.sheets[item.SheetID]; !found {
		return fmt.Errorf("%w: chart sheet does not belong to the workbook", ErrInvalid)
	}
	if _, found := state.sheets[item.SourceSheetID]; !found {
		return fmt.Errorf("%w: chart source sheet does not belong to the workbook", ErrInvalid)
	}
	return nil
}

func (r *MemoryRepository) chartsForWorkbookLocked(workbookID, sheetID string) []Chart {
	items := make([]Chart, 0)
	for _, item := range r.charts {
		if item.WorkbookID == workbookID && (sheetID == "" || item.SheetID == sheetID) {
			items = append(items, cloneChart(item))
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

func buildChartData(chart Chart, version int64, cells []Cell) (ChartData, error) {
	selected, err := cellrange.Parse(chart.SourceRange)
	if err != nil {
		return ChartData{Chart: chart, WorkbookVersion: version, Series: []ChartSeries{}, Warning: "#REF!"}, nil
	}
	rows, columns := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
	matrix := make([][]any, rows)
	for row := range matrix {
		matrix[row] = make([]any, columns)
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
	if chart.Type == "histogram" {
		return buildHistogramData(chart, version, matrix), nil
	}
	if chart.Type == "scatter" {
		return buildScatterData(chart, version, matrix), nil
	}
	rowStart, columnStart := 0, 0
	if chart.FirstRowHeaders && rows > 1 {
		rowStart = 1
	}
	if chart.FirstColumnLabels && columns > 1 {
		columnStart = 1
	}
	if rowStart >= rows || columnStart >= columns {
		return ChartData{Chart: chart, WorkbookVersion: version, Series: []ChartSeries{}, Warning: "표시할 숫자 데이터가 없습니다."}, nil
	}
	series := make([]ChartSeries, 0, columns-columnStart)
	for column := columnStart; column < columns; column++ {
		name := "계열 " + strconv.Itoa(column-columnStart+1)
		if chart.FirstRowHeaders {
			if header := displayChartValue(matrix[0][column]); header != "" {
				name = header
			}
		}
		points := make([]ChartPoint, 0, rows-rowStart)
		for row := rowStart; row < rows; row++ {
			category := strconv.Itoa(row - rowStart + 1)
			if chart.FirstColumnLabels {
				if label := displayChartValue(matrix[row][0]); label != "" {
					category = label
				}
			}
			value, ok := numericChartValue(matrix[row][column])
			var pointer *float64
			if ok {
				copy := value
				pointer = &copy
			}
			point := ChartPoint{Category: category, Value: pointer}
			points = append(points, point)
		}
		series = append(series, ChartSeries{Name: name, Points: points})
		if chart.Type == "pie" {
			break
		}
	}
	return ChartData{Chart: chart, WorkbookVersion: version, Series: series}, nil
}

func buildScatterData(chart Chart, version int64, matrix [][]any) ChartData {
	rowStart := 0
	if chart.FirstRowHeaders && len(matrix) > 1 {
		rowStart = 1
	}
	if len(matrix) <= rowStart || len(matrix[0]) < 2 {
		return ChartData{Chart: chart, WorkbookVersion: version, Series: []ChartSeries{}, Warning: "분산형 차트에는 X열과 하나 이상의 Y열이 필요합니다."}
	}
	series := make([]ChartSeries, 0, len(matrix[0])-1)
	for column := 1; column < len(matrix[0]); column++ {
		name := "계열 " + strconv.Itoa(column)
		if chart.FirstRowHeaders {
			if header := displayChartValue(matrix[0][column]); header != "" {
				name = header
			}
		}
		points := make([]ChartPoint, 0, len(matrix)-rowStart)
		for row := rowStart; row < len(matrix); row++ {
			x, hasX := numericChartValue(matrix[row][0])
			y, hasY := numericChartValue(matrix[row][column])
			if !hasX || !hasY {
				continue
			}
			xCopy, yCopy := x, y
			points = append(points, ChartPoint{Category: displayChartValue(matrix[row][0]), X: &xCopy, Value: &yCopy})
		}
		series = append(series, ChartSeries{Name: name, Points: points})
	}
	return ChartData{Chart: chart, WorkbookVersion: version, Series: series}
}

func buildHistogramData(chart Chart, version int64, matrix [][]any) ChartData {
	values := make([]float64, 0)
	for rowIndex, row := range matrix {
		for columnIndex, value := range row {
			if chart.FirstRowHeaders && rowIndex == 0 {
				continue
			}
			if chart.FirstColumnLabels && columnIndex == 0 && len(row) > 1 {
				continue
			}
			if number, ok := numericChartValue(value); ok {
				values = append(values, number)
			}
		}
	}
	if len(values) == 0 {
		return ChartData{Chart: chart, WorkbookVersion: version, Series: []ChartSeries{}, Warning: "표시할 숫자 데이터가 없습니다."}
	}
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	bins := int(math.Ceil(math.Sqrt(float64(len(values)))))
	if bins < 1 {
		bins = 1
	}
	if bins > 30 {
		bins = 30
	}
	width := (maximum - minimum) / float64(bins)
	if width == 0 {
		width = 1
	}
	counts := make([]float64, bins)
	for _, value := range values {
		index := int((value - minimum) / width)
		if index >= bins {
			index = bins - 1
		}
		counts[index]++
	}
	points := make([]ChartPoint, bins)
	for index, count := range counts {
		start, end := minimum+float64(index)*width, minimum+float64(index+1)*width
		copy := count
		points[index] = ChartPoint{Category: fmt.Sprintf("%.4g–%.4g", start, end), Value: &copy}
	}
	return ChartData{Chart: chart, WorkbookVersion: version, Series: []ChartSeries{{Name: "빈도", Points: points}}}
}

func displayChartValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		data, _ := json.Marshal(typed)
		return string(data)
	}
}

func numericChartValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(typed, ",", "")), 64)
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	default:
		return 0, false
	}
}
