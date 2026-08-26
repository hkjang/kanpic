package ai

import (
	"regexp"
	"strings"

	"kanpic/internal/workbook"
)

type routedIntent struct {
	Mode  string
	Skill string
}

// routeIntent is deliberately deterministic. The model plans the work, but it
// does not get to silently widen a read request into a write-capable mode.
func routeIntent(message string) routedIntent {
	value := strings.ToLower(strings.TrimSpace(message))
	command := ""
	if strings.HasPrefix(value, "/") {
		command = strings.Fields(value)[0]
	}
	switch command {
	case "/formula":
		return routedIntent{Mode: ModeFormula, Skill: "formula_generation"}
	case "/fix":
		return routedIntent{Mode: ModeFix, Skill: "formula_repair"}
	case "/clean":
		return routedIntent{Mode: ModeClean, Skill: "data_cleanup"}
	case "/format":
		return routedIntent{Mode: ModeFormat, Skill: "sheet_formatting"}
	case "/chart":
		return routedIntent{Mode: ModeChart, Skill: "chart_generation"}
	case "/analyze", "/summarize":
		return routedIntent{Mode: ModeSummarize, Skill: "data_analysis"}
	case "/explain":
		return routedIntent{Mode: ModeExplain, Skill: "formula_explain"}
	case "/pivot":
		return routedIntent{Mode: ModeAgent, Skill: "pivot_analysis"}
	}
	if containsAny(value, "지난번 작업 취소", "작업 취소", "undo", "되돌려") {
		return routedIntent{Mode: ModeAgent, Skill: "rollback"}
	}
	if containsAny(value, "경영진 보고", "경영회의", "신규 시트", "새 시트", "피벗", "pivot", "여러 시트", "sheet1", "sheet2") {
		return routedIntent{Mode: ModeAgent, Skill: "workbook_orchestration"}
	}
	// 아래 다섯 도구는 에이전트 모드에서만 쓸 수 있다. 사람이 그 일을 말로
	// 시켰는데 읽기 전용 모드로 가면, 도구가 있어도 닿지 않는다. 차트보다
	// 먼저 보는 이유는 "매출 많은 순으로 정렬해줘" 같은 말이 아래의 서식·
	// 수식 갈래로 새어 나가기 때문이다.
	if skill := agentToolSkill(value); skill != "" {
		return routedIntent{Mode: ModeAgent, Skill: skill}
	}
	if containsAny(value, "차트", "그래프", "chart", "시각화") && containsAny(value, "바꿔", "변경", "수정", "전환", "업데이트", "change", "update") {
		return routedIntent{Mode: ModeChart, Skill: "chart_update"}
	}
	if containsAny(value, "차트", "그래프", "chart", "시각화") {
		return routedIntent{Mode: ModeChart, Skill: "chart_generation"}
	}
	if alignmentWords(value) || containsAny(value, "보기 좋게", "서식", "스타일", "열 너비", "정렬해줘", "format", "디자인") {
		return routedIntent{Mode: ModeFormat, Skill: "sheet_formatting"}
	}
	if containsAny(value, "잘못된 수식", "수식 오류", "#ref!", "#value!", "고쳐", "수정해") {
		return routedIntent{Mode: ModeFix, Skill: "formula_repair"}
	}
	if containsAny(value, "수식 설명", "계산 방식", "왜 이런 값", "explain") {
		return routedIntent{Mode: ModeExplain, Skill: "formula_explain"}
	}
	if containsAny(value, "중복", "빈 행", "빈값", "정제", "표준화", "통일", "clean") {
		return routedIntent{Mode: ModeClean, Skill: "data_cleanup"}
	}
	if containsAny(value, "이상치", "이상한 값", "급격", "anomaly", "outlier") {
		return routedIntent{Mode: ModeAnomaly, Skill: "anomaly_detection"}
	}
	if containsAny(value, "수식", "합계", "증가율", "계산", "자동 채우", "lookup", "xlookup", "sum") {
		return routedIntent{Mode: ModeFormula, Skill: "formula_generation"}
	}
	return routedIntent{Mode: ModeSummarize, Skill: "data_analysis"}
}

// agentToolSkill 은 사람이 말한 일이 워크북 도구로 하는 일인지 본다. 빈
// 문자열이면 아니다.
func agentToolSkill(value string) string {
	switch {
	case tableIntent(value):
		return "sheet_table"
	case conditionalFormatIntent(value):
		return "conditional_format"
	case validationIntent(value):
		return "data_validation"
	case pivotIntent(value):
		return "pivot_analysis"
	case filterIntent(value):
		return "filter_view"
	case sortIntent(value):
		return "range_sort"
	}
	return ""
}

// tableIntent 는 범위를 이름 있는 표로 만들라는 요청이다.
//
// "표" 는 표 프로그램에서 가장 흔한 낱말이라 넓게 잡으면 안 된다. "표 서식을
// 입혀줘" 는 색을 칠하는 일이고 "피벗 테이블" 은 다른 도구다. 그래서 범위를
// 표로 **바꾸는** 말만 본다 — 만들다, 바꾸다, 변환하다가 뒤에 와야 한다.
func tableIntent(value string) bool {
	if containsAny(value, "피벗", "pivot", "서식", "스타일", "색") {
		return false
	}
	return containsAny(value,
		"표로 만들", "표로 바꿔", "표로 변환", "표로 지정",
		"테이블로 만들", "테이블로 바꿔", "테이블로 변환",
		"이름 있는 표", "구조화된 표", "표 이름을 붙", "표로 등록")
}

// sortIntent 는 "정렬" 이 줄 세우기인지 맞춤인지 가른다. 한국어에서 두 뜻이
// 같은 말이다 — "가운데 정렬" 은 서식이고 "매출 많은 순으로 정렬" 은 줄을
// 다시 세우는 일이다. 가르지 못하면 정렬 요청이 서식 갈래로 샌다.
func sortIntent(value string) bool {
	if alignmentWords(value) {
		return false
	}
	return containsAny(value, "오름차순", "내림차순", "순으로 정렬", "기준으로 정렬", "순서대로", "큰 순", "작은 순", "많은 순", "적은 순", "높은 순", "낮은 순", "최신순", "가나다순", "sort by", "ascending", "descending") ||
		(containsAny(value, "정렬", "줄 세", "순서") && !containsAny(value, "서식", "스타일", "너비"))
}

// alignmentWords 는 "정렬" 이 맞춤을 뜻하는 자리를 찾는다. 줄 세우기에서
// 빼는 곳과 서식으로 보내는 곳이 같은 목록을 본다 — 두 곳에 나누어 적으면
// 한쪽만 늘어나고, 그때 정렬 요청이 다시 샌다.
func alignmentWords(value string) bool {
	return containsAny(value, "가운데 정렬", "왼쪽 정렬", "오른쪽 정렬", "위쪽 정렬", "아래쪽 정렬", "세로 정렬", "가로 정렬", "텍스트 정렬", "정렬 맞춤", "맞춤 정렬", "양쪽 정렬")
}

// filterIntent 는 보이는 줄만 줄이는 요청이다. 지우는 것이 아니다.
func filterIntent(value string) bool {
	return containsAny(value, "필터", "걸러", "filter") ||
		containsAny(value, "만 보이", "만 보여", "만 나오게", "만 표시", "빼고 보여", "빼고 표시", "제외하고 보여", "숨겨서 보여") ||
		(containsAny(value, "이상만", "이하만", "초과만", "미만만") && !containsAny(value, "합계", "평균"))
}

// validationIntent 는 셀에 넣을 수 있는 값을 제한하는 요청이다.
func validationIntent(value string) bool {
	return containsAny(value, "드롭다운", "체크박스", "유효성", "입력 규칙", "입력 제한", "dropdown", "checkbox", "validation") ||
		containsAny(value, "목록에서만", "목록에서 고르", "중에서만 고르", "만 고르게", "만 입력", "만 넣을 수", "못 넣게", "못 쓰게", "입력하지 못하게")
}

// conditionalFormatIntent 는 값에 따라 칠하라는 요청이다. 셀에 색을 박는
// 서식과 달리 자료가 바뀌면 색도 따라 바뀌어야 한다.
func conditionalFormatIntent(value string) bool {
	if containsAny(value, "조건부 서식", "conditional format") {
		return true
	}
	return containsAny(value, "색 칠", "색칠", "빨갛게", "빨간색으로", "노랗게", "초록으로", "색으로 표시", "색으로 구분", "강조해", "강조 표시", "하이라이트", "눈에 띄게", "highlight") &&
		containsAny(value, "이상", "이하", "초과", "미만", "넘는", "넘으면", "미달", "음수", "상위", "하위", "중복", "빈 값", "다른", "같은", "조건", "면 ", "인 ", "일 때")
}

// pivotIntent 는 분류별 요약 요청이다. "부서별 매출 합계" 처럼 무엇으로
// 묶고 무엇을 셀지가 함께 있어야 한다.
func pivotIntent(value string) bool {
	if containsAny(value, "피벗", "pivot", "교차 분석", "크로스탭") {
		return true
	}
	for _, notGrouping := range []string{"특별", "개별", "각별", "구별", "차별", "판별", "식별", "분별", "이별", "작별"} {
		value = strings.ReplaceAll(value, notGrouping, "")
	}
	return groupingPhrase.MatchString(value) &&
		containsAny(value, "합계", "총계", "평균", "개수", "건수", "집계", "요약", "몇 개", "몇 명", "세줘", "세어", "카운트", "정리해", "묶어")
}

// groupingPhrase 는 "부서별", "월별" 처럼 무엇으로 묶는지 말하는 자리를
// 찾는다. "특별", "개별" 처럼 별이 접미사가 아닌 말은 제외한다.
var groupingPhrase = regexp.MustCompile(`(?:^|[^가-힣])(?:[가-힣]{1,6})별(?:[^가-힣]|$)`)

func routeFollowUpIntent(message string, routed routedIntent, history []ConversationMessage, charts []workbook.Chart) routedIntent {
	if routed.Mode != ModeSummarize || routed.Skill != "data_analysis" || len(charts) == 0 {
		return routed
	}
	value := strings.ToLower(strings.TrimSpace(message))
	if !containsAny(value, "바꿔", "변경", "수정", "전환", "업데이트", "제목", "범례", "축", "change", "update", "rename") {
		return routed
	}
	start := max(0, len(history)-6)
	previous := ""
	for _, item := range history[start:] {
		previous += " " + strings.ToLower(item.Content)
	}
	if containsAny(previous, "차트", "그래프", "chart", "시각화") {
		return routedIntent{Mode: ModeChart, Skill: "chart_update"}
	}
	return routed
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func riskForMode(mode string, changes int) string {
	if changes >= 1000 {
		return RiskCritical
	}
	switch mode {
	case ModeExplain, ModeSummarize, ModeAnomaly:
		return RiskRead
	case ModeFormat:
		return RiskLow
	case ModeFormula, ModeFix, ModeChart:
		return RiskMedium
	case ModeClean, ModeAgent:
		return RiskHigh
	default:
		return RiskHigh
	}
}

func skillForMode(mode string) string {
	return map[string]string{
		ModeFormula: "formula_generation", ModeFix: "formula_repair", ModeExplain: "formula_explain",
		ModeSummarize: "data_analysis", ModeAnomaly: "anomaly_detection", ModeClean: "data_cleanup",
		ModeFormat: "sheet_formatting", ModeChart: "chart_generation", ModeAgent: "workbook_orchestration",
	}[mode]
}
