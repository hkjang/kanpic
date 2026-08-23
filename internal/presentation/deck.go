package presentation

import (
	"strconv"
	"strings"
)

// MaxTableRows is how many rows of a range reach a table slide. A slide is not
// a report: past this the deck says how many were left out rather than printing
// a page nobody can read.
const MaxTableRows = 10

// DeckOptions are the choices a person makes before a deck is built. Everything
// else is derived from the numbers.
type DeckOptions struct {
	Title    string
	Language string
	Audience string
	// IncludeTable keeps the full table slide. It is on by default because the
	// person who selected the range usually wants the numbers in the deck too.
	IncludeTable bool
}

// Build turns what the range means into what the deck says.
//
// The shape of the deck follows the shape of the data rather than a fixed
// template: a time series gets a trend slide, a composition gets a share slide,
// and a range with nothing to plot does not get an empty chart.
func Build(analysis Analysis, options DeckOptions) Deck {
	title := strings.TrimSpace(options.Title)
	if title == "" {
		title = defaultTitle(analysis)
	}
	language := strings.TrimSpace(options.Language)
	if language == "" {
		language = "ko"
	}
	deck := Deck{Title: title, Subtitle: analysis.Headline, Language: language, Source: analysis.Source, Slides: []Slide{}}
	deck.Slides = append(deck.Slides, Slide{
		Kind: SlideCover, Title: title, Lead: analysis.Headline,
		Notes: sourceNote(analysis.Source),
	})
	if analysis.Shape == ShapeEmpty {
		// 빈 범위로도 덱은 만들어진다. 표지 한 장에 왜 비었는지 적는 편이
		// 오류를 돌려주는 것보다 낫다 — 사람은 범위를 다시 고르면 된다.
		deck.Slides = append(deck.Slides, Slide{Kind: SlideContent, Title: "내용 없음", Lead: "선택한 범위에 표시할 값이 없습니다."})
		return deck
	}
	if tiles := kpiRows(analysis); len(tiles) > 0 {
		deck.Slides = append(deck.Slides, Slide{
			Kind: SlideContent, Title: "핵심 지표", Lead: "",
			Component: &Component{Kind: "kpi", Caption: "주요 지표", Rows: tiles},
		})
	}
	if chart := chartComponent(analysis); chart != nil {
		deck.Slides = append(deck.Slides, Slide{Kind: SlideContent, Title: chartTitle(analysis), Component: chart})
	}
	if options.IncludeTable {
		if table, omitted := tableComponent(analysis); table != nil {
			slide := Slide{Kind: SlideContent, Title: "상세 내역", Component: table}
			if omitted > 0 {
				slide.Lead = strconv.Itoa(analysis.RowCount) + "개 중 " + strconv.Itoa(len(table.Rows)-1) + "개를 실었습니다."
				slide.Notes = "나머지 " + strconv.Itoa(omitted) + "개 항목은 원본 범위에 있습니다."
			}
			deck.Slides = append(deck.Slides, slide)
		}
	}
	if points := closingPoints(analysis); len(points) > 0 {
		// 마무리 레이아웃에는 본문 자리가 없는 디자인이 많다. 할 말이 있는
		// 마지막 장은 본문으로 두어야 요점이 글자로 남는다.
		deck.Slides = append(deck.Slides, Slide{Kind: SlideContent, Title: "주요 시사점", Bullets: points})
	}
	return deck
}

func defaultTitle(analysis Analysis) string {
	if analysis.Dimension >= 0 {
		if name := strings.TrimSpace(analysis.Columns[analysis.Dimension].Name); name != "" {
			if len(analysis.Measures) > 0 {
				if measure := strings.TrimSpace(analysis.Columns[analysis.Measures[0]].Name); measure != "" {
					return name + "별 " + measure
				}
			}
			return name + " 현황"
		}
	}
	if analysis.Source.SheetName != "" {
		return analysis.Source.SheetName
	}
	return "데이터 요약"
}

// sourceNote puts the range on the cover's speaker notes. Somebody asked in a
// meeting where a number came from and nobody could say; the deck can.
func sourceNote(source SourceRef) string {
	parts := []string{}
	if source.SheetName != "" {
		parts = append(parts, source.SheetName)
	}
	if source.Range != "" {
		parts = append(parts, source.Range)
	}
	if len(parts) == 0 {
		return ""
	}
	note := "출처: " + strings.Join(parts, " ")
	if source.Version > 0 {
		note += " (워크북 v" + strconv.FormatInt(source.Version, 10) + ")"
	}
	return note
}

// kpiRows turns insights into tiles. At most four: a KPI row is read at a
// glance and a glance holds four things.
func kpiRows(analysis Analysis) []Row {
	rows := []Row{}
	for _, insight := range analysis.Insights {
		label := insightLabel(insight)
		if label == "" {
			continue
		}
		rows = append(rows, Row{Label: label, Fields: []string{insight.Value, insight.Detail}})
		if len(rows) == 4 {
			break
		}
	}
	if len(rows) == 0 && analysis.Shape == ShapeFigures {
		// 이름 없는 숫자 뭉치는 그 자체가 지표다.
		for _, index := range analysis.Measures {
			column := analysis.Columns[index]
			for _, value := range column.Values {
				if value.Blank {
					continue
				}
				rows = append(rows, Row{Label: column.Name, Fields: []string{value.Text}})
				if len(rows) == 4 {
					return rows
				}
			}
		}
	}
	return rows
}

func insightLabel(insight Insight) string {
	switch insight.Kind {
	case InsightTop:
		return "최고 " + insight.Label
	case InsightBottom:
		return "최저 " + insight.Label
	case InsightTotal:
		return "합계 " + insight.Label
	case InsightAverage:
		return "평균 " + insight.Label
	case InsightGrowth:
		return "최대 " + insight.Label
	case InsightShort:
		return "목표 미달"
	default:
		return insight.Label
	}
}

func chartComponent(analysis Analysis) *Component {
	if analysis.Chart == ChartNone || analysis.Dimension < 0 {
		return nil
	}
	// 단계와 이정표는 숫자를 그리지 않는다. 재는 것이 없어도 순서는 보여 줄
	// 것이 있다.
	if analysis.Chart == ChartSteps || analysis.Chart == ChartTimeline {
		return orderedComponent(analysis)
	}
	if len(analysis.Measures) == 0 {
		return nil
	}
	names := analysis.Columns[analysis.Dimension].Values
	primary := analysis.Columns[analysis.Measures[0]]
	caption := strings.TrimSpace(primary.Name)
	if caption == "" {
		caption = strings.TrimSpace(analysis.Columns[analysis.Dimension].Name)
	}
	if analysis.Chart == ChartLine {
		// 추이는 한 줄이 한 계열이고 첫 줄이 시간 축이다.
		axis := make([]string, 0, len(names))
		for _, value := range names {
			if !value.Blank {
				axis = append(axis, value.Text)
			}
		}
		rows := []Row{{Label: axisLabel(analysis), Fields: []string{strings.Join(axis, ", ")}}}
		for _, index := range analysis.Measures {
			column := analysis.Columns[index]
			series := make([]string, 0, len(column.Values))
			for position, value := range column.Values {
				if position < len(names) && names[position].Blank {
					continue
				}
				series = append(series, value.Text)
			}
			rows = append(rows, Row{Label: column.Name, Fields: []string{strings.Join(series, ", ")}})
		}
		return &Component{Kind: "line", Caption: caption, Rows: rows}
	}
	kind := "bars"
	switch analysis.Chart {
	case ChartShare:
		kind = "share"
	case ChartComparison:
		kind = "comparison"
	}
	rows := make([]Row, 0, len(names))
	for position, name := range names {
		if name.Blank || position >= len(primary.Values) {
			continue
		}
		value := primary.Values[position]
		if value.Blank {
			continue
		}
		rows = append(rows, Row{Label: name.Text, Fields: []string{value.Text}})
	}
	if len(rows) == 0 {
		return nil
	}
	return &Component{Kind: kind, Caption: caption, Rows: rows}
}

// orderedComponent draws the rows in the order they were written: a process as
// steps, dated rows as a timeline. The label is what names the row and the
// detail is what it says; a date beside a stage is kept with the detail rather
// than replacing it.
func orderedComponent(analysis Analysis) *Component {
	kind := "steps"
	if analysis.Chart == ChartTimeline {
		kind = "timeline"
	}
	labels := analysis.Columns[analysis.Dimension].Values
	detail, date := -1, -1
	for index, column := range analysis.Columns {
		if index == analysis.Dimension {
			continue
		}
		if column.Role == RoleDetail && detail < 0 {
			detail = index
		}
		if column.Kind == ColumnDate && date < 0 {
			date = index
		}
	}
	if detail < 0 {
		return nil
	}
	rows := make([]Row, 0, len(labels))
	for position, label := range labels {
		if label.Blank {
			continue
		}
		text := ""
		if position < len(analysis.Columns[detail].Values) {
			text = analysis.Columns[detail].Values[position].Text
		}
		if date >= 0 && position < len(analysis.Columns[date].Values) {
			if at := analysis.Columns[date].Values[position]; !at.Blank {
				text = strings.TrimSpace(text + " (" + at.Text + ")")
			}
		}
		rows = append(rows, Row{Label: label.Text, Fields: []string{text}})
	}
	if len(rows) == 0 {
		return nil
	}
	caption := strings.TrimSpace(analysis.Columns[analysis.Dimension].Name)
	return &Component{Kind: kind, Caption: caption, Rows: rows}
}

func axisLabel(analysis Analysis) string {
	if name := strings.TrimSpace(analysis.Columns[analysis.Dimension].Name); name != "" {
		return name
	}
	return "구분"
}

func chartTitle(analysis Analysis) string {
	if analysis.Chart == ChartSteps {
		return "진행 순서"
	}
	if analysis.Chart == ChartTimeline {
		return "주요 일정"
	}
	dimension := strings.TrimSpace(analysis.Columns[analysis.Dimension].Name)
	measure := ""
	if len(analysis.Measures) > 0 {
		measure = strings.TrimSpace(analysis.Columns[analysis.Measures[0]].Name)
	}
	switch {
	case analysis.Chart == ChartLine && measure != "":
		return measure + " 추이"
	case dimension != "" && measure != "":
		return dimension + "별 " + measure
	case measure != "":
		return measure
	default:
		return "현황"
	}
}

// tableComponent lays the range out as it stands. It returns the number of rows
// that did not fit so the slide can say so rather than quietly dropping them.
func tableComponent(analysis Analysis) (*Component, int) {
	if analysis.RowCount == 0 || len(analysis.Columns) == 0 {
		return nil, 0
	}
	header := Row{}
	for index, column := range analysis.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			name = "열 " + strconv.Itoa(index+1)
		}
		if index == 0 {
			header.Label = name
			continue
		}
		header.Fields = append(header.Fields, name)
	}
	rows := []Row{header}
	shown := analysis.RowCount
	if shown > MaxTableRows {
		shown = MaxTableRows
	}
	for position := 0; position < shown; position++ {
		row := Row{}
		for index, column := range analysis.Columns {
			text := ""
			if position < len(column.Values) && !column.Values[position].Blank {
				text = column.Values[position].Text
			}
			if index == 0 {
				row.Label = text
				continue
			}
			row.Fields = append(row.Fields, text)
		}
		rows = append(rows, row)
	}
	return &Component{Kind: "table", Caption: "", Rows: rows}, analysis.RowCount - shown
}

// closingPoints writes what the numbers suggest looking into. They are
// questions raised by the data, not conclusions the data cannot support.
func closingPoints(analysis Analysis) []string {
	points := []string{}
	for _, insight := range analysis.Insights {
		switch insight.Kind {
		case InsightShort:
			if insight.Detail != "" {
				points = append(points, insight.Detail+"의 "+insight.Label+" "+insight.Value+" 원인 점검")
			}
		case InsightGrowth:
			if insight.Detail != "" {
				points = append(points, insight.Detail+"의 "+insight.Label+" "+insight.Value+" 성장 요인 분석")
			}
		case InsightBottom:
			if insight.Detail != "" && len(points) < 2 {
				points = append(points, insight.Detail+"의 "+insight.Label+" 개선 방안 검토")
			}
		}
		if len(points) == 3 {
			break
		}
	}
	return points
}
