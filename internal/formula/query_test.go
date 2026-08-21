package formula

import "testing"

// The sales table every QUERY example works over.
func salesCells() map[string]any {
	return map[string]any{
		"A1": "지역", "B1": "채널", "C1": "매출", "D1": "수량",
		"A2": "서울", "B2": "온라인", "C2": 4200000.0, "D2": 62.0,
		"A3": "부산", "B3": "오프라인", "C3": 1850000.0, "D3": 24.0,
		"A4": "서울", "B4": "오프라인", "C4": 3100000.0, "D4": 41.0,
		"A5": "대구", "B5": "온라인", "C5": 970000.0, "D5": 15.0,
		"A6": "서울", "B6": "온라인", "C6": 5600000.0, "D6": 88.0,
		"A7": "부산", "B7": "온라인", "C7": 2450000.0, "D7": 33.0,
	}
}

func queryMatrix(t *testing.T, formula string) [][]any {
	t.Helper()
	result := New().Evaluate(formula, salesCells())
	if result.Error != nil {
		t.Fatalf("%s: %v", formula, result.Error)
	}
	matrix, ok := result.Value.([][]any)
	if !ok {
		t.Fatalf("%s returned %#v, which is not a table", formula, result.Value)
	}
	return matrix
}

// select and where are what most QUERY formulas are: a filtered view of a
// table, with its header intact.
func TestQuerySelectsAndFilters(t *testing.T) {
	t.Parallel()
	matrix := queryMatrix(t, `=QUERY(A1:D7,"select A, C where B = '온라인' and C > 1000000 order by C desc")`)
	expected := [][]any{
		{"지역", "매출"},
		{"서울", 5600000.0},
		{"서울", 4200000.0},
		{"부산", 2450000.0},
	}
	if len(matrix) != len(expected) {
		t.Fatalf("got %d rows, want %d: %v", len(matrix), len(expected), matrix)
	}
	for row := range expected {
		for column := range expected[row] {
			if matrix[row][column] != expected[row][column] {
				t.Errorf("row %d column %d = %v, want %v", row, column, matrix[row][column], expected[row][column])
			}
		}
	}
}

// group by with an aggregate is the pivot table people write as a formula.
func TestQueryGroupsAndAggregates(t *testing.T) {
	t.Parallel()
	matrix := queryMatrix(t, `=QUERY(A1:D7,"select A, sum(C), count(A) group by A order by sum(C) desc")`)
	if len(matrix) != 4 {
		t.Fatalf("got %v", matrix)
	}
	if matrix[0][0] != "지역" || matrix[0][1] != "sum 매출" || matrix[0][2] != "count 지역" {
		t.Fatalf("header=%v", matrix[0])
	}
	if matrix[1][0] != "서울" || matrix[1][1] != 12900000.0 || matrix[1][2] != 3.0 {
		t.Fatalf("first group=%v", matrix[1])
	}
	if matrix[2][0] != "부산" || matrix[2][1] != 4300000.0 {
		t.Fatalf("second group=%v", matrix[2])
	}
	if matrix[3][0] != "대구" || matrix[3][1] != 970000.0 {
		t.Fatalf("third group=%v", matrix[3])
	}
}

func TestQueryAggregatesWithoutGrouping(t *testing.T) {
	t.Parallel()
	matrix := queryMatrix(t, `=QUERY(A1:D7,"select sum(C), avg(D), min(C), max(C), count(A)")`)
	if len(matrix) != 2 {
		t.Fatalf("got %v", matrix)
	}
	row := matrix[1]
	if row[0] != 18170000.0 || row[2] != 970000.0 || row[3] != 5600000.0 || row[4] != 6.0 {
		t.Fatalf("summary=%v", row)
	}
	if average, _ := toNumber(row[1]); average < 43.8 || average > 43.9 {
		t.Fatalf("average=%v", row[1])
	}
}

func TestQuerySupportsTheTextOperators(t *testing.T) {
	t.Parallel()
	for query, expected := range map[string]int{
		`select A where A contains '서'`:                         3,
		`select A where B starts with '온'`:                      4,
		`select A where A ends with '산'`:                        2,
		`select A where A matches '서울|대구'`:                      4,
		`select A where not A = '서울'`:                           3,
		`select A where A = '서울' or A = '대구'`:                   4,
		`select A where C is not null`:                          6,
		`select A where (A = '서울' and C > 4000000) or A = '대구'`: 3,
	} {
		matrix := queryMatrix(t, `=QUERY(A1:D7,"`+query+`")`)
		// Every result carries the header row, so the body is one row shorter.
		if len(matrix) != expected+1 && len(matrix) != expected {
			t.Errorf("%s returned %d rows: %v", query, len(matrix), matrix)
		}
	}
}

func TestQueryLimitsOffsetsAndLabels(t *testing.T) {
	t.Parallel()
	matrix := queryMatrix(t, `=QUERY(A1:D7,"select A, C order by C desc limit 2 offset 1 label C '금액'")`)
	if len(matrix) != 3 || matrix[0][1] != "금액" {
		t.Fatalf("got %v", matrix)
	}
	if matrix[1][1] != 4200000.0 || matrix[2][1] != 3100000.0 {
		t.Fatalf("rows=%v", matrix[1:])
	}
}

// A range without a header row must not lose its first line of data.
func TestQueryHandlesHeaderRows(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 10.0, "B1": "가", "A2": 20.0, "B2": "나"}
	matrix, ok := New().Evaluate(`=QUERY(A1:B2,"select A")`, cells).Value.([][]any)
	if !ok || len(matrix) != 2 || matrix[0][0] != 10.0 {
		t.Fatalf("headerless query returned %v", matrix)
	}
	// Declaring the header count explicitly wins over the guess.
	declared := queryMatrix(t, `=QUERY(A1:D7,"select A",0)`)
	if len(declared) != 7 || declared[0][0] != "지역" {
		t.Fatalf("declared headers returned %v", declared)
	}
}

func TestQueryReportsWhatItCannotDo(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		"select Z",                 // outside the range
		"select A, sum(C)",         // A is neither grouped nor aggregated
		"select A where",           // incomplete
		"select A where A ?? '서울'", // unknown operator
		"select A where A = '서울",   // unterminated text
		"pivot A",                  // unsupported clause
	} {
		result := New().Evaluate(`=QUERY(A1:D7,"`+query+`")`, salesCells())
		if result.Error == nil {
			t.Errorf("%q should be reported, got %v", query, result.Value)
		}
	}
	// A filter that matches nothing still shows the header, so the formula
	// reads as an empty table rather than a failure.
	empty := New().Evaluate(`=QUERY(A1:D7,"select A where C > 99999999")`, salesCells())
	matrix, ok := empty.Value.([][]any)
	if !ok || len(matrix) != 1 || matrix[0][0] != "지역" {
		t.Fatalf("empty result = %v (%v)", empty.Value, empty.Error)
	}
	// Without a header there is nothing at all to show.
	headerless := New().Evaluate(`=QUERY(A1:B2,"select A where A > 99")`, map[string]any{"A1": 10.0, "B1": "가", "A2": 20.0, "B2": "나"})
	if headerless.Error == nil || headerless.Error.Code != "#N/A" {
		t.Fatalf("headerless empty result = %v (%v)", headerless.Value, headerless.Error)
	}
}

// A sparkline is a chart that lives in one cell, so the formula produces the
// numbers and the appearance and the client draws it.
func TestSparklineDescribesAChart(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 3.0, "B1": 7.0, "C1": "", "D1": -2.0, "E1": "미정"}
	chart, ok := New().Evaluate("=SPARKLINE(A1:E1)", cells).Value.(map[string]any)
	if !ok {
		t.Fatalf("SPARKLINE returned %#v", New().Evaluate("=SPARKLINE(A1:E1)", cells).Value)
	}
	if chart["kanpic"] != SparklineMarker || chart["chart"] != "line" {
		t.Fatalf("chart=%v", chart)
	}
	values, isList := chart["values"].([]any)
	if !isList || len(values) != 3 || values[0] != 3.0 || values[2] != -2.0 {
		t.Fatalf("values=%v", chart["values"])
	}

	// Options are written as the two column array Sheets uses.
	styled, _ := New().Evaluate(`=SPARKLINE(A1:B1,{"charttype","column";"color","#5268a6";"negcolor","#c2413b";"max",10})`, cells).Value.(map[string]any)
	if styled["chart"] != "column" || styled["color"] != "#5268a6" || styled["negativeColor"] != "#c2413b" || styled["max"] != 10.0 {
		t.Fatalf("styled=%v", styled)
	}
	// An unknown chart type is reported rather than drawn as something else.
	if result := New().Evaluate(`=SPARKLINE(A1:B1,{"charttype","pie"})`, cells); result.Error == nil {
		t.Fatalf("unknown chart type returned %v", result.Value)
	}
	// A range with no numbers has nothing to draw.
	if result := New().Evaluate("=SPARKLINE(A1:A1)", map[string]any{"A1": "가"}); result.Error == nil {
		t.Fatalf("text only returned %v", result.Value)
	}
}
