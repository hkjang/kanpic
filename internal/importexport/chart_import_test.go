package importexport

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// 엑셀이 쓴 파일은 <c:barChart> 처럼 이름 앞가지를 붙이고, 다른 도구가 쓴
// 파일은 기본 이름 공간을 써서 <barChart> 로 적는다. 우리가 시험에 쓸 수
// 있는 excelize 는 뒤엣것만 쓴다.
//
// 뒤엣것으로만 확인하고 넘어가면, 정작 사람들이 들고 오는 **엑셀이 쓴
// 파일** 에서 아무 차트도 읽지 못하는 것을 모른 채 내보내게 된다. 그래서
// 앞가지를 붙인 모습도 손으로 만들어 함께 확인한다.
func TestChartXMLReadsBothNamespaceShapes(t *testing.T) {
	t.Parallel()
	plain := `<chartSpace xmlns="http://schemas.openxmlformats.org/drawingml/2006/chart"><chart><plotArea><barChart><grouping val="clustered"></grouping><ser><cat><strRef><f>Sheet1!$A$2:$A$4</f></strRef></cat><val><numRef><f>Sheet1!$B$2:$B$4</f></numRef></val></ser></barChart></plotArea></chart></chartSpace>`
	prefixed := `<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"><c:chart><c:plotArea><c:barChart><c:grouping val="clustered"/><c:ser><c:cat><c:strRef><c:f>Sheet1!$A$2:$A$4</c:f></c:strRef></c:cat><c:val><c:numRef><c:f>Sheet1!$B$2:$B$4</c:f></c:numRef></c:val></c:ser></c:barChart></c:plotArea></c:chart></c:chartSpace>`

	for name, document := range map[string]string{"기본 이름 공간": plain, "c: 앞가지": prefixed} {
		archive := zipWithChart(t, document)
		charts, unread := importCharts(archive)
		if unread != 0 {
			t.Errorf("%s: 읽지 못한 차트 %d개", name, unread)
		}
		if len(charts) != 1 {
			t.Fatalf("%s: 차트 %d개가 나왔다", name, len(charts))
		}
		if charts[0].SheetName != "Sheet1" || charts[0].Chart.Type != "bar" || charts[0].Chart.SourceRange != "A2:B4" {
			t.Errorf("%s: %#v", name, charts[0])
		}
	}
}

// 여러 시트에서 끌어 온 차트는 범위 하나로 묶을 수 없다. 엉뚱한 범위로
// 되살려 두면 사람이 그 그림을 믿는다. 되살리지 않고 알리는 편이 낫다.
func TestChartAcrossSheetsIsReportedNotGuessed(t *testing.T) {
	t.Parallel()
	document := `<chartSpace xmlns="http://schemas.openxmlformats.org/drawingml/2006/chart"><chart><plotArea><lineChart><ser><cat><strRef><f>Sheet1!$A$2:$A$4</f></strRef></cat><val><numRef><f>Sheet2!$B$2:$B$4</f></numRef></val></ser></lineChart></plotArea></chart></chartSpace>`
	charts, unread := importCharts(zipWithChart(t, document))
	if len(charts) != 0 || unread != 1 {
		t.Fatalf("charts=%#v unread=%d", charts, unread)
	}
}

// 진짜 xlsx 파일을 만들어 통째로 지나가 본다. 차트가 시트에 붙고, 종류와
// 제목과 범위가 그대로 와야 한다.
func TestXLSXImportKeepsCharts(t *testing.T) {
	t.Parallel()
	file := excelize.NewFile()
	defer file.Close()
	name := file.GetSheetName(0)
	for address, value := range map[string]any{"A1": "월", "B1": "매출", "A2": "1월", "B2": 100, "A3": "2월", "B3": 150, "A4": "3월", "B4": 120} {
		if err := file.SetCellValue(name, address, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.AddChart(name, "D2", &excelize.Chart{
		Type:   excelize.Bar,
		Series: []excelize.ChartSeries{{Name: name + "!$B$1", Categories: name + "!$A$2:$A$4", Values: name + "!$B$2:$B$4"}},
		Title:  excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: "월별 매출"}}},
	}); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := file.Write(&buffer); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse("charts.xlsx", buffer.Bytes(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sheets) == 0 || len(parsed.Sheets[0].Charts) != 1 {
		t.Fatalf("charts = %#v", parsed.Sheets)
	}
	chart := parsed.Sheets[0].Charts[0]
	if chart.Type != "bar" || chart.Title != "월별 매출" || chart.SourceRange != "A1:B4" {
		t.Fatalf("chart = %#v", chart)
	}
	// 가져왔으므로 "차트를 가져오지 않습니다" 라고 알리면 안 된다.
	for _, warning := range parsed.Preview.Warnings {
		if strings.Contains(warning, "차트") {
			t.Errorf("가져왔는데도 알림이 남았다: %q", warning)
		}
	}
}

func zipWithChart(t *testing.T, document string) *zip.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	part, err := writer.Create("xl/charts/chart1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(document)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return reader
}
