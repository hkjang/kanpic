package formula

import "testing"

// 시나리오는 여러 칸을 한 벌로 묶어 이름을 붙인 것이다. 회의에서 두 안을
// 나란히 놓고 보는 그 일을 수식으로 옮긴다.
func TestCompareScenariosLinesUpTheAssumptions(t *testing.T) {
	t.Parallel()
	// 이익 = (단가 - 원가) * 수량
	cells := sheet(map[string]CellState{
		"B1": {Value: 10000.0}, // 단가
		"B2": {Value: 6000.0},  // 원가
		"B3": {Value: 1000.0},  // 수량
		"B4": {Formula: "=(B1-B2)*B3"},
		"B5": {Formula: "=B4/B3"},
	})
	result, err := New().CompareScenarios(cells, ScenarioCompareInput{
		Targets: []string{"B4", "B5"},
		Sets: []ScenarioSet{
			{Name: "낙관", Inputs: map[string]float64{"B1": 12000, "B3": 1500}},
			{Name: "보수", Inputs: map[string]float64{"B1": 9000, "B3": 800}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 가정을 넣기 전 모습을 함께 보여 주지 않으면 무엇에서 무엇으로 바뀌는지
	// 알 수 없다.
	if len(result.Current) != 2 || result.Current[0] == nil || *result.Current[0] != 4000000 {
		t.Fatalf("지금 값=%#v", result.Current)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("줄 수=%d", len(result.Rows))
	}
	// 낙관: (12000-6000)*1500 = 9,000,000, 한 개당 6000
	if *result.Rows[0].Values[0] != 9000000 || *result.Rows[0].Values[1] != 6000 {
		t.Errorf("낙관=%v %v", *result.Rows[0].Values[0], *result.Rows[0].Values[1])
	}
	// 보수: (9000-6000)*800 = 2,400,000, 한 개당 3000
	if *result.Rows[1].Values[0] != 2400000 || *result.Rows[1].Values[1] != 3000 {
		t.Errorf("보수=%v %v", *result.Rows[1].Values[0], *result.Rows[1].Values[1])
	}
	// 벌마다 원래 값으로 되돌린 뒤 그 벌의 가정만 넣어야 한다. 되돌리지 않으면
	// 앞 벌의 단가 12000 이 뒤 벌에 남아 보수가 틀린 값을 낸다.
	if cells["B1"].Value != 10000.0 || cells["B3"].Value != 1000.0 {
		t.Errorf("원본이 바뀌었다: B1=%v B3=%v", cells["B1"].Value, cells["B3"].Value)
	}
}

// 셈하지 못한 자리는 까닭을 적는다. 지금 값을 그대로 적으면 가정이 먹힌 것처럼
// 보인다.
func TestCompareScenariosReportsWhatItCouldNotCompute(t *testing.T) {
	t.Parallel()
	cells := sheet(map[string]CellState{
		"B1": {Value: 1000.0},
		"B2": {Value: 5.0},
		"B3": {Formula: "=B1/B2"},
		"B4": {Value: "메모"},
	})
	result, err := New().CompareScenarios(cells, ScenarioCompareInput{
		Targets: []string{"B3", "B4"},
		Sets:    []ScenarioSet{{Name: "0으로", Inputs: map[string]float64{"B2": 0}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := result.Rows[0]
	if row.Values[0] != nil {
		t.Errorf("나눌 수 없는 자리에 값이 있다: %v", *row.Values[0])
	}
	if len(row.Failures) != 2 {
		t.Fatalf("못 셈한 자리=%#v", row.Failures)
	}
	if row.Failures[0].Target != "B3" || row.Failures[0].Reason != "#DIV/0!" {
		t.Errorf("첫 실패=%#v", row.Failures[0])
	}
	// 수식이 든 칸에 값을 넣으면 그 수식이 사라진다. 셈하기 전에 막는다.
	if _, err := New().CompareScenarios(cells, ScenarioCompareInput{
		Targets: []string{"B3"},
		Sets:    []ScenarioSet{{Name: "수식칸", Inputs: map[string]float64{"B3": 1}}},
	}); err == nil {
		t.Error("수식 칸에 값을 넣으려는 것을 막지 않았다")
	}
	// 결과 셀이나 시나리오가 없으면 견줄 것이 없다.
	if _, err := New().CompareScenarios(cells, ScenarioCompareInput{Sets: []ScenarioSet{{Name: "빈", Inputs: map[string]float64{"B2": 1}}}}); err == nil {
		t.Error("결과 셀 없이 통과했다")
	}
	if _, err := New().CompareScenarios(cells, ScenarioCompareInput{Targets: []string{"B3"}}); err == nil {
		t.Error("시나리오 없이 통과했다")
	}
}
