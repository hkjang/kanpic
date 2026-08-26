package workbook

import "testing"

func waterfallChart() Chart {
	return Chart{Type: "waterfall", FirstRowHeaders: true}
}

// 폭포는 앞줄까지의 누계에서 시작해 값만큼 자란다. 그래야 매출에서 원가와
// 판관비를 빼 영업이익에 이르는 길이 보인다.
func TestWaterfallStacksFromTheRunningTotal(t *testing.T) {
	t.Parallel()
	matrix := [][]any{
		{"항목", "금액", "합계"},
		{"매출", 1000.0, nil},
		{"원가", -600.0, nil},
		{"판관비", -150.0, nil},
		{"영업이익", 250.0, true},
	}
	data := buildWaterfallData(waterfallChart(), 1, matrix)
	if len(data.Series) != 1 {
		t.Fatalf("계열이 %d 개다", len(data.Series))
	}
	want := []struct {
		category    string
		start, end  float64
		total       bool
	}{
		{"매출", 0, 1000, false},
		{"원가", 1000, 400, false},
		{"판관비", 400, 250, false},
		{"영업이익", 0, 250, true},
	}
	points := data.Series[0].Points
	if len(points) != len(want) {
		t.Fatalf("점이 %d 개다", len(points))
	}
	for index, expected := range want {
		point := points[index]
		if point.Category != expected.category || *point.X != expected.start || *point.Value != expected.end || point.Total != expected.total {
			t.Errorf("%d번째: %s %v→%v total=%v, 원하는 것은 %s %v→%v total=%v",
				index, point.Category, *point.X, *point.Value, point.Total,
				expected.category, expected.start, expected.end, expected.total)
		}
	}
}

// 합계 줄은 바닥부터 그리고, 그 뒤의 증감은 그 합계에서 이어진다.
func TestWaterfallContinuesFromASubtotal(t *testing.T) {
	t.Parallel()
	matrix := [][]any{
		{"항목", "금액", "합계"},
		{"매출", 1000.0, nil},
		{"매출총이익", 400.0, "합계"},
		{"판관비", -150.0, nil},
	}
	points := buildWaterfallData(waterfallChart(), 1, matrix).Series[0].Points
	if *points[1].X != 0 || *points[1].Value != 400 {
		t.Fatalf("소계가 바닥에서 그려지지 않는다: %v→%v", *points[1].X, *points[1].Value)
	}
	if *points[2].X != 400 || *points[2].Value != 250 {
		t.Errorf("소계 뒤가 이어지지 않는다: %v→%v", *points[2].X, *points[2].Value)
	}
}

// 표에 적힌 합계가 우리가 더한 값과 달라도 표를 믿는다. 그림이 표와
// 어긋나면 사람은 어느 쪽이 맞는지 알 수 없다.
func TestWaterfallTrustsTheSheetsOwnTotal(t *testing.T) {
	t.Parallel()
	matrix := [][]any{
		{"항목", "금액", "합계"},
		{"매출", 1000.0, nil},
		{"원가", -600.0, nil},
		{"영업이익", 999.0, true},
	}
	points := buildWaterfallData(waterfallChart(), 1, matrix).Series[0].Points
	if *points[2].Value != 999 {
		t.Errorf("합계를 %v 로 그렸다. 표에 적힌 999 여야 한다", *points[2].Value)
	}
}

func TestWaterfallSaysWhenThereIsNothingToDraw(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		matrix [][]any
	}{
		{"값 열이 없다", [][]any{{"항목"}, {"매출"}}},
		{"숫자가 없다", [][]any{{"항목", "금액"}, {"매출", "천만원"}}},
	} {
		data := buildWaterfallData(waterfallChart(), 1, testCase.matrix)
		if data.Warning == "" {
			t.Errorf("%s: 아무 말도 하지 않는다", testCase.name)
		}
	}
}
