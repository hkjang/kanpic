package ptium

import (
	"strings"
	"testing"

	"kanpic/internal/presentation"
)

func TestWriteSourceProducesTheDeckLanguage(t *testing.T) {
	t.Parallel()
	deck := presentation.Deck{Title: "2026년 영업실적", Slides: []presentation.Slide{
		{Kind: presentation.SlideCover, Title: "2026년 영업실적", Lead: "영업2가 목표에 못 미칩니다.", Notes: "출처: 실적 A1:D4"},
		{Kind: presentation.SlideContent, Title: "핵심 지표", Component: &presentation.Component{
			Kind: "kpi", Caption: "주요 지표",
			Rows: []presentation.Row{{Label: "목표 미달", Fields: []string{"91%", "영업2"}}},
		}},
		{Kind: presentation.SlideContent, Title: "주요 시사점", Bullets: []string{"영업2 원인 점검"}},
	}}
	source := WriteSource(deck)
	want := strings.Join([]string{
		"# 2026년 영업실적", "@cover", "> 영업2가 목표에 못 미칩니다.", "!notes 출처: 실적 A1:D4", "",
		"# 핵심 지표", "@content", "::kpi 주요 지표", "- 목표 미달 | 91% | 영업2", "::", "",
		"# 주요 시사점", "@content", "- 영업2 원인 점검", "",
	}, "\n")
	if source != want {
		t.Fatalf("source =\n%s\nwant\n%s", source, want)
	}
}

// 언어가 줄 단위이므로, 값 안에 든 구분 기호가 그대로 나가면 슬라이드가 조용히
// 달라진다. 부서 이름에 세로줄이 하나 있으면 열이 하나 더 생긴다.
func TestWriteSourceProtectsValuesThatLookLikeMarkers(t *testing.T) {
	t.Parallel()
	deck := presentation.Deck{Slides: []presentation.Slide{{
		Kind: presentation.SlideContent, Title: "# 예산 검토",
		Component: &presentation.Component{Kind: "table", Rows: []presentation.Row{
			{Label: "영업1|2", Fields: []string{"120억"}},
			{Label: "줄바꿈\n있는 값", Fields: []string{"95억"}},
		}},
	}}}
	source := WriteSource(deck)
	if !strings.Contains(source, "# \\# 예산 검토") {
		t.Fatalf("title was not escaped:\n%s", source)
	}
	if !strings.Contains(source, "- \\영업1|2 | 120억") {
		t.Fatalf("field separator was not escaped:\n%s", source)
	}
	// 줄바꿈이 남으면 그 줄은 표의 행이 아니라 새 문단이 된다.
	if strings.Contains(source, "줄바꿈\n") || !strings.Contains(source, "줄바꿈 있는 값") {
		t.Fatalf("newline survived into the source:\n%s", source)
	}
}

// 빈 칸이 뒤에 남으면 값 없는 열이 하나 더 생긴다.
func TestWriteSourceDropsTrailingEmptyFields(t *testing.T) {
	t.Parallel()
	source := WriteSource(presentation.Deck{Slides: []presentation.Slide{{
		Kind: presentation.SlideContent, Title: "지표",
		Component: &presentation.Component{Kind: "kpi", Rows: []presentation.Row{{Label: "합계", Fields: []string{"325억", ""}}}},
	}}})
	if !strings.Contains(source, "- 합계 | 325억\n") {
		t.Fatalf("trailing field kept:\n%s", source)
	}
}

// kanpic 이 아는 종류를 Ptium 이 모를 수 있다. 지어내는 대신 표로 두면 값은
// 하나도 잃지 않는다.
func TestWriteSourceFallsBackForAnUnknownComponent(t *testing.T) {
	t.Parallel()
	source := WriteSource(presentation.Deck{Slides: []presentation.Slide{{
		Kind: presentation.SlideContent, Title: "가정",
		Component: &presentation.Component{Kind: "sankey", Rows: []presentation.Row{{Label: "가", Fields: []string{"1"}}}},
	}}})
	if !strings.Contains(source, "::table") || strings.Contains(source, "sankey") {
		t.Fatalf("unknown component:\n%s", source)
	}
}
