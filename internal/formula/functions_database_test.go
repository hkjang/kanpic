package formula

import "testing"

const databaseTable = `{"부서","매출","인원";"영업",100,3;"개발",70,5;"영업",50,2;"지원",30,1}`

// D 로 시작하는 함수는 표에서 조건에 맞는 줄만 골라 한 열을 셈한다. 업무용
// 표에서 아직 자주 보이는데 하나도 없었다. 값은 손으로 셀 수 있는 자료로
// 확인한다 — 영업은 100과 50, 둘의 합 150, 평균 75다.
func TestDatabaseFunctionsAggregateOnlyMatchingRows(t *testing.T) {
	t.Parallel()
	engine := New()
	sales := `{"부서";"영업"}`
	for _, item := range []struct{ expression, expected string }{
		{`=DSUM(` + databaseTable + `,"매출",` + sales + `)`, "150"},
		// 열은 이름으로도 몇 번째인지로도 가리킬 수 있다.
		{`=DSUM(` + databaseTable + `,2,` + sales + `)`, "150"},
		{`=DAVERAGE(` + databaseTable + `,"매출",` + sales + `)`, "75"},
		{`=DCOUNT(` + databaseTable + `,"매출",` + sales + `)`, "2"},
		{`=DCOUNTA(` + databaseTable + `,"부서",` + sales + `)`, "2"},
		{`=DMAX(` + databaseTable + `,"매출",` + sales + `)`, "100"},
		{`=DMIN(` + databaseTable + `,"매출",` + sales + `)`, "50"},
		{`=DPRODUCT(` + databaseTable + `,"매출",` + sales + `)`, "5000"},
		// 100과 50의 표본 분산은 1250, 모집단 분산은 625다.
		{`=DVAR(` + databaseTable + `,"매출",` + sales + `)`, "1250"},
		{`=DVARP(` + databaseTable + `,"매출",` + sales + `)`, "625"},
		{`=DSTDEVP(` + databaseTable + `,"매출",` + sales + `)`, "25"},
		{`=ROUND(DSTDEV(` + databaseTable + `,"매출",` + sales + `),4)`, "35.3553"},
		// 조건은 COUNTIF 와 같은 규칙으로 읽는다.
		{`=DSUM(` + databaseTable + `,"매출",{"매출";">=50"})`, "220"},
		// 조건표의 줄이 여럿이면 그중 하나만 맞으면 된다.
		{`=DSUM(` + databaseTable + `,"매출",{"부서";"영업";"지원"})`, "180"},
		// 한 줄 안의 조건은 모두 맞아야 한다.
		{`=DSUM(` + databaseTable + `,"매출",{"부서","매출";"영업",">60"})`, "100"},
		{`=DGET(` + databaseTable + `,"매출",{"부서";"지원"})`, "30"},
		{`=DGET(` + databaseTable + `,"부서",{"매출";">90"})`, "영업"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s -> %s %s", item.expression, result.Error.Code, result.Error.Message)
			continue
		}
		if actual := display(result.Value); actual != item.expected {
			t.Errorf("%s=%s, 기대=%s", item.expression, actual, item.expected)
		}
	}
}

// 답을 낼 수 없는 자리는 그렇다고 말해야 한다. 조용히 0 을 내면 사람은
// 조건에 맞는 줄이 없다는 것과 합이 0 이라는 것을 구별할 수 없다.
func TestDatabaseFunctionsSayWhenTheyCannotAnswer(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct{ expression, code string }{
		// DGET 은 딱 한 줄이어야 한다.
		{`=DGET(` + databaseTable + `,"매출",{"부서";"영업"})`, "#NUM!"},
		{`=DGET(` + databaseTable + `,"매출",{"부서";"없음"})`, "#VALUE!"},
		// 표에 없는 열은 셈할 수 없다. 조건표의 머리글도 마찬가지다.
		{`=DSUM(` + databaseTable + `,"없는열",{"부서";"영업"})`, "#VALUE!"},
		{`=DSUM(` + databaseTable + `,"매출",{"없는열";"영업"})`, "#VALUE!"},
		{`=DSUM(` + databaseTable + `,9,{"부서";"영업"})`, "#VALUE!"},
		// 평균 낼 숫자가 없으면 나눌 수 없다.
		{`=DAVERAGE(` + databaseTable + `,"매출",{"부서";"없음"})`, "#DIV/0!"},
		// 표본 표준편차는 두 개가 있어야 한다.
		{`=DSTDEV(` + databaseTable + `,"매출",{"부서";"지원"})`, "#DIV/0!"},
		// 머리글만 있고 자료가 없는 표는 표가 아니다.
		{`=DSUM({"부서","매출"},"매출",{"부서";"영업"})`, "#VALUE!"},
		{`=DSUM(` + databaseTable + `,"매출")`, "#VALUE!"},
	} {
		result := engine.Evaluate(item.expression, map[string]any{})
		if result.Error == nil {
			t.Errorf("%s 가 %v 를 냈다", item.expression, result.Value)
			continue
		}
		if result.Error.Code != item.code {
			t.Errorf("%s -> %s, 기대=%s", item.expression, result.Error.Code, item.code)
		}
	}
	// 조건에 맞는 줄이 없으면 합은 0 이다. 이것은 오류가 아니다.
	if result := engine.Evaluate(`=DSUM(`+databaseTable+`,"매출",{"부서";"없음"})`, map[string]any{}); result.Error != nil || display(result.Value) != "0" {
		t.Errorf("맞는 줄이 없는 DSUM=%#v", result)
	}
}
