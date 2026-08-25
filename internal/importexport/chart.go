package importexport

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/formula"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// A chart anchored by pixels has to land on a cell in XLSX. These are the
// default sizes kanpic draws with, which is what the stored position was
// measured against.
const (
	defaultColumnPixels = 108.0
	defaultRowPixels    = 27.0
)

var chartTypes = map[string]excelize.ChartType{
	"bar":          excelize.Col,
	"stacked_bar":  excelize.ColStacked,
	"line":         excelize.Line,
	"area":         excelize.Area,
	"stacked_area": excelize.AreaStacked,
	"pie":          excelize.Pie,
	"scatter":      excelize.Scatter,
	"combo":        excelize.Col,
}

// anchorCell converts a pixel position into the cell Excel hangs the chart on.
// The placement is approximate — Excel has no pixel anchor — but it keeps a
// chart near the data it was put beside.
func anchorCell(position workbook.ChartPosition) string {
	column := int(float64(position.X)/defaultColumnPixels) + 1
	row := int(float64(position.Y)/defaultRowPixels) + 1
	if column < 1 {
		column = 1
	}
	if row < 1 {
		row = 1
	}
	return cellrange.Address(row, column)
}

func absoluteRange(sheet string, startRow, startColumn, endRow, endColumn int) string {
	return fmt.Sprintf("'%s'!$%s$%d:$%s$%d", sheet,
		columnLetters(startColumn), startRow, columnLetters(endColumn), endRow)
}

// exportChart turns a kanpic chart into the XLSX equivalent plus, for a
// combination chart, the second chart Excel overlays on the first. A chart
// whose shape Excel has no equivalent for is skipped rather than drawn as
// something else.
func exportChart(item workbook.Chart, sourceSheet string) (*excelize.Chart, *excelize.Chart) {
	kind, known := chartTypes[item.Type]
	if !known {
		return nil, nil
	}
	selected, err := cellrange.Parse(item.SourceRange)
	if err != nil {
		return nil, nil
	}
	firstRow := selected.Start.Row
	if item.FirstRowHeaders {
		firstRow++
	}
	if firstRow > selected.End.Row {
		return nil, nil
	}
	firstValueColumn := selected.Start.Column
	categories := ""
	if item.FirstColumnLabels && selected.End.Column > selected.Start.Column {
		categories = absoluteRange(sourceSheet, firstRow, selected.Start.Column, selected.End.Row, selected.Start.Column)
		firstValueColumn++
	}
	series := make([]excelize.ChartSeries, 0, selected.End.Column-firstValueColumn+1)
	for column := firstValueColumn; column <= selected.End.Column; column++ {
		name := columnLetters(column)
		if item.FirstRowHeaders {
			name = absoluteRange(sourceSheet, selected.Start.Row, column, selected.Start.Row, column)
		}
		series = append(series, excelize.ChartSeries{
			Name:       name,
			Categories: categories,
			Values:     absoluteRange(sourceSheet, firstRow, column, selected.End.Row, column),
		})
	}
	if len(series) == 0 {
		return nil, nil
	}
	build := func(kind excelize.ChartType, members []excelize.ChartSeries) *excelize.Chart {
		chart := &excelize.Chart{
			Type:      kind,
			Series:    members,
			Dimension: excelize.ChartDimension{Width: uint(item.Position.Width), Height: uint(item.Position.Height)},
			Legend:    excelize.ChartLegend{Position: legendPosition(item.LegendPosition), ShowLegendKey: false},
			// 값 표시는 계열마다가 아니라 차트 전체의 설정이다.
			PlotArea: excelize.ChartPlotArea{ShowVal: item.DataLabels},
			XAxis:    excelize.ChartAxis{Title: excelize.ChartTitle{Paragraph: axisTitle(item.XAxisTitle)}},
			YAxis:    yAxis(item),
		}
		if strings.TrimSpace(item.Title) != "" {
			chart.Title = excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: item.Title}}}
		}
		return chart
	}
	// A combination chart draws every series but the last as columns and the
	// last one as a line, which is the shape kanpic renders.
	if item.Type == "combo" && len(series) > 1 {
		return build(excelize.Col, series[:len(series)-1]), build(excelize.Line, series[len(series)-1:])
	}
	return build(kind, series), nil
}

// yAxis 는 세로축의 이름과, 사람이 정해 둔 범위를 함께 싣는다. 범위를
// 정한 것은 뜻이 있어서이므로 엑셀에서도 그대로 보여야 한다.
func yAxis(item workbook.Chart) excelize.ChartAxis {
	axis := excelize.ChartAxis{Title: excelize.ChartTitle{Paragraph: axisTitle(item.YAxisTitle)}}
	if item.YAxisMin != nil {
		axis.Minimum = item.YAxisMin
	}
	if item.YAxisMax != nil {
		axis.Maximum = item.YAxisMax
	}
	return axis
}

func axisTitle(text string) []excelize.RichTextRun {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []excelize.RichTextRun{{Text: text}}
}

func legendPosition(position string) string {
	switch position {
	case "top":
		return "top"
	case "bottom":
		return "bottom"
	case "left":
		return "left"
	case "right":
		return "right"
	default:
		return ""
	}
}

// lambdaDefinedName 은 이름 있는 수식을 엑셀이 아는 꼴로 바꾼다. 엑셀은
// 매개변수를 가진 이름을 LAMBDA 로 적고, 파일 안에서는 _xlfn 이 붙는다.
//
//	마진율(매출, 원가) = (매출-원가)/매출
//	  -> _xlfn.LAMBDA(매출,원가,(매출-원가)/매출)
func lambdaDefinedName(item workbook.NamedFunction) (string, bool) {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item.Body), "="))
	if body == "" {
		return "", false
	}
	body = formula.ForExcel("=" + body)
	body = strings.TrimPrefix(body, "=")
	// 정의된 이름의 값에는 = 를 붙이지 않는다. 이름 범위 쪽도 그렇게 적고,
	// 파일 안의 XML 에도 = 는 들어가지 않는다.
	//
	// 매개변수가 없어도 LAMBDA 로 감싼다. 값만 적으면 엑셀에서 기준연도 로
	// 부르게 되는데 kanpic 에서는 기준연도() 다. 부르는 법이 파일을 건너며
	// 달라지면 수식을 손봐야 하고, 그러면 도로 가져올 때도 이름 있는 수식이
	// 아니라 상수가 된다.
	arguments := append(append([]string{}, item.Parameters...), body)
	return "_xlfn.LAMBDA(" + strings.Join(arguments, ",") + ")", true
}

// namedFunctionFromDefinedName 은 엑셀의 LAMBDA 정의된 이름을 다시 이름 있는
// 수식으로 읽는다. 내보낸 파일을 도로 열었을 때 이름이 사라지면 안 된다.
//
//	_xlfn.LAMBDA(매출,원가,(매출-원가)/매출)
//	  -> 마진율(매출, 원가) = (매출-원가)/매출
//
// 마지막 인자가 본문이고 그 앞이 매개변수다. 본문 안에도 쉼표가 있으므로
// 괄호와 따옴표 밖의 쉼표만 센다.
func namedFunctionFromDefinedName(name, refersTo string) (workbook.ImportNamedFunction, bool) {
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(refersTo), "="))
	for _, prefix := range []string{"_xlfn.LAMBDA(", "_xlfn._xlws.LAMBDA(", "LAMBDA("} {
		if len(text) > len(prefix) && strings.EqualFold(text[:len(prefix)], prefix) && strings.HasSuffix(text, ")") {
			text = text[len(prefix) : len(text)-1]
			parts := splitTopLevel(text)
			if len(parts) == 0 {
				return workbook.ImportNamedFunction{}, false
			}
			body := strings.TrimSpace(parts[len(parts)-1])
			if body == "" {
				return workbook.ImportNamedFunction{}, false
			}
			parameters := make([]string, 0, len(parts)-1)
			for _, parameter := range parts[:len(parts)-1] {
				parameter = strings.TrimSpace(parameter)
				if !isNameToken(parameter) {
					return workbook.ImportNamedFunction{}, false
				}
				parameters = append(parameters, parameter)
			}
			return workbook.ImportNamedFunction{Name: name, Parameters: parameters, Body: strings.TrimPrefix(formula.FromExcel("="+body), "=")}, true
		}
	}
	return workbook.ImportNamedFunction{}, false
}

// splitTopLevel 은 괄호와 따옴표 밖의 쉼표에서만 자른다. LAMBDA 의 본문이
// IF(a,b,c) 처럼 제 쉼표를 가지고 있기 때문이다.
func splitTopLevel(text string) []string {
	parts := make([]string, 0, 4)
	depth, quoted, start := 0, false, 0
	for index, character := range text {
		switch {
		case character == '"':
			quoted = !quoted
		case quoted:
		case character == '(' || character == '{' || character == '[':
			depth++
		case character == ')' || character == '}' || character == ']':
			depth--
		case character == ',' && depth == 0:
			parts = append(parts, text[start:index])
			start = index + 1
		}
	}
	return append(parts, text[start:])
}

// isNameToken 은 매개변수로 쓸 수 있는 이름인지 본다. 아니라면 LAMBDA 의
// 모습을 하고 있어도 kanpic 이 담을 수 있는 것이 아니다.
func isNameToken(text string) bool {
	if text == "" {
		return false
	}
	for index, character := range text {
		if character == '_' || unicode.IsLetter(character) {
			continue
		}
		if index > 0 && (character == '.' || unicode.IsDigit(character)) {
			continue
		}
		return false
	}
	return true
}
