package importexport

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

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
			XAxis:     excelize.ChartAxis{Title: excelize.ChartTitle{Paragraph: axisTitle(item.XAxisTitle)}},
			YAxis:     excelize.ChartAxis{Title: excelize.ChartTitle{Paragraph: axisTitle(item.YAxisTitle)}},
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
