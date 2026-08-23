package presentation

import (
	"encoding/json"
	"strings"
	"testing"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

// sheetOf builds the cells of a small table so a test reads like the range it
// describes rather than like a list of coordinates.
func sheetOf(rows [][]any) []workbook.Cell {
	cells := []workbook.Cell{}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			if value == nil {
				continue
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				panic(err)
			}
			cells = append(cells, workbook.Cell{Row: rowIndex + 1, Column: columnIndex + 1, Value: encoded})
		}
	}
	return cells
}

func rangeOf(t *testing.T, address string) cellrange.Range {
	t.Helper()
	selected, err := cellrange.Parse(address)
	if err != nil {
		t.Fatalf("range %s: %v", address, err)
	}
	return selected
}

var salesRows = [][]any{
	{"부서", "매출", "전년대비", "목표달성률"},
	{"영업1", "120억", "+15%", "108%"},
	{"영업2", "95억", "-3%", "91%"},
	{"영업3", "110억", "+8%", "103%"},
}

func TestAnalyzeReadsWhatTheColumnsMean(t *testing.T) {
	t.Parallel()
	analysis := Analyze(SourceRef{SheetName: "실적", Range: "A1:D4"}, rangeOf(t, "A1:D4"), sheetOf(salesRows))
	if !analysis.HasHeader || analysis.RowCount != 3 {
		t.Fatalf("header=%v rows=%d", analysis.HasHeader, analysis.RowCount)
	}
	if analysis.Dimension != 0 || len(analysis.Measures) != 3 {
		t.Fatalf("dimension=%d measures=%v", analysis.Dimension, analysis.Measures)
	}
	// 단위가 붙은 값도 숫자로 읽어야 막대가 그려진다.
	if got := analysis.Columns[1]; got.Kind != ColumnNumber || got.Role != RoleMeasure || got.Values[0].Number != 120 {
		t.Fatalf("매출 column=%+v", got)
	}
	// 같은 백분율이라도 이름이 다른 말을 한다.
	if got := analysis.Columns[2]; got.Kind != ColumnPercent || got.Role != RoleChange {
		t.Fatalf("전년대비 column kind=%s role=%s", got.Kind, got.Role)
	}
	if got := analysis.Columns[3]; got.Role != RoleAttainment {
		t.Fatalf("목표달성률 role=%s", got.Role)
	}
	if analysis.Shape != ShapeCategories || analysis.Chart != ChartBars {
		t.Fatalf("shape=%s chart=%s", analysis.Shape, analysis.Chart)
	}
}

func TestAnalyzeFindsWhatIsWorthSaying(t *testing.T) {
	t.Parallel()
	analysis := Analyze(SourceRef{}, rangeOf(t, "A1:D4"), sheetOf(salesRows))
	kinds := map[string]Insight{}
	for _, insight := range analysis.Insights {
		if _, seen := kinds[insight.Kind]; !seen {
			kinds[insight.Kind] = insight
		}
	}
	if top := kinds[InsightTop]; top.Detail != "영업1" || top.Value != "120억" {
		t.Fatalf("top=%+v", top)
	}
	// 목표에 못 미친 곳이 이 표의 요점이다.
	if short := kinds[InsightShort]; short.Detail != "영업2" || short.Value != "91%" {
		t.Fatalf("short=%+v", short)
	}
	if growth := kinds[InsightGrowth]; growth.Detail != "영업1" || growth.Value != "+15%" {
		t.Fatalf("growth=%+v", growth)
	}
	// 목표 미달이 이 표에서 가장 할 말이 많은 사실이므로 맨 앞에 온다.
	if analysis.Insights[0].Kind != InsightShort {
		t.Fatalf("insights led with %s", analysis.Insights[0].Kind)
	}
	// 합계는 부분이 입은 단위를 그대로 입는다.
	if total := kinds[InsightTotal]; total.Value != "325억" {
		t.Fatalf("total=%+v", total)
	}
	if !strings.Contains(analysis.Headline, "영업2") || !strings.Contains(analysis.Headline, "91%") {
		t.Fatalf("headline=%q", analysis.Headline)
	}
}

// 목표를 모두 넘겼으면 미달은 없다. 가장 낮은 달성률도 좋은 소식이므로
// 슬라이드에 "목표 미달" 이라고 적으면 거짓말이 된다.
func TestAnalyzeDoesNotInventAProblem(t *testing.T) {
	t.Parallel()
	analysis := Analyze(SourceRef{}, rangeOf(t, "A1:C3"), sheetOf([][]any{
		{"부서", "매출", "목표달성률"},
		{"영업1", 120, "108%"},
		{"영업2", 95, "101%"},
	}))
	for _, insight := range analysis.Insights {
		if insight.Kind == InsightShort {
			t.Fatalf("reported a shortfall where every target was met: %+v", insight)
		}
	}
	if strings.Contains(analysis.Headline, "못 미") {
		t.Fatalf("headline=%q", analysis.Headline)
	}
}

func TestAnalyzeRecognisesShapes(t *testing.T) {
	t.Parallel()
	// 날짜 축은 추이다.
	series := Analyze(SourceRef{}, rangeOf(t, "A1:B4"), sheetOf([][]any{
		{"월", "처리량"}, {"1월", 120}, {"2월", 132}, {"3월", 148},
	}))
	if series.Shape != ShapeSeries || series.Chart != ChartLine {
		t.Fatalf("series shape=%s chart=%s", series.Shape, series.Chart)
	}
	// 백분율이 100으로 모이면 순위가 아니라 구성이다.
	share := Analyze(SourceRef{}, rangeOf(t, "A1:B4"), sheetOf([][]any{
		{"채널", "비중"}, {"직접", "50%"}, {"대리점", "30%"}, {"온라인", "20%"},
	}))
	if share.Chart != ChartShare {
		t.Fatalf("share chart=%s", share.Chart)
	}
	// 이름 열 없이 숫자 몇 개는 지표다.
	figures := Analyze(SourceRef{}, rangeOf(t, "A1:B2"), sheetOf([][]any{{"매출", "이익"}, {120, 18}}))
	if figures.Shape != ShapeFigures {
		t.Fatalf("figures shape=%s", figures.Shape)
	}
	// 항목이 너무 많으면 차트가 아니라 표다.
	many := make([][]any, 0, 20)
	many = append(many, []any{"항목", "값"})
	for index := 0; index < 18; index++ {
		many = append(many, []any{"항목" + string(rune('A'+index)), index})
	}
	wide := Analyze(SourceRef{}, rangeOf(t, "A1:B19"), sheetOf(many))
	if wide.Shape != ShapeTable || wide.Chart != ChartNone {
		t.Fatalf("wide shape=%s chart=%s", wide.Shape, wide.Chart)
	}
}

// 숫자 위의 숫자는 머리글이 아니다. 첫 줄을 이름으로 오해하면 자료 한 줄이
// 통째로 사라진다.
func TestAnalyzeOnlyTakesWordsAsAHeader(t *testing.T) {
	t.Parallel()
	analysis := Analyze(SourceRef{}, rangeOf(t, "A1:B3"), sheetOf([][]any{{1, 2}, {3, 4}, {5, 6}}))
	if analysis.HasHeader || analysis.RowCount != 3 {
		t.Fatalf("header=%v rows=%d", analysis.HasHeader, analysis.RowCount)
	}
}

func TestBuildFollowsTheShapeOfTheData(t *testing.T) {
	t.Parallel()
	analysis := Analyze(SourceRef{SheetName: "실적", Range: "A1:D4", Version: 7}, rangeOf(t, "A1:D4"), sheetOf(salesRows))
	deck := Build(analysis, DeckOptions{Title: "2026년 영업실적", IncludeTable: true})
	if deck.Slides[0].Kind != SlideCover || deck.Slides[0].Title != "2026년 영업실적" {
		t.Fatalf("cover=%+v", deck.Slides[0])
	}
	if !strings.Contains(deck.Slides[0].Notes, "A1:D4") || !strings.Contains(deck.Slides[0].Notes, "v7") {
		t.Fatalf("cover notes=%q", deck.Slides[0].Notes)
	}
	kinds := []string{}
	for _, slide := range deck.Slides {
		if slide.Component != nil {
			kinds = append(kinds, slide.Component.Kind)
		}
	}
	if strings.Join(kinds, ",") != "kpi,bars,table" {
		t.Fatalf("components=%v", kinds)
	}
	// 요점이 있는 마지막 장은 본문 레이아웃이어야 한다. 마무리 레이아웃에는
	// 글이 들어갈 자리가 없는 디자인이 흔하다.
	closing := deck.Slides[len(deck.Slides)-1]
	if closing.Kind != SlideContent || closing.Title != "주요 시사점" || len(closing.Bullets) == 0 {
		t.Fatalf("closing=%+v", closing)
	}
	// 표는 머리글부터 시작한다. 그러지 않으면 첫 부서가 열 이름이 된다.
	for _, slide := range deck.Slides {
		if slide.Component != nil && slide.Component.Kind == "table" {
			if slide.Component.Rows[0].Label != "부서" || slide.Component.Rows[1].Label != "영업1" {
				t.Fatalf("table rows=%+v", slide.Component.Rows[:2])
			}
		}
	}
}

// 스무 줄짜리 범위는 슬라이드에 다 들어가지 않는다. 조용히 자르면 열 줄이
// 전부인 것처럼 보이므로, 몇 개를 실었는지 말해야 한다.
func TestBuildSaysWhatItLeftOut(t *testing.T) {
	t.Parallel()
	rows := [][]any{{"항목", "값"}}
	for index := 0; index < 25; index++ {
		rows = append(rows, []any{"항목" + string(rune('A'+index)), index})
	}
	analysis := Analyze(SourceRef{}, rangeOf(t, "A1:B26"), sheetOf(rows))
	deck := Build(analysis, DeckOptions{IncludeTable: true})
	var table *Slide
	for index := range deck.Slides {
		if deck.Slides[index].Component != nil && deck.Slides[index].Component.Kind == "table" {
			table = &deck.Slides[index]
		}
	}
	if table == nil {
		t.Fatal("no table slide")
	}
	if len(table.Component.Rows) != MaxTableRows+1 {
		t.Fatalf("table rows=%d", len(table.Component.Rows))
	}
	if !strings.Contains(table.Lead, "25") || !strings.Contains(table.Notes, "15") {
		t.Fatalf("lead=%q notes=%q", table.Lead, table.Notes)
	}
}

func TestBuildOnAnEmptyRangeStillMakesADeck(t *testing.T) {
	t.Parallel()
	deck := Build(Analyze(SourceRef{}, rangeOf(t, "A1:C3"), nil), DeckOptions{Title: "빈 범위"})
	if len(deck.Slides) != 2 || deck.Slides[1].Lead == "" {
		t.Fatalf("deck=%+v", deck.Slides)
	}
}

// 달력의 한 칸과 기간을 재는 숫자는 다르다. 둘을 섞으면 "18개월" 이 시간
// 축으로 올라가고 추이 차트가 뒤집힌다.
func TestAnalyzeTellsCalendarLabelsFromDurations(t *testing.T) {
	t.Parallel()
	for text, wantDate := range map[string]bool{
		"1월": true, "12월": true, "13월": false, "18개월": false,
		"3분기": true, "5분기": false, "2026년": true, "3년": false,
		"Q3": true, "120억": false,
	} {
		if got := looksLikeDate(text); got != wantDate {
			t.Fatalf("looksLikeDate(%q)=%v", text, got)
		}
	}
	// 기간은 숫자로 남아 지표가 된다.
	duration := Analyze(SourceRef{}, rangeOf(t, "A1:B3"), sheetOf([][]any{{"항목", "기간"}, {"전환", "18개월"}, {"안정화", "6개월"}}))
	if duration.Columns[1].Kind != ColumnNumber || duration.Columns[1].Values[0].Number != 18 {
		t.Fatalf("duration column=%+v", duration.Columns[1])
	}
}
