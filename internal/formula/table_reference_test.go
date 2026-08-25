package formula

import "testing"

// 표를 이름으로 가리키면 열이 끼워지고 지워져도 수식이 그대로 맞다. 범위로
// 적은 =SUM(C2:C50) 은 사람이 옮겨 적어야 하고, 잊으면 조용히 틀린 값을 낸다.
func TestStructuredTableReferences(t *testing.T) {
	t.Parallel()
	evaluator := New()
	evaluator.scope.CurrentSheet = "S1"
	evaluator.SetTables(map[string]Table{
		"매출표": {SheetID: "S1", Range: "A1:C4", HeaderRow: true, Columns: []string{"지역", "금액", "비고"}},
	})
	cells := map[string]any{
		"S1!A1": "지역", "S1!B1": "금액", "S1!C1": "비고",
		"S1!A2": "서울", "S1!B2": 100.0,
		"S1!A3": "부산", "S1!B3": 200.0,
		"S1!A4": "대구", "S1!B4": 300.0,
	}
	for formula, expected := range map[string]any{
		// 머리글을 뺀 자료 줄이 기본이다. 머리글까지 더하면 글자가 섞여 답이
		// 달라지는데, =SUM(매출표[금액]) 은 사람이 합계를 바란 것이다.
		`=SUM(매출표[금액])`:      600.0,
		`=SUM(매출표)`:          600.0,
		`=COUNTA(매출표[지역])`:   3.0,
		`=COUNTA(매출표[#전체])`:  9.0,
		`=COUNTA(매출표[#머리글])`: 3.0,
		`=SUM(매출표[#데이터])`:    600.0,
		// 엑셀은 지정자를 한 번 더 감싸기도 한다. 같은 뜻이다.
		`=SUM(매출표[[금액]])`: 600.0,
		// 파일에서 들어온 수식은 영문으로 적혀 있다.
		`=COUNTA(매출표[#All])`:             9.0,
		`=XLOOKUP("부산",매출표[지역],매출표[금액])`: 200.0,
	} {
		result := evaluator.Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	// 없는 열은 어느 표의 무슨 열인지 말해 준다. #REF! 만 보여 주면 사람이
	// 표를 하나하나 열어 봐야 한다.
	if result := evaluator.Evaluate(`=SUM(매출표[없는열])`, cells); result.Error == nil || result.Error.Code != "#REF!" {
		t.Errorf("없는 열=%#v", result.Error)
	}
	// 표가 아닌 이름은 지금까지처럼 모르는 이름이다.
	if result := evaluator.Evaluate(`=SUM(없는표[금액])`, cells); result.Error == nil || result.Error.Code != "#NAME?" {
		t.Errorf("없는 표=%#v", result.Error)
	}
	// 머리글 줄에도 기대야 한다. 기대 두지 않으면 열 이름을 고쳤을 때 다시
	// 셈할 계기가 없어, 없어진 열의 옛 답이 맞는 답인 양 남는다.
	result := evaluator.Evaluate(`=SUM(매출표[금액])`, cells)
	header := map[string]bool{}
	for _, dependency := range result.Dependencies {
		header[dependency] = true
	}
	if !header["S1!A1"] || !header["S1!B1"] || !header["S1!C1"] {
		t.Errorf("머리글에 기대지 않는다: %v", result.Dependencies)
	}
	// 대괄호가 닫히지 않으면 수식이 아니다.
	if result := evaluator.Evaluate(`=SUM(매출표[금액)`, cells); result.Error == nil {
		t.Error("닫히지 않은 대괄호가 통과했다")
	}
}

// 머리글이 없는 표는 열1, 열2 로 센다. 이름 없는 열도 가리킬 수 있어야 한다.
func TestTableWithoutHeaderRow(t *testing.T) {
	t.Parallel()
	evaluator := New()
	evaluator.scope.CurrentSheet = "S1"
	evaluator.SetTables(map[string]Table{
		"기록": {SheetID: "S1", Range: "A1:B2", HeaderRow: false, Columns: []string{"열1", "열2"}},
	})
	cells := map[string]any{"S1!A1": 1.0, "S1!B1": 2.0, "S1!A2": 3.0, "S1!B2": 4.0}
	if result := evaluator.Evaluate(`=SUM(기록[열2])`, cells); result.Error != nil || result.Value != 6.0 {
		t.Errorf("열2 합계=%v (%v)", result.Value, result.Error)
	}
	if result := evaluator.Evaluate(`=SUM(기록)`, cells); result.Error != nil || result.Value != 10.0 {
		t.Errorf("표 전체=%v (%v)", result.Value, result.Error)
	}
	// 머리글이 없으면 머리글 줄을 달라고 할 수 없다.
	if result := evaluator.Evaluate(`=COUNTA(기록[#머리글])`, cells); result.Error == nil || result.Error.Code != "#REF!" {
		t.Errorf("없는 머리글=%#v", result.Error)
	}
}
