package presentation

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// MaxAnalysisCells bounds what one deck reads. A range larger than this is a
// database export rather than something to present, and a deck made of two
// thousand rows is not a deck.
const MaxAnalysisCells = 20_000

// MaxChartRows is how many categories a bar chart stays readable at. Past this
// the range is presented as a table instead of a picture nobody can read.
const MaxChartRows = 12

// Analyze reads the cells of a range and works out what it is.
func Analyze(source SourceRef, selected cellrange.Range, cells []workbook.Cell) Analysis {
	analysis := Analysis{Source: source, Dimension: -1, Shape: ShapeEmpty, Columns: []Column{}, Measures: []int{}, Insights: []Insight{}}
	rows, columns := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
	if rows < 1 || columns < 1 || rows*columns > MaxAnalysisCells {
		return analysis
	}
	grid := make([][]Value, rows)
	for index := range grid {
		grid[index] = make([]Value, columns)
		for column := range grid[index] {
			grid[index][column] = Value{Blank: true}
		}
	}
	for _, cell := range cells {
		row, column := cell.Row-selected.Start.Row, cell.Column-selected.Start.Column
		if row < 0 || row >= rows || column < 0 || column >= columns {
			continue
		}
		grid[row][column] = readValue(cell.Value)
	}
	// 완전히 빈 행과 열은 선택에 딸려 들어온 여백이다. 그대로 두면 표에 빈
	// 줄이 생기고 차트에 이름 없는 막대가 선다.
	grid = trimBlankRows(grid)
	grid = trimBlankColumns(grid)
	if len(grid) == 0 || len(grid[0]) == 0 {
		return analysis
	}
	analysis.HasHeader = looksLikeHeader(grid)
	body := grid
	header := make([]string, len(grid[0]))
	if analysis.HasHeader {
		for index, value := range grid[0] {
			header[index] = value.Text
		}
		body = grid[1:]
	}
	analysis.RowCount = len(body)
	for index := range header {
		column := Column{Index: index, Name: strings.TrimSpace(header[index])}
		for _, row := range body {
			column.Values = append(column.Values, row[index])
		}
		column.Kind = classifyColumn(column.Values)
		analysis.Columns = append(analysis.Columns, column)
	}
	assignRoles(&analysis)
	decideShape(&analysis)
	analysis.Insights = findInsights(analysis)
	sort.SliceStable(analysis.Insights, func(first, second int) bool {
		return insightRank(analysis.Insights[first].Kind) < insightRank(analysis.Insights[second].Kind)
	})
	analysis.Headline = writeHeadline(analysis)
	return analysis
}

func readValue(raw json.RawMessage) Value {
	if len(raw) == 0 {
		return Value{Blank: true}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Value{Blank: true}
	}
	switch typed := decoded.(type) {
	case nil:
		return Value{Blank: true}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return Value{Blank: true}
		}
		return Value{Text: formatNumber(typed), Number: typed, IsNum: true}
	case bool:
		if typed {
			return Value{Text: "TRUE"}
		}
		return Value{Text: "FALSE"}
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return Value{Blank: true}
		}
		// 축 이름이 먼저다. "1월" 에서 단위를 떼면 숫자 1이 되지만 그것은
		// 처리량이 아니라 시간이고, 숫자로 읽으면 추이 차트의 가로축이
		// 세로축으로 올라간다.
		if looksLikeDate(text) {
			return Value{Text: text}
		}
		if number, ok := parseNumberText(text); ok {
			return Value{Text: text, Number: number, IsNum: true}
		}
		return Value{Text: text}
	default:
		return Value{Blank: true}
	}
}

// parseNumberText reads the number out of what a person typed. "120억" and
// "+15%" are numbers wearing a unit, and a deck that cannot see the number in
// them draws no chart at all.
func parseNumberText(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	cleaned := strings.NewReplacer(",", "", " ", "", " ", "").Replace(trimmed)
	cleaned = strings.TrimSuffix(cleaned, "%")
	// 단위는 겹쳐 붙는다 — "18개월", "1,200억원". 한 번만 떼면 남은 글자
	// 때문에 숫자로 읽히지 않으므로 더 뗄 것이 없을 때까지 돈다.
	for {
		shortened := cleaned
		for _, unit := range []string{"원", "억", "만", "천", "개", "건", "명", "pt", "p", "회", "일", "월", "년", "차", "위"} {
			shortened = strings.TrimSuffix(shortened, unit)
		}
		if shortened == cleaned {
			break
		}
		cleaned = shortened
	}
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "+" || cleaned == "-" {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.TrimPrefix(cleaned, "+"), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func formatNumber(value float64) string {
	if value == math.Trunc(value) && math.Abs(value) < 1e15 {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// looksLikeHeader decides whether the first row names the columns. It says yes
// when the first row is words and the rows under it are not: a row of numbers
// on top of numbers is data, however it is styled.
func looksLikeHeader(grid [][]Value) bool {
	if len(grid) < 2 {
		return false
	}
	first := grid[0]
	firstText, firstFilled := 0, 0
	for _, value := range first {
		if value.Blank {
			continue
		}
		firstFilled++
		if !value.IsNum {
			firstText++
		}
	}
	if firstFilled == 0 || firstText != firstFilled {
		return false
	}
	// 아래에 숫자가 하나도 없으면 전부 글자인 표이고, 첫 줄만 머리글이라고
	// 볼 근거가 없다. 그럴 때는 첫 줄도 자료로 둔다.
	for _, row := range grid[1:] {
		for _, value := range row {
			if value.IsNum {
				return true
			}
		}
	}
	return false
}

func classifyColumn(values []Value) string {
	filled, numbers, percents, dates := 0, 0, 0, 0
	for _, value := range values {
		if value.Blank {
			continue
		}
		filled++
		if value.IsNum {
			numbers++
			if strings.HasSuffix(strings.TrimSpace(value.Text), "%") {
				percents++
			}
			continue
		}
		if looksLikeDate(value.Text) {
			dates++
		}
	}
	switch {
	case filled == 0:
		return ColumnEmpty
	case percents*2 >= filled:
		return ColumnPercent
	case numbers*2 > filled:
		return ColumnNumber
	case dates*2 >= filled:
		return ColumnDate
	default:
		return ColumnText
	}
}

var dateLayouts = []string{"2006-01-02", "2006/01/02", "2006.01.02", "2006-01", "2006/01", "2006.01", "20060102"}

func looksLikeDate(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, layout := range dateLayouts {
		if _, err := time.Parse(layout, trimmed); err == nil {
			return true
		}
	}
	// 1월, 2026년, 3분기, Q3 같은 축 이름도 시간이다. 다만 범위를 지켜야
	// 한다 — "18개월" 은 기간을 재는 숫자이지 달력의 한 칸이 아니다.
	for _, unit := range []struct {
		suffix    string
		low, high float64
	}{{"월", 1, 12}, {"분기", 1, 4}, {"년", 1900, 2200}} {
		if !strings.HasSuffix(trimmed, unit.suffix) {
			continue
		}
		head := strings.TrimSuffix(trimmed, unit.suffix)
		if head == "" || strings.IndexFunc(head, func(letter rune) bool { return letter < '0' || letter > '9' }) >= 0 {
			continue
		}
		number, err := strconv.ParseFloat(head, 64)
		if err == nil && number >= unit.low && number <= unit.high {
			return true
		}
	}
	if len(trimmed) == 2 && (trimmed[0] == 'Q' || trimmed[0] == 'q') && trimmed[1] >= '1' && trimmed[1] <= '4' {
		return true
	}
	return false
}

// assignRoles names what each column is for. The names people write are the
// best evidence available and they are worth reading: 전년대비 and 달성률 are
// both percentages and belong on different parts of a slide.
func assignRoles(analysis *Analysis) {
	for index := range analysis.Columns {
		column := &analysis.Columns[index]
		name := strings.ToLower(column.Name)
		switch {
		case column.Kind == ColumnText || column.Kind == ColumnDate:
			column.Role = RoleDimension
		case containsAny(name, "전년", "증감", "대비", "성장", "change", "growth", "delta", "yoy"):
			column.Role = RoleChange
		case containsAny(name, "달성", "목표", "target", "attainment", "진척", "progress"):
			column.Role = RoleAttainment
		case containsAny(name, "비중", "구성비", "점유", "share", "ratio", "mix"):
			column.Role = RoleShare
		case column.Kind == ColumnPercent:
			column.Role = RoleChange
		default:
			column.Role = RoleMeasure
		}
	}
	// 차원은 이름을 담은 첫 열이다. 날짜 열이 있으면 그쪽이 축이다.
	for index, column := range analysis.Columns {
		if column.Kind == ColumnDate {
			analysis.Dimension = index
			break
		}
	}
	if analysis.Dimension < 0 {
		for index, column := range analysis.Columns {
			if column.Kind == ColumnText {
				analysis.Dimension = index
				break
			}
		}
	}
	for index, column := range analysis.Columns {
		if index == analysis.Dimension || column.Kind == ColumnEmpty {
			continue
		}
		if column.Kind == ColumnNumber || column.Kind == ColumnPercent {
			analysis.Measures = append(analysis.Measures, index)
		}
	}
}

func decideShape(analysis *Analysis) {
	switch {
	case analysis.RowCount == 0 || len(analysis.Columns) == 0:
		analysis.Shape, analysis.Chart = ShapeEmpty, ChartNone
	case len(analysis.Measures) == 0:
		analysis.Shape, analysis.Chart = ShapeTable, ChartNone
	case analysis.Dimension < 0:
		// 이름 열이 없는 숫자 뭉치는 지표 몇 개이거나 그냥 표다.
		if analysis.RowCount*len(analysis.Measures) <= 4 {
			analysis.Shape, analysis.Chart = ShapeFigures, ChartNone
		} else {
			analysis.Shape, analysis.Chart = ShapeTable, ChartNone
		}
	case analysis.Columns[analysis.Dimension].Kind == ColumnDate:
		analysis.Shape, analysis.Chart = ShapeSeries, ChartLine
	case analysis.RowCount > MaxChartRows:
		analysis.Shape, analysis.Chart = ShapeTable, ChartNone
	default:
		analysis.Shape = ShapeCategories
		analysis.Chart = ChartBars
		primary := analysis.Columns[analysis.Measures[0]]
		if primary.Role == RoleShare || sumsToWhole(primary) {
			analysis.Chart = ChartShare
		} else if analysis.RowCount == 2 {
			analysis.Chart = ChartComparison
		}
	}
}

// sumsToWhole spots a column of parts of a whole. Percentages that add up to a
// hundred are a composition, and a composition is not a ranking.
func sumsToWhole(column Column) bool {
	if column.Kind != ColumnPercent {
		return false
	}
	total, counted := 0.0, 0
	for _, value := range column.Values {
		if !value.IsNum {
			continue
		}
		total += value.Number
		counted++
	}
	return counted >= 2 && math.Abs(total-100) <= 1.5
}

func findInsights(analysis Analysis) []Insight {
	insights := []Insight{}
	if analysis.Dimension < 0 || len(analysis.Measures) == 0 || analysis.RowCount == 0 {
		return insights
	}
	names := analysis.Columns[analysis.Dimension].Values
	label := func(row int) string {
		if row < len(names) && !names[row].Blank {
			return names[row].Text
		}
		return ""
	}
	primary := analysis.Columns[analysis.Measures[0]]
	if top, ok := extreme(primary, true); ok {
		insights = append(insights, Insight{Kind: InsightTop, Label: primary.Name, Value: primary.Values[top].Text, Detail: label(top), Number: primary.Values[top].Number})
	}
	if bottom, ok := extreme(primary, false); ok && len(primary.Values) > 1 {
		insights = append(insights, Insight{Kind: InsightBottom, Label: primary.Name, Value: primary.Values[bottom].Text, Detail: label(bottom), Number: primary.Values[bottom].Number})
	}
	if total, counted := sum(primary); counted > 1 && primary.Kind == ColumnNumber {
		// 합계는 부분이 입고 있던 단위를 그대로 입어야 한다. 120억과 95억을
		// 더해 놓고 "325" 라고만 쓰면 무엇이 325인지 슬라이드가 말하지 않는다.
		insights = append(insights, Insight{Kind: InsightTotal, Label: primary.Name, Value: formatNumber(total) + commonUnit(primary), Number: total})
	}
	for _, index := range analysis.Measures {
		column := analysis.Columns[index]
		switch column.Role {
		case RoleChange:
			if best, ok := extreme(column, true); ok {
				insights = append(insights, Insight{Kind: InsightGrowth, Label: column.Name, Value: column.Values[best].Text, Detail: label(best), Number: column.Values[best].Number})
			}
		case RoleAttainment:
			// 목표 아래로 떨어진 것이 이 슬라이드의 요점이다. 다 넘겼으면
			// 가장 낮은 것도 좋은 소식이므로 말할 거리가 없다.
			if worst, ok := extreme(column, false); ok && column.Values[worst].Number < 100 {
				insights = append(insights, Insight{Kind: InsightShort, Label: column.Name, Value: column.Values[worst].Text, Detail: label(worst), Number: column.Values[worst].Number})
			}
		}
	}
	return insights
}

// commonUnit is the suffix every value in a column wears, when they all wear
// the same one. Mixed units mean the column is not one measurement and a total
// of it would be wrong anyway, so nothing is added.
func commonUnit(column Column) string {
	unit, found := "", false
	for _, value := range column.Values {
		if !value.IsNum || value.Blank {
			continue
		}
		suffix := strings.TrimLeft(strings.TrimSpace(value.Text), "+-0123456789.,")
		if !found {
			unit, found = suffix, true
			continue
		}
		if suffix != unit {
			return ""
		}
	}
	if !found {
		return ""
	}
	return unit
}

// insightRank orders what the range has to say by how much it matters on a
// slide. A target that was missed leads; a total is context. Discovery order is
// the order the columns happen to sit in, which is not the same thing.
func insightRank(kind string) int {
	switch kind {
	case InsightShort:
		return 0
	case InsightTop:
		return 1
	case InsightGrowth:
		return 2
	case InsightBottom:
		return 3
	case InsightTotal:
		return 4
	default:
		return 5
	}
}

func extreme(column Column, highest bool) (int, bool) {
	found, at := false, 0
	for index, value := range column.Values {
		if !value.IsNum {
			continue
		}
		if !found || (highest && value.Number > column.Values[at].Number) || (!highest && value.Number < column.Values[at].Number) {
			found, at = true, index
		}
	}
	return at, found
}

func sum(column Column) (float64, int) {
	total, counted := 0.0, 0
	for _, value := range column.Values {
		if value.IsNum {
			total += value.Number
			counted++
		}
	}
	return total, counted
}

// writeHeadline says what the range shows in one line. It is assembled from
// the insights rather than written, so it is always true of the numbers even
// when no AI provider is configured.
func writeHeadline(analysis Analysis) string {
	var short, top *Insight
	for index := range analysis.Insights {
		switch analysis.Insights[index].Kind {
		case InsightShort:
			if short == nil {
				short = &analysis.Insights[index]
			}
		case InsightTop:
			if top == nil {
				top = &analysis.Insights[index]
			}
		}
	}
	switch {
	case short != nil && short.Detail != "":
		return short.Detail + "의 " + short.Label + "이 " + short.Value + "로 목표에 못 미칩니다."
	case top != nil && top.Detail != "":
		return top.Detail + "이(가) " + top.Label + " " + top.Value + "로 가장 높습니다."
	case analysis.RowCount > 0:
		return strconv.Itoa(analysis.RowCount) + "개 항목을 정리했습니다."
	default:
		return ""
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func trimBlankRows(grid [][]Value) [][]Value {
	kept := make([][]Value, 0, len(grid))
	for _, row := range grid {
		for _, value := range row {
			if !value.Blank {
				kept = append(kept, row)
				break
			}
		}
	}
	return kept
}

func trimBlankColumns(grid [][]Value) [][]Value {
	if len(grid) == 0 {
		return grid
	}
	keep := make([]int, 0, len(grid[0]))
	for column := range grid[0] {
		for _, row := range grid {
			if !row[column].Blank {
				keep = append(keep, column)
				break
			}
		}
	}
	if len(keep) == len(grid[0]) {
		return grid
	}
	trimmed := make([][]Value, len(grid))
	for index, row := range grid {
		next := make([]Value, 0, len(keep))
		for _, column := range keep {
			next = append(next, row[column])
		}
		trimmed[index] = next
	}
	return trimmed
}
