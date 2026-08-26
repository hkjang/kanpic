package formula

import "testing"

// 데이터 표는 "이 값이 이만큼일 때 결과가 어떻게 되나" 를 한 번에 늘어놓는다.
// 손으로 하면 값을 넣고 적고 되돌리기를 되풀이해야 하는 일이다.
func TestDataTableSpellsOutOneAndTwoVariableAssumptions(t *testing.T) {
	t.Parallel()
	// B3 = 원금 * 이자율.
	cells := sheet(map[string]CellState{
		"B1": {Value: 1000.0},
		"B2": {Value: 0.05},
		"B3": {Formula: "=B1*B2"},
	})
	// 한 방향: 이자율만 바꿔 본다.
	result, err := New().DataTable(cells, DataTableInput{
		Target: "B3", ColumnInput: "B2", ColumnValues: []float64{0.03, 0.04, 0.05},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Values) != 3 || len(result.Failures) != 0 {
		t.Fatalf("한 방향 표=%#v", result)
	}
	for index, expected := range []float64{30, 40, 50} {
		if result.Values[index][0] == nil || *result.Values[index][0] != expected {
			t.Errorf("%d번째=%v, want %v", index, result.Values[index][0], expected)
		}
	}
	// 두 방향: 원금과 이자율을 함께 바꿔 본다.
	both, err := New().DataTable(cells, DataTableInput{
		Target: "B3", ColumnInput: "B1", ColumnValues: []float64{1000, 2000},
		RowInput: "B2", RowValues: []float64{0.03, 0.04},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]float64{{30, 40}, {60, 80}}
	for row := range want {
		for column := range want[row] {
			if both.Values[row][column] == nil || *both.Values[row][column] != want[row][column] {
				t.Errorf("[%d][%d]=%v, want %v", row, column, both.Values[row][column], want[row][column])
			}
		}
	}
	// 원래 시트는 그대로여야 한다. 가정을 넣어 본 것이 남으면 표를 한 번
	// 그렸다는 이유로 사람의 자료가 바뀐다.
	if cells["B2"].Value != 0.05 || cells["B1"].Value != 1000.0 {
		t.Errorf("원본이 바뀌었다: B1=%v B2=%v", cells["B1"].Value, cells["B2"].Value)
	}
}

// 셈하지 못한 자리는 비워 두고 까닭을 적는다. 0 으로 채우면 사람은 그것을
// 답으로 읽는다.
func TestDataTableReportsWhatItCouldNotCompute(t *testing.T) {
	t.Parallel()
	cells := sheet(map[string]CellState{
		"B1": {Value: 1000.0},
		"B2": {Value: 5.0},
		"B3": {Formula: "=B1/B2"},
	})
	result, err := New().DataTable(cells, DataTableInput{
		Target: "B3", ColumnInput: "B2", ColumnValues: []float64{10, 0, 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Values[0][0] == nil || *result.Values[0][0] != 100 {
		t.Errorf("첫 줄=%v", result.Values[0][0])
	}
	// 0 으로 나눈 자리는 비어 있고, 왜 비었는지 적혀 있어야 한다.
	if result.Values[1][0] != nil {
		t.Errorf("나눌 수 없는 자리에 값이 있다: %v", *result.Values[1][0])
	}
	if len(result.Failures) != 1 || result.Failures[0].Row != 1 || result.Failures[0].Reason != "#DIV/0!" {
		t.Fatalf("못 셈한 자리=%#v", result.Failures)
	}
	if result.Values[2][0] == nil || *result.Values[2][0] != 50 {
		t.Errorf("셋째 줄=%v", result.Values[2][0])
	}
}

// 넣은 값이 찾을 칸에 닿지 않으면 표는 같은 값으로 가득 찬다. 그것을 답으로
// 내면 사람은 그 가정이 결과를 바꾸지 않는다고 읽는다.
func TestDataTableRefusesInputsThatDoNotReachTheTarget(t *testing.T) {
	t.Parallel()
	cells := sheet(map[string]CellState{
		"B1": {Value: 1000.0},
		"B2": {Value: 0.05},
		"B3": {Formula: "=B1*2"},
		"C1": {Value: 7.0},
	})
	result, err := New().DataTable(cells, DataTableInput{
		Target: "B3", ColumnInput: "C1", ColumnValues: []float64{1, 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) != 2 {
		t.Fatalf("닿지 않는 가정=%#v", result)
	}
	// 수식이 든 칸에 값을 넣으면 그 수식이 사라진다. 셈하기 전에 막는다.
	if _, err := New().DataTable(cells, DataTableInput{Target: "B3", ColumnInput: "B3", ColumnValues: []float64{1}}); err == nil {
		t.Error("수식 칸에 값을 넣으려는 것을 막지 않았다")
	}
	// 넣을 칸을 하나도 정하지 않으면 표를 만들 수 없다.
	if _, err := New().DataTable(cells, DataTableInput{Target: "B3", ColumnValues: []float64{1}}); err == nil {
		t.Error("넣을 셀 없이 통과했다")
	}
	// 한 번에 셈할 수 있는 칸에는 끝이 있어야 한다. 표 하나가 워크북을
	// 그만큼 다시 셈하게 한다.
	many := make([]float64, DataTableMaxCells+1)
	if _, err := New().DataTable(cells, DataTableInput{Target: "B3", ColumnInput: "B2", ColumnValues: many}); err == nil {
		t.Error("너무 큰 표가 통과했다")
	}
}
