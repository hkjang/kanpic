package importexport

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strings"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// 엑셀의 차트는 xl/charts/chartN.xml 에 따로 담긴다. excelize 는 차트를 쓸
// 수는 있어도 읽지는 못해서(v2.11.0 에 AddChart 는 있고 GetChart 는 없다),
// 여기서 XML 을 곧바로 읽는다.
//
// 이름 공간은 두 가지 모습으로 온다. 엑셀이 쓴 파일은 <c:barChart> 처럼
// 앞가지를 붙이고, 다른 도구가 쓴 파일은 기본 이름 공간을 써서
// <barChart> 로 적는다. Go 의 encoding/xml 은 앞가지를 빼고 이름만 맞추므로
// 둘 다 읽힌다. 시험에서 두 모습을 모두 확인한다.
type chartSpaceXML struct {
	Chart struct {
		Title struct {
			Rich struct {
				Paragraphs []struct {
					Runs []struct {
						Text string `xml:"t"`
					} `xml:"r"`
				} `xml:"p"`
			} `xml:"rich"`
		} `xml:"title>tx"`
		PlotArea struct {
			Bar     *chartKindXML `xml:"barChart"`
			Line    *chartKindXML `xml:"lineChart"`
			Pie     *chartKindXML `xml:"pieChart"`
			Area    *chartKindXML `xml:"areaChart"`
			Scatter *chartKindXML `xml:"scatterChart"`
		} `xml:"plotArea"`
	} `xml:"chart"`
}

type chartKindXML struct {
	Direction struct {
		Value string `xml:"val,attr"`
	} `xml:"barDir"`
	Grouping struct {
		Value string `xml:"val,attr"`
	} `xml:"grouping"`
	Series []struct {
		Name       string `xml:"tx>strRef>f"`
		Categories string `xml:"cat>strRef>f"`
		NumberCats string `xml:"cat>numRef>f"`
		Values     string `xml:"val>numRef>f"`
		ScatterX   string `xml:"xVal>numRef>f"`
		ScatterY   string `xml:"yVal>numRef>f"`
	} `xml:"ser"`
}

// importedChart is one chart recovered from the file, already reduced to what
// kanpic stores: a type, a title and the one range its series came from.
type importedChart struct {
	SheetName string
	Chart     workbook.ImportChart
}

// importCharts 는 파일 안의 차트를 읽어 시트별로 나눠 돌려준다. 읽지 못한
// 차트 수도 함께 돌려주어, 부르는 쪽이 "차트 2개는 가져오지 않습니다" 라고
// 정확한 수를 알릴 수 있게 한다.
func importCharts(archive *zip.Reader) ([]importedChart, int) {
	charts := make([]importedChart, 0)
	unread := 0
	for _, entry := range archive.File {
		if !strings.HasPrefix(entry.Name, "xl/charts/chart") || !strings.HasSuffix(entry.Name, ".xml") {
			continue
		}
		parsed, ok := readChartPart(entry)
		if !ok {
			unread++
			continue
		}
		charts = append(charts, parsed)
	}
	return charts, unread
}

func readChartPart(entry *zip.File) (importedChart, bool) {
	file, err := entry.Open()
	if err != nil {
		return importedChart{}, false
	}
	defer func() { _ = file.Close() }()
	// 차트 하나가 아무리 커도 이 정도면 넉넉하다. 압축을 푼 크기로 사람을
	// 지치게 만드는 파일을 막는다.
	raw, err := io.ReadAll(io.LimitReader(file, 4<<20))
	if err != nil {
		return importedChart{}, false
	}
	var space chartSpaceXML
	if err := xml.Unmarshal(raw, &space); err != nil {
		return importedChart{}, false
	}
	kind, name := chartKind(space)
	if kind == nil || len(kind.Series) == 0 {
		return importedChart{}, false
	}
	sheetName, area, ok := seriesBounds(kind)
	if !ok {
		return importedChart{}, false
	}
	return importedChart{SheetName: sheetName, Chart: workbook.ImportChart{
		Type: name, Title: chartTitle(space), SourceRange: area,
	}}, true
}

// chartKind 는 그림이 어떤 종류인지 고른다. 여러 종류가 겹쳐 있으면 첫
// 번째만 가져온다 — kanpic 의 combo 는 여기서 되살리기에는 모양이 다르다.
func chartKind(space chartSpaceXML) (*chartKindXML, string) {
	plot := space.Chart.PlotArea
	switch {
	case plot.Bar != nil:
		// barDir 이 col 이면 세로 막대, bar 면 가로 막대다. kanpic 의 bar 는
		// 둘을 나누지 않는다.
		if strings.EqualFold(plot.Bar.Grouping.Value, "stacked") {
			return plot.Bar, "stacked_bar"
		}
		return plot.Bar, "bar"
	case plot.Line != nil:
		return plot.Line, "line"
	case plot.Pie != nil:
		return plot.Pie, "pie"
	case plot.Area != nil:
		if strings.EqualFold(plot.Area.Grouping.Value, "stacked") {
			return plot.Area, "stacked_area"
		}
		return plot.Area, "area"
	case plot.Scatter != nil:
		return plot.Scatter, "scatter"
	}
	return nil, ""
}

func chartTitle(space chartSpaceXML) string {
	var builder strings.Builder
	for _, paragraph := range space.Chart.Title.Rich.Paragraphs {
		for _, run := range paragraph.Runs {
			builder.WriteString(run.Text)
		}
	}
	return strings.TrimSpace(builder.String())
}

// seriesBounds 는 계열이 가리키는 자리를 모두 감싸는 하나의 범위를 만든다.
//
// kanpic 의 차트는 이어진 범위 하나를 본다. 엑셀은 계열마다 따로 가리킬 수
// 있어서, 떨어진 자리를 가리키는 차트는 감싸는 범위가 원래 뜻과 달라진다.
// 그런 차트는 되살리지 않고 "가져오지 않았다" 고 알리는 편이 낫다 — 엉뚱한
// 그림을 그려 두면 사람이 그것을 믿는다.
func seriesBounds(kind *chartKindXML) (string, string, bool) {
	sheetName := ""
	first, last := cellrange.Position{}, cellrange.Position{}
	found := false
	for _, series := range kind.Series {
		for _, reference := range []string{series.Name, series.Categories, series.NumberCats, series.Values, series.ScatterX, series.ScatterY} {
			if strings.TrimSpace(reference) == "" {
				continue
			}
			sheet, area, ok := splitDefinedNameTarget(reference)
			if !ok {
				return "", "", false
			}
			if sheetName == "" {
				sheetName = sheet
			} else if sheetName != sheet {
				// 여러 시트에서 끌어 온 차트. 범위 하나로 묶을 수 없다.
				return "", "", false
			}
			parsed, err := cellrange.Parse(area)
			if err != nil {
				return "", "", false
			}
			if !found {
				first, last, found = parsed.Start, parsed.End, true
				continue
			}
			first.Row = min(first.Row, parsed.Start.Row)
			first.Column = min(first.Column, parsed.Start.Column)
			last.Row = max(last.Row, parsed.End.Row)
			last.Column = max(last.Column, parsed.End.Column)
		}
	}
	if !found || sheetName == "" {
		return "", "", false
	}
	return sheetName, cellrange.Address(first.Row, first.Column) + ":" + cellrange.Address(last.Row, last.Column), true
}
