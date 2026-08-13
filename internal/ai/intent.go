package ai

import "strings"

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
	if containsAny(value, "차트", "그래프", "chart", "시각화") {
		return routedIntent{Mode: ModeChart, Skill: "chart_generation"}
	}
	if containsAny(value, "보기 좋게", "서식", "스타일", "열 너비", "정렬해줘", "format", "디자인") {
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
