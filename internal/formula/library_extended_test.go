package formula

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Every documented function has to exist: a catalog entry the engine cannot
// evaluate is worse than no entry at all, because the function list is what
// people browse before they type.
func TestEveryDocumentedFunctionIsImplemented(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1.0, "A2": 2.0, "A3": 3.0, "B1": 1.0, "B2": 2.0, "B3": 3.0}
	for _, entry := range Catalog() {
		result := New().Evaluate("="+entry.Name+"()", cells)
		if result.Error != nil && result.Error.Code == "#NAME?" {
			t.Errorf("%s is documented but not implemented", entry.Name)
		}
	}
}

func TestCatalogEntriesAreCompleteAndUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{})
	for _, entry := range Catalog() {
		if _, duplicate := seen[entry.Name]; duplicate {
			t.Errorf("%s is documented twice", entry.Name)
		}
		seen[entry.Name] = struct{}{}
		if entry.Category == "" || entry.Syntax == "" || entry.Summary == "" {
			t.Errorf("%s is missing catalog detail", entry.Name)
		}
		if !strings.HasPrefix(entry.Syntax, entry.Name+"(") {
			t.Errorf("%s syntax does not start with the function name: %s", entry.Name, entry.Syntax)
		}
	}
}

func evaluateNumber(t *testing.T, formula string, cells map[string]any) float64 {
	t.Helper()
	result := New().Evaluate(formula, cells)
	if result.Error != nil {
		t.Fatalf("%s: %v", formula, result.Error)
	}
	number, ok := toNumber(result.Value)
	if !ok {
		t.Fatalf("%s returned %v, which is not a number", formula, result.Value)
	}
	return number
}

func assertClose(t *testing.T, formula string, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %v, want %v", formula, got, want)
	}
}

// The loan and investment functions have to agree with the numbers people get
// from Excel and Google Sheets, including the sign convention.
func TestFinancialFunctionsMatchTheSpreadsheetConvention(t *testing.T) {
	t.Parallel()
	cells := map[string]any{}
	for _, testCase := range []struct {
		formula   string
		expected  float64
		tolerance float64
	}{
		{"=PMT(0.05/12,360,300000)", -1610.46, 0.01},
		{"=PMT(0.05/12,360,300000,0,1)", -1603.78, 0.01},
		{"=IPMT(0.05/12,1,360,300000)", -1250.00, 0.01},
		{"=PPMT(0.05/12,1,360,300000)", -360.46, 0.01},
		{"=FV(0.06/12,120,-200,-1000)", 34595.27, 0.01},
		{"=PV(0.08,20,-500)", 4909.07, 0.01},
		{"=NPER(0.05/12,-1610.46,300000)", 360, 0.05},
		{"=RATE(360,-1610.46,300000)*12", 0.05, 0.0001},
		{"=NPV(0.1,-100,60,60,60)", 44.7374, 0.001},
		{"=CUMIPMT(0.05/12,360,300000,1,12,0)", -14899.48, 0.01},
		{"=CUMPRINC(0.05/12,360,300000,1,12,0)", -4426.10, 0.01},
		{"=SLN(10000,1000,5)", 1800, 0.001},
		{"=SYD(10000,1000,5,1)", 3000, 0.001},
		{"=DDB(10000,1000,5,1)", 4000, 0.001},
		{"=EFFECT(0.05,12)", 0.051162, 0.000001},
		{"=NOMINAL(0.051162,12)", 0.05, 0.000001},
		{"=RRI(10,1000,2000)", 0.071773, 0.000001},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, cells), testCase.expected, testCase.tolerance)
	}
}

func TestInternalRateFunctionsUseTheirOwnRanges(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": -1000.0, "A2": 300.0, "A3": 400.0, "A4": 500.0,
		"B1": "2024-01-01", "B2": "2024-06-01", "B3": "2024-12-01", "B4": "2025-06-01",
	}
	assertClose(t, "IRR", evaluateNumber(t, "=IRR(A1:A4)", cells), 0.088963, 0.000001)
	// A guess must not be mistaken for another cash flow.
	assertClose(t, "IRR guess", evaluateNumber(t, "=IRR(A1:A4,0.5)", cells), 0.088963, 0.000001)
	assertClose(t, "XIRR", evaluateNumber(t, "=XIRR(A1:A4,B1:B4)", cells), 0.203256, 0.00001)
	assertClose(t, "XNPV", evaluateNumber(t, "=XNPV(0.1,A1:A4,B1:B4)", cells), 91.6797, 0.001)
	assertClose(t, "MIRR", evaluateNumber(t, "=MIRR(A1:A4,0.1,0.12)", cells), 0.098157, 0.000001)
}

func TestStatisticsCoverTheEverydayCases(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": 2.0, "A2": 4.0, "A3": 4.0, "A4": 4.0, "A5": 5.0, "A6": 5.0, "A7": 7.0, "A8": 9.0,
		"B1": 1.0, "B2": 2.0, "B3": 3.0, "B4": 4.0, "C1": 2.0, "C2": 4.0, "C3": 6.0, "C4": 8.0,
	}
	for formula, expected := range map[string]float64{
		"=STDEVP(A1:A8)":              2.0,
		"=VARP(A1:A8)":                4.0,
		"=VAR(A1:A8)":                 4.571428571,
		"=LARGE(A1:A8,2)":             7.0,
		"=SMALL(A1:A8,2)":             4.0,
		"=RANK(5,A1:A8)":              3.0,
		"=RANK(5,A1:A8,1)":            5.0,
		"=MODE(A1:A8)":                4.0,
		"=PERCENTILE(A1:A8,0.5)":      4.5,
		"=QUARTILE(A1:A8,2)":          4.5,
		"=COUNTUNIQUE(A1:A8)":         5.0,
		"=CORREL(B1:B4,C1:C4)":        1.0,
		"=SLOPE(C1:C4,B1:B4)":         2.0,
		"=INTERCEPT(C1:C4,B1:B4)":     0.0,
		"=FORECAST(5,C1:C4,B1:B4)":    10.0,
		"=SUMPRODUCT(B1:B4,C1:C4)":    60.0,
		"=SUBTOTAL(9,B1:B4)":          10.0,
		"=SUBTOTAL(101,B1:B4)":        2.5,
		"=AVERAGEIF(A1:A8,\">4\")":    6.5,
		"=MAXIFS(A1:A8,A1:A8,\"<5\")": 4.0,
		"=MINIFS(A1:A8,A1:A8,\">4\")": 5.0,
	} {
		assertClose(t, formula, evaluateNumber(t, formula, cells), expected, 0.000001)
	}
}

func TestLookupAndArrayFunctions(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": "사과", "B1": 1200.0,
		"A2": "배", "B2": 3000.0,
		"A3": "귤", "B3": 800.0,
	}
	for formula, expected := range map[string]any{
		`=XLOOKUP("배",A1:A3,B1:B3)`:          3000.0,
		`=XLOOKUP("망고",A1:A3,B1:B3,"없음")`:    "없음",
		`=XLOOKUP(1000,B1:B3,A1:A3,"없음",-1)`: "귤",
		`=XLOOKUP(1000,B1:B3,A1:A3,"없음",1)`:  "사과",
		`=XMATCH("귤",A1:A3)`:                 3.0,
		`=ROWS(A1:B3)`:                       3.0,
		`=COLUMNS(A1:B3)`:                    2.0,
		`=CHOOSE(2,"가","나","다")`:             "나",
		`=ADDRESS(2,3)`:                      "$C$2",
		`=LOOKUP(1500,B1:B3)`:                800.0,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	// UNIQUE and SEQUENCE produce arrays that spill.
	unique := New().Evaluate("=UNIQUE(A1:A3)", map[string]any{"A1": "가", "A2": "가", "A3": "나"})
	if unique.Error != nil {
		t.Fatalf("UNIQUE: %v", unique.Error)
	}
	rows, ok := unique.Value.([][]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("UNIQUE returned %v", unique.Value)
	}
	sequence := New().Evaluate("=SEQUENCE(2,3)", nil)
	if matrix, isMatrix := sequence.Value.([][]any); !isMatrix || len(matrix) != 2 || len(matrix[0]) != 3 || matrix[1][2] != 6.0 {
		t.Fatalf("SEQUENCE returned %v (%v)", sequence.Value, sequence.Error)
	}
}

// ROW and COLUMN report the cell the formula lives in, which is what makes
// them useful inside array and lookup work.
func TestRowAndColumnKnowTheirCell(t *testing.T) {
	t.Parallel()
	graph, err := New().Recalculate(map[string]CellState{
		"SHEET1!C5": {Formula: "=ROW()"},
		"SHEET1!C6": {Formula: "=COLUMN()"},
		"SHEET1!C7": {Formula: "=ROW(B9)"},
		"SHEET1!C8": {Formula: "=COLUMN(D2)"},
	}, []string{"SHEET1!C5", "SHEET1!C6", "SHEET1!C7", "SHEET1!C8"})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]float64{"SHEET1!C5": 5, "SHEET1!C6": 3, "SHEET1!C7": 9, "SHEET1!C8": 4}
	for _, cell := range graph.Cells {
		if cell.Error != nil {
			t.Errorf("%s: %v", cell.Address, cell.Error)
			continue
		}
		if number, _ := toNumber(cell.Value); number != expected[cell.Address] {
			t.Errorf("%s = %v, want %v", cell.Address, cell.Value, expected[cell.Address])
		}
	}
}

// A constant OFFSET or INDIRECT is an ordinary reference, dependencies and all.
func TestOffsetAndIndirectResolveConstantTargets(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1.0, "A2": 2.0, "A3": 3.0, "B2": 20.0}
	for formula, expected := range map[string]any{
		"=OFFSET(A1,1,0)":          2.0,
		"=OFFSET(A1,1,1)":          20.0,
		"=SUM(OFFSET(A1,0,0,3,1))": 6.0,
		`=INDIRECT("A2")`:          2.0,
		`=SUM(INDIRECT("A1:A3"))`:  6.0,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil || result.Value != expected {
			t.Errorf("%s = %v (%v), want %v", formula, result.Value, result.Error, expected)
		}
	}
	// The cell OFFSET lands on is a real dependency, so editing it recalculates.
	if dependencies := New().Evaluate("=OFFSET(A1,1,0)", cells).Dependencies; !containsValue(dependencies, "A2") {
		t.Fatalf("OFFSET dependencies=%v", dependencies)
	}
	// A computed target still resolves, and the formula is marked volatile so
	// the workbook keeps it fresh.
	computed := New().Evaluate(`=INDIRECT("A"&2)`, cells)
	if computed.Error != nil || computed.Value != 2.0 {
		t.Fatalf("computed INDIRECT = %v (%v)", computed.Value, computed.Error)
	}
	if !IsVolatile(`=INDIRECT("A"&2)`) || !IsVolatile("=TODAY()") || !IsVolatile("=NOW\t()") || !IsVolatile("=INDIRECT\n(\"A2\")") || IsVolatile("=SUM(A1:A3)") {
		t.Fatal("volatility detection is wrong")
	}
}

func TestExtendedTextAndDateFunctions(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1234567.891, "A2": "2024-03-15", "A3": "kanpic@corp.example"}
	for formula, expected := range map[string]any{
		`=TEXT(A1,"#,##0")`:                       "1,234,568",
		`=TEXT(A1,"#,##0.00")`:                    "1,234,567.89",
		`=TEXT(0.256,"0.0%")`:                     "25.6%",
		`=TEXT(A2,"yyyy-mm-dd")`:                  "2024-03-15",
		`=TEXT(A2,"yyyy년 mm월")`:                   "2024년 03월",
		`=FIXED(1234.5,1)`:                        "1,234.5",
		`=REPLACE("2024-01-01",1,4,"2025")`:       "2025-01-01",
		`=EXACT("Kanpic","kanpic")`:               false,
		`=REGEXMATCH(A3,"@corp\.")`:               true,
		`=REGEXEXTRACT(A3,"@(.+)$")`:              "corp.example",
		`=REGEXREPLACE(A3,"@.+$","")`:             "kanpic",
		`=JOIN("-","a","b","c")`:                  "a-b-c",
		`=CHAR(65)`:                               "A",
		`=CODE("A")`:                              65.0,
		`=EDATE(A2,1)`:                            "2024-04-15",
		`=EOMONTH(A2,0)`:                          "2024-03-31",
		`=EOMONTH(A2,-1)`:                         "2024-02-29",
		`=DAYS("2024-03-31",A2)`:                  16.0,
		`=DATEDIF("2023-01-15",A2,"M")`:           14.0,
		`=DATEDIF("2023-01-15",A2,"Y")`:           1.0,
		`=NETWORKDAYS("2024-03-04","2024-03-08")`: 5.0,
		`=NETWORKDAYS("2024-03-04","2024-03-10")`: 5.0,
		`=WORKDAY("2024-03-08",1)`:                "2024-03-11",
		`=YEARFRAC("2024-01-01","2024-07-01")`:    0.5,
		`=WEEKNUM(A2)`:                            11.0,
		// 시각은 하루를 1 로 본 분수다. 13시 5분은 47100초이므로
		// 47100/86400 이다. 값은 열다섯 자리로 다듬어 나온다. 예전에는
		// "13:05:00" 이라는 글자였는데, 그러면 시각에 더할 수가 없었다.
		`=TIME(13,5,0)`:       0.545138888888889,
		`=HOUR("13:05:00")`:   13.0,
		`=SPLIT("a,b,c",",")`: nil,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if expected == nil {
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %#v, want %#v", formula, result.Value, expected)
		}
	}
	if split := New().Evaluate(`=SPLIT("a,b,c",",")`, nil); len(split.Value.([][]any)[0]) != 3 {
		t.Fatalf("SPLIT returned %v", split.Value)
	}
}

func TestLogicFunctions(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 10.0, "A2": "텍스트"}
	for formula, expected := range map[string]any{
		`=IFS(A1>100,"큼",A1>5,"보통",TRUE,"작음")`: "보통",
		`=IFS(A1>100,"큼")`:                     nil,
		`=SWITCH(2,1,"하나",2,"둘","기타")`:         "둘",
		`=SWITCH(9,1,"하나",2,"둘","기타")`:         "기타",
		`=XOR(TRUE,FALSE)`:                     true,
		`=XOR(TRUE,TRUE)`:                      false,
		`=IFNA(NA(),"대체")`:                     "대체",
		`=IFNA(A1,"대체")`:                       10.0,
		`=ISERROR(1/0)`:                        true,
		`=ISERROR(A1)`:                         false,
		`=ISNA(NA())`:                          true,
		`=ISERR(NA())`:                         false,
		`=ISNUMBER(A1)`:                        true,
		`=ISNUMBER(A2)`:                        false,
		`=ISTEXT(A2)`:                          true,
		`=ISBLANK(Z9)`:                         true,
		`=ISEVEN(A1)`:                          true,
		`=ISEMAIL("a@b.co")`:                   true,
		`=N(TRUE)`:                             1.0,
	} {
		result := New().Evaluate(formula, cells)
		if expected == nil {
			if result.Error == nil {
				t.Errorf("%s should report an error, got %v", formula, result.Value)
			}
			continue
		}
		if result.Error != nil || result.Value != expected {
			t.Errorf("%s = %v (%v), want %v", formula, result.Value, result.Error, expected)
		}
	}
}

func TestMathFunctions(t *testing.T) {
	t.Parallel()
	for formula, expected := range map[string]float64{
		"=CEILING(4.2,1)":   5,
		"=CEILING(2.5,0.5)": 2.5,
		"=FLOOR(4.8,1)":     4,
		"=MROUND(17,5)":     15,
		"=TRUNC(3.987,2)":   3.98,
		"=SIGN(-4)":         -1,
		"=QUOTIENT(17,5)":   3,
		"=GCD(24,36)":       12,
		"=LCM(4,6)":         12,
		"=SUMSQ(3,4)":       25,
		"=FACT(5)":          120,
		"=COMBIN(5,2)":      10,
		"=PERMUT(5,2)":      20,
		"=EVEN(3)":          4,
		"=ODD(4)":           5,
		"=LOG(8,2)":         3,
		"=DEGREES(PI())":    180,
	} {
		assertClose(t, formula, evaluateNumber(t, formula, nil), expected, 0.000001)
	}
}

func containsValue(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// An argument left empty is not a zero. `FIXED(1234.5,,TRUE)` used to slide
// TRUE into the decimals slot and print "1,234.5"; the skipped slot has to
// survive all the way to the function that owns the default.
func TestOmittedArgumentsKeepTheirPlace(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1.0, "A2": 2.0, "A3": 3.0, "B1": "x", "B2": "y", "B3": "z"}
	for formula, expected := range map[string]any{
		`=FIXED(1234.5,,TRUE)`:       "1234.50",
		`=SUM(1,,2)`:                 3.0,
		`=XLOOKUP(2,A1:A3,B1:B3,,0)`: "y",
		`=XMATCH(2,A1:A3,,1)`:        2.0,
		`=ADDRESS(1,2,,"Sheet1")`:    "Sheet1!$B$1",
		`=OFFSET(A1,1,0,,1)`:         2.0,
		// Payments at the start of the period carry no interest in the first
		// one; reading the trailing 1 as the future value gave -100 instead.
		`=IPMT(0.1,1,3,1000,,1)`:   0.0,
		`=SPLIT("a,b",",",,FALSE)`: nil,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if expected != nil && result.Value != expected {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	// A skipped count means the whole extent, not none of it.
	if result := New().Evaluate(`=SEQUENCE(2,,5)`, cells); result.Error != nil {
		t.Errorf("SEQUENCE with a skipped column count: %v", result.Error)
	} else if matrix, ok := result.Value.([][]any); !ok || len(matrix) != 2 || matrix[0][0] != 5.0 || matrix[1][0] != 6.0 {
		t.Errorf("SEQUENCE(2,,5) = %v", result.Value)
	}
	// INDEX with a skipped column returns the whole row.
	if result := New().Evaluate(`=INDEX(A1:B3,2,)`, cells); result.Error != nil {
		t.Errorf("INDEX with a skipped column: %v", result.Error)
	}
}

func TestTextBeforeAndTextAfterFindTheDelimiter(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": "이름: 홍길동"}
	for formula, expected := range map[string]string{
		`=TEXTBEFORE(A1,": ")`:                       "이름",
		`=TEXTAFTER(A1,": ")`:                        "홍길동",
		`=TEXTBEFORE("a-b-c","-",2)`:                 "a-b",
		`=TEXTAFTER("a-b-c","-",-1)`:                 "c",
		`=TEXTBEFORE("abc","-",1,0,0,"없음")`:          "없음",
		`=TEXTAFTER("A-b","-",1,1)`:                  "b",
		`=TEXTBEFORE("a, b and c",{", "," and "},2)`: "a, b",
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %v, want %q", formula, result.Value, expected)
		}
	}
	// Without a match and without a fallback the answer is #N/A, not an empty
	// string: an empty string is a real result that would hide the miss.
	if result := New().Evaluate(`=TEXTBEFORE("abc","-")`, cells); result.Error == nil || result.Error.Code != "#N/A" {
		t.Errorf("TEXTBEFORE without a match = %v, %v", result.Value, result.Error)
	}
}

func TestTextSplitBuildsATable(t *testing.T) {
	t.Parallel()
	result := New().Evaluate(`=TEXTSPLIT("a,b;c,d",",",";")`, map[string]any{})
	if result.Error != nil {
		t.Fatalf("TEXTSPLIT: %v", result.Error)
	}
	matrix, ok := result.Value.([][]any)
	if !ok || len(matrix) != 2 || len(matrix[0]) != 2 || matrix[1][1] != "d" {
		t.Fatalf("TEXTSPLIT = %v", result.Value)
	}
	// A short row is padded so the result stays rectangular.
	padded := New().Evaluate(`=TEXTSPLIT("a,b;c",",",";")`, map[string]any{})
	if padded.Error != nil {
		t.Fatalf("ragged TEXTSPLIT: %v", padded.Error)
	}
	if matrix, ok := padded.Value.([][]any); !ok || len(matrix) != 2 || len(matrix[1]) != 2 || matrix[1][1] != nil {
		t.Fatalf("ragged TEXTSPLIT = %v", padded.Value)
	}
}

func TestStackingAndSlicingArrays(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": "지역", "B1": "매출", "A2": "부산", "B2": 80.0, "A3": "서울", "B3": 120.0, "A4": "대구", "B4": 95.0,
	}
	for formula, expected := range map[string][][]any{
		`=VSTACK(A2:B2,A3:B3)`:    {{"부산", 80.0}, {"서울", 120.0}},
		`=HSTACK(A2:A3,B2:B3)`:    {{"부산", 80.0}, {"서울", 120.0}},
		`=TAKE(A1:B4,2)`:          {{"지역", "매출"}, {"부산", 80.0}},
		`=TAKE(A1:B4,-2)`:         {{"서울", 120.0}, {"대구", 95.0}},
		`=DROP(A1:B4,1)`:          {{"부산", 80.0}, {"서울", 120.0}, {"대구", 95.0}},
		`=DROP(A1:B4,,-1)`:        {{"지역"}, {"부산"}, {"서울"}, {"대구"}},
		`=CHOOSEROWS(A1:B4,1,-1)`: {{"지역", "매출"}, {"대구", 95.0}},
		`=CHOOSECOLS(A1:B4,2)`:    {{"매출"}, {80.0}, {120.0}, {95.0}},
		`=SORTBY(A2:B4,B2:B4,-1)`: {{"서울", 120.0}, {"대구", 95.0}, {"부산", 80.0}},
		// 한 줄을 접는다. 마지막 줄의 빈 자리는 채울 값이 있으면 그것으로,
		// 없으면 빈 칸으로 둔다.
		`=WRAPROWS(A2:A4,2,"-")`: {{"부산", "서울"}, {"대구", "-"}},
		`=WRAPCOLS(A2:A4,2,"-")`: {{"부산", "대구"}, {"서울", "-"}},
		`=WRAPROWS(A2:A4,3)`:     {{"부산", "서울", "대구"}},
		`=EXPAND(A2:B2,2,3,0)`:   {{"부산", 80.0, 0.0}, {0.0, 0.0, 0.0}},
		`=EXPAND(A2:B2,1,3)`:     {{"부산", 80.0, nil}},
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if !reflect.DeepEqual(result.Value, expected) {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	// 접을 것이 이미 두 줄이면 무엇을 어떤 차례로 읽었는지 사람이 알 수 없다.
	if result := New().Evaluate(`=WRAPROWS(A1:B4,2)`, cells); result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Errorf("두 줄을 접었다: %v (%v)", result.Value, result.Error)
	}
	if result := New().Evaluate(`=WRAPROWS(A2:A4,0)`, cells); result.Error == nil || result.Error.Code != "#NUM!" {
		t.Errorf("한 줄 길이 0: %v (%v)", result.Value, result.Error)
	}
	// EXPAND 는 넓히는 함수다. 줄이는 것은 자르는 것이고 TAKE 가 한다. 여기서
	// 조용히 잘라 내면 사라진 자료를 아무도 알아채지 못한다.
	if result := New().Evaluate(`=EXPAND(A1:B4,2,2)`, cells); result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Errorf("EXPAND 가 잘라 냈다: %v (%v)", result.Value, result.Error)
	}
	// 채울 값을 정하지 않으면 빈 칸이다. 구글 시트와 엑셀은 #N/A 를 채우지만
	// kanpic 의 오류는 칸 하나가 통째로 가지는 것이라 배열 안에 담을 자리가
	// 없다. 오류처럼 보이는 글자를 넣으면 ISNA 가 거짓이 되어 더 나쁘다.
	if result := New().Evaluate(`=ISBLANK(INDEX(WRAPROWS(A2:A4,2),2,2))`, cells); result.Error != nil || result.Value != true {
		t.Errorf("채우지 않은 자리=%v (%v)", result.Value, result.Error)
	}
	// Stacking uneven parts keeps the union of the shapes and leaves the
	// corner blank rather than failing the whole call.
	if result := New().Evaluate(`=VSTACK(A2:B2,A3:A3)`, cells); result.Error != nil {
		t.Errorf("ragged VSTACK: %v", result.Error)
	} else if matrix, ok := result.Value.([][]any); !ok || len(matrix) != 2 || matrix[1][1] != nil {
		t.Errorf("ragged VSTACK = %v", result.Value)
	}
	// A key that does not line up with the array is refused, because guessing
	// which row belongs to which key would sort the table into nonsense.
	if result := New().Evaluate(`=SORTBY(A2:B4,B2:B3)`, cells); result.Error == nil {
		t.Errorf("SORTBY with a short key = %v", result.Value)
	}
}

// LET names the steps of a calculation so a long formula reads in the order it
// is computed and each step is calculated once.
func TestLetNamesTheStepsOfACalculation(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 10.0, "A2": 20.0, "A3": 30.0}
	for formula, expected := range map[string]any{
		`=LET(x,5,x*2)`:                                  10.0,
		`=LET(x,5,y,x+1,x*y)`:                            30.0,
		`=LET(total,SUM(A1:A3),total/COUNT(A1:A3))`:      20.0,
		`=LET(x,A1,IF(x>5,"큼","작음"))`:                    "큼",
		`=LAMBDA(x,x+1)(4)`:                              5.0,
		`=LET(double,LAMBDA(x,x*2),double(21))`:          42.0,
		`=LET(area,LAMBDA(w,h,w*h),area(3,4)+area(2,2))`: 16.0,
		`=SUM(LET(x,2,x),3)`:                             5.0,
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if result.Value != expected {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	// A name that reads as a cell reference is refused, because the formula
	// would mean something different depending on where it sits.
	if result := New().Evaluate(`=LET(A1,5,A1)`, cells); result.Error == nil {
		t.Errorf("LET with a cell reference for a name = %v", result.Value)
	}
	// The cells a named step reads are still dependencies of the formula.
	if result := New().Evaluate(`=LET(total,SUM(A1:A3),total)`, cells); len(result.Dependencies) != 3 {
		t.Errorf("LET dependencies = %v", result.Dependencies)
	}
	// A LAMBDA on its own is a function, not something a cell can hold.
	if result := New().Evaluate(`=LAMBDA(x,x)`, cells); result.Error == nil {
		t.Errorf("a bare LAMBDA = %v", result.Value)
	}
}

func TestLambdaHelpersWalkAnArray(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1.0, "A2": 2.0, "A3": 3.0, "B1": 10.0, "B2": 20.0, "B3": 30.0}
	for formula, expected := range map[string][][]any{
		`=MAP(A1:A3,LAMBDA(x,x*2))`:          {{2.0}, {4.0}, {6.0}},
		`=MAP(A1:A3,B1:B3,LAMBDA(x,y,x+y))`:  {{11.0}, {22.0}, {33.0}},
		`=BYROW(A1:B3,LAMBDA(row,SUM(row)))`: {{11.0}, {22.0}, {33.0}},
		`=BYCOL(A1:B3,LAMBDA(col,SUM(col)))`: {{6.0, 60.0}},
		`=SCAN(0,A1:A3,LAMBDA(acc,x,acc+x))`: {{1.0}, {3.0}, {6.0}},
	} {
		result := New().Evaluate(formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if !reflect.DeepEqual(result.Value, expected) {
			t.Errorf("%s = %v, want %v", formula, result.Value, expected)
		}
	}
	if result := New().Evaluate(`=REDUCE(0,A1:A3,LAMBDA(acc,x,acc+x))`, cells); result.Error != nil || result.Value != 6.0 {
		t.Errorf("REDUCE = %v, %v", result.Value, result.Error)
	}
	// A named LAMBDA can be handed to MAP, which is the point of having both.
	if result := New().Evaluate(`=LET(tax,LAMBDA(v,v*0.1),SUM(MAP(B1:B3,tax)))`, cells); result.Error != nil || result.Value != 6.0 {
		t.Errorf("MAP with a named LAMBDA = %v, %v", result.Value, result.Error)
	}
	// Without a function to apply there is nothing to do, and quietly
	// returning the array would hide the mistake.
	if result := New().Evaluate(`=MAP(A1:A3,5)`, cells); result.Error == nil {
		t.Errorf("MAP without a LAMBDA = %v", result.Value)
	}
}

// The product explains each error code in Korean. That table cannot be
// generated from here, so this test is where a new code gets noticed: adding
// one means adding an explanation in web/src/lib/formulaError.ts too.
func TestErrorCodesAreTheOnesTheProductExplains(t *testing.T) {
	t.Parallel()
	documented := map[string]struct{}{
		"#CIRC!": {}, "#DIV/0!": {}, "#ERROR!": {}, "#N/A": {}, "#NAME?": {},
		"#NULL!": {}, "#NUM!": {}, "#REF!": {}, "#SPILL!": {}, "#VALUE!": {},
	}
	if len(ErrorCodes) != len(documented) {
		t.Fatalf("the engine has %d error codes and the product explains %d", len(ErrorCodes), len(documented))
	}
	for _, code := range ErrorCodes {
		if _, known := documented[code]; !known {
			t.Errorf("%s has no explanation in web/src/lib/formulaError.ts", code)
		}
		// Every code has to survive being written as a literal, because a
		// formula can name one directly and a cell can hold one.
		if result := New().Evaluate("="+code, map[string]any{}); result.Error == nil || result.Error.Code != code {
			t.Errorf("%s as a literal = %v, %v", code, result.Value, result.Error)
		}
	}
}

// kanpic keeps a date as its written form rather than as a serial number, so
// the two most common date formulas in any spreadsheet - how many days apart,
// and a week from now - both came back as #VALUE!.
func TestDateArithmeticAnswersDaysApartAndShiftsADate(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": "2026-08-23",
		"A2": "2026-09-01",
		"A3": "2026-08-23 09:30:00",
		"A4": "2026/08/23",
		"A5": "7",
		"A6": "연필",
	}
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{"=A2-A1", float64(9)},
		{"=A1-A2", float64(-9)},
		{"=A1+7", "2026-08-30"},
		{"=A1-1", "2026-08-22"},
		{"=7+A1", "2026-08-30"},
		// 월과 해를 넘어가도 달력대로 움직인다.
		{"=A1+130", "2026-12-31"},
		{"=A1+131", "2027-01-01"},
		// 시간이 붙은 값은 시간을 잃지 않는다.
		{"=A3+1", "2026-08-24 09:30:00"},
		// 쓰인 모양이 다르면 그 모양 그대로 돌려준다.
		{"=A4+1", "2026/08/24"},
		// 숫자끼리는 지금까지처럼 계산한다.
		{"=1+2", float64(3)},
		{"=A5+1", float64(8)},
		{"=A6&\"칸\"", "연필칸"},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Fatalf("%s: %v", testCase.formula, result.Error)
		}
		if result.Value != testCase.want {
			t.Fatalf("%s = %#v, want %#v", testCase.formula, result.Value, testCase.want)
		}
	}
	for _, formula := range []string{
		// 날짜 둘을 더하는 것은 뜻이 없다. 직렬 번호가 없으므로 큰 숫자를
		// 내놓느니 오류를 낸다.
		"=A1+A2",
		// 날짜에서 빼는 것이지 날짜를 빼는 것이 아니다.
		"=7-A1",
		"=A1*2",
		"=A6-A1",
		// 기간이 나타낼 수 있는 범위를 넘어서면 다른 세기로 넘어가 버린다.
		"=A1+9000000",
	} {
		if result := New().Evaluate(formula, cells); result.Error == nil {
			t.Fatalf("%s produced %#v instead of an error", formula, result.Value)
		}
	}
}

// `=0.1+0.2` reading 0.30000000000000004 is the oldest way a spreadsheet can
// look broken. Every spreadsheet hides the binary remainder the same way: a
// result carries fifteen significant decimal digits.
func TestNumericResultsCarryFifteenSignificantDigits(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{"=0.1+0.2", 0.3},
		{"=0.3-0.1", 0.2},
		{"=1.1*3", 3.3},
		{"=(0.1+0.2)*10", float64(3)},
		{"=SUM(0.1,0.2,0.3)", 0.6},
		// 열다섯 자리까지는 그대로 남는다. 반올림이 값을 옮기면 안 된다.
		{"=1/3", 0.333333333333333},
		{"=2/3", 0.666666666666667},
		{"=1234567.89*100", 123456789.0},
		// 비교도 같은 자리에서 이루어져야 `=IF(합계=예상,…)` 이 맞는 답을 낸다.
		{"=0.1+0.2=0.3", true},
		{"=(0.1+0.2)>0.3", false},
		{"=(0.1+0.2)>=0.3", true},
	} {
		result := New().Evaluate(testCase.formula, map[string]any{})
		if result.Error != nil {
			t.Fatalf("%s: %v", testCase.formula, result.Error)
		}
		if result.Value != testCase.want {
			t.Fatalf("%s = %#v, want %#v", testCase.formula, result.Value, testCase.want)
		}
	}
}

// 지수 표기는 값으로 칠 때는 받아들이면서 수식 안에서는 "unexpected token E"
// 였다. 아주 크거나 작은 수를 다루면 바로 막힌다.
func TestFormulasAcceptScientificNotation(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"E3": 7, "A1": 2}
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{"=2E3", float64(2000)},
		{"=1.5E+3", float64(1500)},
		{"=1E-2", 0.01},
		{"=1E-10*2", 2e-10},
		{"=2e2+1", float64(201)},
		// E3 은 셀 참조다. 숫자에 삼켜서는 안 된다.
		{"=E3", 7},
		{"=A1*E3", float64(14)},
		{"=SUM(A1:E3)", float64(9)},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Fatalf("%s: %v", testCase.formula, result.Error)
		}
		if result.Value != testCase.want {
			t.Fatalf("%s = %#v, want %#v", testCase.formula, result.Value, testCase.want)
		}
	}
}

// 넘쳐 버린 결과는 JSON 으로 쓸 수 없어 브라우저에 빈 응답이 갔다.
func TestOverflowReportsNumberRangeInsteadOfAnUnwritableValue(t *testing.T) {
	t.Parallel()
	for _, formula := range []string{"=1E308*10", "=-1E308*10", "=10^400", "=SEQUENCE(2,1)*1E308*10"} {
		result := New().Evaluate(formula, map[string]any{})
		if result.Error == nil {
			t.Fatalf("%s produced %#v instead of an error", formula, result.Value)
		}
		if result.Error.Code != "#NUM!" {
			t.Fatalf("%s reported %s", formula, result.Error.Code)
		}
		if _, err := json.Marshal(result); err != nil {
			t.Fatalf("%s: the result cannot be written as JSON: %v", formula, err)
		}
	}
}

// TREND 과 FORECAST 는 셈이 같고 **인수 차례가 다르다**. 예전에는 둘을 한
// 갈래로 묶어 두어, 엑셀과 시트의 문서대로 TREND 를 쓰면 인수가 조용히
// 뒤바뀌었다. 오류도 없이 그럴듯한 수가 나오는 쪽이 더 나쁘다.
//
//	TREND({2;4;6},{1;2;3},{7;8;9}) 은 14, 16, 18 이다.
//	묶여 있던 시절에는 -4 하나가 나왔다.
func TestTrendTakesItsArgumentsInTheOrderSheetsDoes(t *testing.T) {
	t.Parallel()
	cells := map[string]any{}
	// 구할 x 를 하나만 주면 값 하나가 나온다.
	assertClose(t, "TREND one", evaluateNumber(t, "=TREND({2;4;6},{1;2;3},7)", cells), 14, 1e-9)
	// FORECAST 는 구할 x 가 맨 앞이고, 같은 답을 낸다.
	assertClose(t, "FORECAST", evaluateNumber(t, "=FORECAST(7,{2;4;6},{1;2;3})", cells), 14, 1e-9)
	// 알려진 x 를 적지 않으면 1, 2, 3… 을 쓴다.
	assertClose(t, "TREND default x", evaluateNumber(t, "=INDEX(TREND({2;4;6}),3)", cells), 6, 1e-9)
	// b 를 거짓으로 두면 직선이 원점을 지난다. 기울기는 34/14 이다.
	assertClose(t, "TREND no intercept", evaluateNumber(t, "=TREND({3;5;7},{1;2;3},7,FALSE)", cells), 17, 1e-9)

	// 결과는 구할 x 와 같은 모양으로 돌아온다.
	result := New().Evaluate("=TREND({2;4;6},{1;2;3},{7;8;9})", cells)
	if result.Error != nil {
		t.Fatalf("TREND array: %v", result.Error)
	}
	matrix, ok := result.Value.([][]any)
	if !ok {
		t.Fatalf("TREND returned %T, want a column of three values", result.Value)
	}
	if len(matrix) != 3 || len(matrix[0]) != 1 {
		t.Fatalf("TREND returned %d×%d, want 3×1", len(matrix), len(matrix[0]))
	}
	for index, want := range []float64{14, 16, 18} {
		number, _ := toNumber(matrix[index][0])
		assertClose(t, "TREND array", number, want, 1e-9)
	}

	// 알려진 x 와 y 의 개수가 다르면 조용히 넘어가지 않는다.
	if mismatched := New().Evaluate("=TREND({2;4;6},{1;2},7)", cells); mismatched.Error == nil {
		t.Fatalf("TREND with mismatched series returned %v, want an error", mismatched.Value)
	}
}

// 이름 끝에 A 가 붙은 통계 함수는 숫자가 아닌 값을 0 으로 센다. 붙지 않은
// 쪽은 건너뛴다. 엑셀과 시트가 그렇게 나눠 놓았고, AVERAGEA 는 처음부터
// 그렇게 세고 있었는데 STDEVA 와 VARA 는 건너뛰고 있었다.
//
// 1, 2, 3, "x" 는 A 가 붙으면 1, 2, 3, 0 이 된다.
func TestTheAVariantsCountTextAsZero(t *testing.T) {
	t.Parallel()
	cells := map[string]any{}
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		{`=VARA(1,2,3,"x")`, 5.0 / 3.0},
		{`=STDEVA(1,2,3,"x")`, 1.2909944487358056},
		{`=VARPA(1,2,3,"x")`, 1.25},
		{`=STDEVPA(1,2,3,"x")`, 1.118033988749895},
		{`=AVERAGEA(1,2,3,"x")`, 1.5},
		// A 가 붙지 않은 쪽은 글자를 건너뛰므로 1, 2, 3 만 센다.
		{`=VAR(1,2,3,"x")`, 1},
		{`=STDEV(1,2,3,"x")`, 1},
		{`=VARP(1,2,3,"x")`, 2.0 / 3.0},
		// 참은 1, 거짓은 0 으로 센다.
		{`=VARA(1,2,3,TRUE)`, 11.0 / 12.0},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, cells), testCase.want, 1e-9)
	}
}

// 엑셀과 시트에서 흔히 쓰는 선택 인수 몇 가지가 빠져 있었다. 문서를 보고
// 그대로 적으면 #VALUE! 가 났다.
//
//	SUBSTITUTE(글, 찾을 값, 바꿀 값, [몇 번째])
//	FIND / SEARCH(찾을 값, 글, [시작 위치])
//	WEEKDAY(날짜, [유형])
//
// CONCATENATE 는 아예 없었다. CONCAT 만 있어서, 옛 이름을 적으면 #NAME? 이
// 났다. 엑셀을 배운 사람은 대개 옛 이름을 먼저 떠올린다.
func TestTheOptionalArgumentsSheetsDocumentsAreAccepted(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		// 몇 번째로 나온 것만 바꾼다.
		{`=SUBSTITUTE("aaa","a","b",1)`, "baa"},
		{`=SUBSTITUTE("aaa","a","b",2)`, "aba"},
		{`=SUBSTITUTE("aaa","a","b",3)`, "aab"},
		// 그만큼 나오지 않으면 원래 글이 그대로 나온다.
		{`=SUBSTITUTE("aaa","a","b",4)`, "aaa"},
		{`=SUBSTITUTE("aaa","a","b")`, "bbb"},
		// 글자 수로 세므로 한글도 자리가 맞는다.
		{`=SUBSTITUTE("가나가","가","다",2)`, "가나다"},

		// 시작 위치를 주면 그 앞은 건너뛰고, 자리는 글 전체에서 센다.
		{`=FIND("a","banana")`, 2.0},
		{`=FIND("a","banana",3)`, 4.0},
		{`=FIND("a","banana",5)`, 6.0},
		{`=SEARCH("A","banana",3)`, 4.0},
		{`=FIND("나","가나다나",3)`, 4.0},

		// 한 주가 어느 요일에 시작하는지 고른다. 2024-01-01 은 월요일이다.
		{`=WEEKDAY("2024-01-01")`, 2.0},
		{`=WEEKDAY("2024-01-01",1)`, 2.0},
		{`=WEEKDAY("2024-01-01",2)`, 1.0},
		{`=WEEKDAY("2024-01-01",3)`, 0.0},
		{`=WEEKDAY("2024-01-01",11)`, 1.0},
		{`=WEEKDAY("2024-01-01",12)`, 7.0},
		{`=WEEKDAY("2024-01-01",17)`, 2.0},

		{`=CONCATENATE("a","b","c")`, "abc"},
		{`=CONCAT("a","b")`, "ab"},
	} {
		result := New().Evaluate(testCase.formula, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}

	// 말이 되지 않는 값은 조용히 넘어가지 않는다.
	for _, testCase := range []struct {
		formula string
		code    string
	}{
		{`=SUBSTITUTE("aaa","a","b",0)`, "#VALUE!"},
		{`=FIND("a","banana",7)`, "#VALUE!"},
		{`=WEEKDAY("2024-01-01",4)`, "#NUM!"},
	} {
		result := New().Evaluate(testCase.formula, map[string]any{})
		if result.Error == nil {
			t.Errorf("%s = %v, want %s", testCase.formula, result.Value, testCase.code)
			continue
		}
		if result.Error.Code != testCase.code {
			t.Errorf("%s = %s, want %s", testCase.formula, result.Error.Code, testCase.code)
		}
	}
}

// 반올림은 사람이 적은 십진수를 기준으로 해야 한다. 이진 실수로 셈하면
// 1.005 는 실제로 1.00499999999999989… 로 담기므로 두 자리에서 반올림해도
// 1.00 이 되지만, 엑셀과 시트는 1.01 을 낸다. 돈을 다루는 표에서는 1원이
// 어긋나고, 어긋난 줄을 눈으로 찾아낼 방법이 없다.
//
// 먼저 열다섯 자리로 다듬은 다음 십진수 그대로 자른다. 다듬는 단계가
// 있어야 =ROUNDUP(0.1+0.2,1) 이 0.4 가 아니라 0.3 이 된다 — 0.1+0.2 는
// 0.30000000000000004 로 담기기 때문이다. 두 경우가 서로 반대 방향이라,
// 한쪽만 맞추면 다른 쪽이 어긋난다.
func TestRoundingFollowsTheDecimalPeopleTyped(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		// 이진 실수로는 1.00 이 되던 것들. 돈에서 흔히 만난다.
		{"=ROUND(1.005,2)", 1.01},
		{"=ROUND(2.675,2)", 2.68},
		{"=ROUND(0.615,2)", 0.62},
		{"=ROUND(1.115,2)", 1.12},
		{"=ROUND(8.475,2)", 8.48},
		{"=ROUND(-1.005,2)", -1.01},

		// 다듬는 단계가 없으면 0.4 가 되어 버린다.
		{"=ROUNDUP(0.1+0.2,1)", 0.3},
		{"=ROUND(0.1+0.2,1)", 0.3},

		// 중간값은 0 에서 먼 쪽으로 간다.
		{"=ROUND(2.5,0)", 3},
		{"=ROUND(-2.5,0)", -3},
		{"=ROUND(-0.5,0)", -1},

		// 자릿수가 음수면 정수 쪽으로 자른다.
		{"=ROUND(1234,-2)", 1200},

		// 올림과 버림은 중간이 아니어도 방향이 정해져 있다.
		{"=ROUNDUP(1.001,2)", 1.01},
		{"=ROUNDDOWN(1.009,2)", 1},
		{"=ROUNDDOWN(1.005,2)", 1},
		{"=ROUND(1.0049,2)", 1},

		// 자릿수를 적지 않으면 정수로 간다.
		{"=ROUND(1.234)", 1},
		{"=ROUNDUP(1.234)", 2},
		{"=ROUNDDOWN(1.789)", 1},

		{"=ROUND(12345.6789,3)", 12345.679},
		{"=ROUND(0,2)", 0},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, map[string]any{}), testCase.want, 1e-12)
	}
}

// CEILING·FLOOR 는 음수를 양의 배수로 맞추는 것을 받아 준다. 부호가 어긋나면
// 무조건 #NUM! 을 내던 때에는 엑셀·구글 시트가 답을 내는 =CEILING(-4.5,2) 가
// 오류였다. 답이 없는 것은 양수를 음의 배수로 맞추는 쪽 하나뿐이다.
// 부호가 어긋나면 답이 없는 것은 MROUND 다.
func TestCeilingAndFloorFollowExcelSignRules(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		// 몫이 음수면 0 에서 멀어지는 쪽과 위쪽이 서로 반대다.
		{"=CEILING(-4.5,2)", -4},
		{"=FLOOR(-4.5,2)", -6},
		{"=CEILING(-2.5,2)", -2},
		{"=FLOOR(-2.5,2)", -4},
		{"=CEILING(-2.5,-2)", -4},
		{"=FLOOR(-2.5,-2)", -2},
		{"=CEILING(-2.5)", -2},
		{"=FLOOR(-2.5)", -3},
		// 십진 셈은 부호가 어긋나도 그대로 따라야 한다.
		{"=CEILING(-0.1-0.2,0.1)", -0.3},
		{"=FLOOR(-0.1-0.2,0.1)", -0.3},
		// 배수로 나누어떨어지면 어느 쪽으로도 움직이지 않는다.
		{"=CEILING(-4,2)", -4},
		{"=FLOOR(-4,2)", -4},
		{"=CEILING(0,-2)", 0},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, map[string]any{}), testCase.want, 1e-12)
	}

	for _, formula := range []string{
		"=CEILING(2.5,-2)",
		"=FLOOR(2.5,-2)",
		"=MROUND(-4.5,2)",
		"=MROUND(4.5,-2)",
	} {
		result := New().Evaluate(formula, map[string]any{})
		if result.Error == nil || result.Error.Code != "#NUM!" {
			t.Errorf("%s: %v 를 냈다. #NUM! 이어야 한다", formula, result.Error)
		}
	}
}

// 배수로 맞추는 함수도 십진수를 따라야 한다. 나눗셈을 이진 실수로 하면
// 0.1+0.2 를 0.1 단위로 올릴 때 0.4 가 나온다. 0.30000000000000004 를
// 0.1 로 나누면 3.0000000000000004 가 되기 때문이다.
//
// 화면에 보이는 자릿수도 마찬가지다. TEXT 는 Go 의 FormatFloat 에 그대로
// 맡기고 있어서 1.005 를 "1.00" 으로 냈다. 브라우저의 Intl.NumberFormat 은
// "1.01" 을 낸다. 격자에 보이는 값과 TEXT 의 답이 서로 달랐던 것이다.
// web/src/lib/cellFormat.test.ts 가 브라우저 쪽에서 같은 값을 고정한다.
func TestMultiplesAndDisplayFollowTheSameDecimal(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		{"=MROUND(1.005,0.01)", 1.01},
		{"=MROUND(2.675,0.01)", 2.68},
		{"=MROUND(0.615,0.01)", 0.62},
		{"=CEILING(0.1+0.2,0.1)", 0.3},
		{"=FLOOR(0.1+0.2,0.1)", 0.3},
		{"=CEILING(1.001,0.01)", 1.01},
		// 부호가 같은 음수 쪽은 0 에서 먼 쪽이 올림이다.
		{"=CEILING(-4.42,-0.05)", -4.45},
		{"=FLOOR(-4.42,-0.05)", -4.4},
		{"=MROUND(-7,-2)", -8},
		{"=MROUND(7,2)", 8},
		{"=CEILING(2.5)", 3},
		{"=FLOOR(2.5)", 2},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, map[string]any{}), testCase.want, 1e-12)
	}

	for _, testCase := range []struct {
		formula string
		want    string
	}{
		{`=TEXT(1.005,"0.00")`, "1.01"},
		{`=TEXT(2.675,"0.00")`, "2.68"},
		{`=TEXT(8.475,"0.00")`, "8.48"},
		{`=TEXT(-2.5,"0")`, "-3"},
		{`=TEXT(1234.5,"#,##0.00")`, "1,234.50"},
		{`=FIXED(1.005,2)`, "1.01"},
	} {
		result := New().Evaluate(testCase.formula, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %q", testCase.formula, result.Value, testCase.want)
		}
	}
}

// 엑셀 파일을 읽어 오면 날짜가 1899-12-30 부터 센 날 수로 담긴다. 사람이
// 손으로 적은 날짜는 "2024-01-15" 같은 글로 담긴다. 한 워크북 안에 두
// 모습이 함께 있을 수 있으므로 둘 다 날짜로 읽어야 한다.
//
// 브라우저의 격자는 날 수를 이미 날짜로 읽어 제대로 보여주고 있었지만
// 수식 쪽은 읽지 못했다. 가져온 파일의 날짜 칸이 보이기만 하고 셈에는
// 쓸 수 없었다 — YEAR, MONTH, WEEKDAY, DATEDIF, TEXT 이 모두 #VALUE! 였다.
//
// 아래 값은 web/src/lib/cellFormat.test.ts 와 **같은 날짜** 를 고정한다.
func TestDatesReadBothTheSerialAndTheText(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		// 2024-01-15 를 엑셀은 45306 으로 담는다.
		{"=YEAR(45306)", 2024.0},
		{"=MONTH(45306)", 1.0},
		{"=DAY(45306)", 15.0},
		{"=WEEKDAY(45306)", 2.0},
		{`=TEXT(45306,"yyyy-mm-dd")`, "2024-01-15"},
		{`=TEXT(45306,"yyyy년 m월 d일")`, "2024년 1월 15일"},
		{`=DATEDIF(45306,"2024-03-01","M")`, 1.0},

		// 글로 적은 날짜도 그대로 읽는다.
		{`=YEAR("2024-01-15")`, 2024.0},

		// 엑셀은 1900 년을 윤년으로 잘못 세므로 1 번이 1900-01-01 이 되려면
		// 60 보다 작은 번호는 하루 뒤에서 세야 한다.
		{"=YEAR(1)", 1900.0},
		{"=MONTH(1)", 1.0},
		{"=DAY(1)", 1.0},
		{"=MONTH(59)", 2.0},
		{"=DAY(59)", 28.0},
		{"=MONTH(61)", 3.0},
		{"=DAY(61)", 1.0},
	} {
		result := New().Evaluate(testCase.formula, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}

	// 날짜가 될 수 없는 것은 날짜로 읽지 않는다.
	for _, formula := range []string{`=YEAR(-1)`, `=YEAR("abc")`, `=YEAR(3000000)`} {
		if result := New().Evaluate(formula, map[string]any{}); result.Error == nil {
			t.Errorf("%s = %v, want an error", formula, result.Value)
		}
	}
}

// 표 서식에서 m 은 **앞뒤를 봐야** 뜻이 정해진다. 시 뒤에 오거나 초 앞에
// 오면 분이고, 그 밖에는 달이다.
//
//	"mm/dd/yyyy"  달
//	"hh:mm"       분
//	"h:mm:ss"     분
//
// 예전에는 서식을 Go 의 시각 layout 으로 바꿔치기해서 m 을 가려낼 수
// 없었다. mm 이 늘 달이 되어 =TEXT(0.5,"hh:mm") 이 12:00 이 아니라 12:12
// 였고, "h:mm:ss" 는 아예 "1" 이 나왔다.
//
// 아래 값은 web/src/lib/cellFormat.test.ts 와 **같은 글자** 를 고정한다.
// 격자에 보이는 값과 TEXT 의 답이 어긋나면 안 된다.
func TestDatePatternsTellMonthsFromMinutes(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		formula string
		want    string
	}{
		// 45306 은 2024-01-15, 0.5 는 낮 열두 시다.
		{`=TEXT(0.5,"hh:mm")`, "12:00"},
		{`=TEXT(0.5,"h:mm:ss")`, "12:00:00"},
		{`=TEXT(0.5104166666666666,"hh:mm:ss")`, "12:15:00"},
		{`=TEXT(0.5104166666666666,"h:m")`, "12:15"},
		// 초 앞에 오면 시가 없어도 분이다.
		{`=TEXT(0.5104166666666666,"m:ss")`, "15:00"},
		{`=TEXT(45306.25,"yyyy-mm-dd hh:mm")`, "2024-01-15 06:00"},

		// 날짜 쪽 m 은 달이다.
		{`=TEXT(45306,"mm/dd/yyyy")`, "01/15/2024"},
		{`=TEXT(45306,"yyyy-mm-dd")`, "2024-01-15"},
		// 토막이 셋인 서식도 모두 그린다.
		{`=TEXT(45306,"yyyy년 m월 d일")`, "2024년 1월 15일"},

		// 이름으로 적는 것들.
		{`=TEXT(45306,"mmmm")`, "January"},
		{`=TEXT(45306.5,"mmm d")`, "Jan 15"},
		{`=TEXT(45306,"dddd")`, "Monday"},
		{`=TEXT(45306,"ddd")`, "Mon"},
		{`=TEXT(45306,"yy")`, "24"},

		// 오전·오후를 적으면 열두 시간으로 센다.
		{`=TEXT(45306.75,"h:mm am/pm")`, "6:00 PM"},
		{`=TEXT(45306.25,"h:mm am/pm")`, "6:00 AM"},

		// 숫자 서식은 그대로다.
		{`=TEXT(1234.5,"#,##0.00")`, "1,234.50"},
		{`=TEXT(0.256,"0.0%")`, "25.6%"},
	} {
		result := New().Evaluate(testCase.formula, map[string]any{})
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %q", testCase.formula, result.Value, testCase.want)
		}
	}
}

// 범위 안에 오류가 하나라도 있으면 수식 전체가 그 오류가 된다. 합계는
// 그래야 한다 — 더할 수 없는 것이 섞여 있으니 합계도 알 수 없다.
//
// 그런데 **세는 함수** 는 다르다. 엑셀과 시트에서 COUNT 는 오류 칸을
// 건너뛰고 숫자만 세고, COUNTA 는 오류 칸도 "비어 있지 않다" 고 센다.
// 예전에는 이 둘도 함께 멈춰서, 열 어딘가에 #N/A 가 하나 있으면 그 열의
// 개수를 셀 방법이 아예 없었다.
func TestCountingFunctionsDoNotStopAtAnError(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1.0, "A2": formulaError("#N/A", "no value"), "A3": 3.0}
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{"=COUNT(A1:A3)", 2.0},
		{"=COUNTA(A1:A3)", 3.0},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}
	// 더하는 쪽은 그대로 멈춘다.
	if result := New().Evaluate("=SUM(A1:A3)", cells); result.Error == nil {
		t.Errorf("=SUM(A1:A3) = %v, 오류여야 한다", result.Value)
	}
}

// AGGREGATE 은 집계하면서 오류가 든 칸을 건너뛰라고 시킬 수 있다. 열
// 하나에 #N/A 가 섞여 합계가 통째로 막히는 것이 흔한 일이라 있는 함수다.
func TestAggregateSkipsErrorsWhenAsked(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1.0, "A2": formulaError("#N/A", "no value"), "A3": 3.0, "A4": 5.0, "A5": 7.0}
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		{"=AGGREGATE(9,6,A1:A5)", 16},     // 합
		{"=AGGREGATE(1,6,A1:A5)", 4},      // 평균
		{"=AGGREGATE(2,6,A1:A5)", 4},      // COUNT
		{"=AGGREGATE(4,6,A1:A5)", 7},      // 최대
		{"=AGGREGATE(5,6,A1:A5)", 1},      // 최소
		{"=AGGREGATE(12,6,A1:A5)", 4},     // 중앙값 1,3,5,7
		{"=AGGREGATE(14,6,A1:A5,2)", 5},   // 두 번째 큰 값
		{"=AGGREGATE(15,6,A1:A5,2)", 3},   // 두 번째 작은 값
		{"=AGGREGATE(16,6,A1:A5,0.5)", 4}, // 백분위수
		{"=AGGREGATE(17,6,A1:A5,1)", 2.5}, // 사분위수
		// 경계를 뺀 쪽은 자리를 k*(n+1) 로 잡는다. 0.25*5 = 1.25 이므로
		// 첫째와 둘째 사이 1/4 자리, 곧 1 + 0.25*(3-1) = 1.5 다.
		{"=AGGREGATE(18,6,A1:A5,0.25)", 1.5},
		{"=AGGREGATE(19,6,A1:A5,1)", 1.5},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, cells), testCase.want, 1e-9)
	}

	// 건너뛰지 말라고 하면 오류를 그대로 낸다.
	for _, formula := range []string{
		"=AGGREGATE(9,4,A1:A5)",      // 아무것도 건너뛰지 않는다
		"=AGGREGATE(9,0,A1:A5)",      // 기본값도 오류는 그대로다
		"=AGGREGATE(20,6,A1:A5)",     // 없는 집계 번호
		"=AGGREGATE(9,9,A1:A5)",      // 없는 옵션
		"=AGGREGATE(18,6,A1:A5,0.1)", // 자료 밖
	} {
		if result := New().Evaluate(formula, cells); result.Error == nil {
			t.Errorf("%s = %v, 오류여야 한다", formula, result.Value)
		}
	}
}

// 값이 무엇인지 묻는 함수는 오류를 만나도 답할 수 있어야 한다.
//
//	=IF(ISNUMBER(A2),A2,0)
//
// 이것은 오류를 피해 가려고 쓰는 가장 흔한 꼴이다. 그런데 그 ISNUMBER 가
// A2 의 오류에 걸려 함께 멈추면, 피해 갈 방법 자체가 없어진다. 오류를
// 다루려고 쓰는 도구가 오류 때문에 막혀 있었다.
//
// ERROR.TYPE 은 아예 쓸 수가 없었다. 오류를 들여다보라고 있는 함수인데
// 인수의 오류에 먼저 걸렸다.
func TestAskingWhatAValueIsWorksOnErrorsToo(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": 1.0,
		"A2": formulaError("#N/A", "no value"),
		"A3": formulaError("#DIV/0!", "divide by zero"),
		"A4": nil,
	}
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{"=ISBLANK(A2)", false},
		{"=ISBLANK(A4)", true},
		{"=ISNUMBER(A2)", false},
		{"=ISNUMBER(A1)", true},
		{"=ISTEXT(A2)", false},
		{"=ISNONTEXT(A2)", true},
		{"=ISLOGICAL(A2)", false},
		{"=TYPE(A2)", 16.0},
		{"=TYPE(A1)", 1.0},
		{`=TYPE("a")`, 2.0},
		{"=TYPE(TRUE)", 4.0},
		// 오류의 종류를 번호로 알려준다. #N/A 는 7, #DIV/0! 는 2 다.
		{"=ERROR.TYPE(A2)", 7.0},
		{"=ERROR.TYPE(A3)", 2.0},
		{"=COUNTBLANK(A1:A4)", 1.0},
		// 오류를 피해 가는 흔한 꼴이 이제 통한다.
		{"=IF(ISNUMBER(A2),A2,0)", 0.0},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}

	// 오류가 아닌 값에는 ERROR.TYPE 이 답할 것이 없다.
	if result := New().Evaluate("=ERROR.TYPE(A1)", cells); result.Error == nil {
		t.Errorf("=ERROR.TYPE(A1) = %v, #N/A 여야 한다", result.Value)
	}
	// 짝수인지 묻기 전에 수여야 하므로 이쪽은 그대로 멈춘다.
	if result := New().Evaluate("=ISEVEN(A2)", cells); result.Error == nil {
		t.Errorf("=ISEVEN(A2) = %v, 오류여야 한다", result.Value)
	}
}

// 마이크로소프트 문서가 오류를 다루라며 권하는 두 가지 꼴이 모두 되지
// 않았다.
//
//	=COUNTIF(범위,"<>#N/A")               특정 오류만 빼고 센다
//	=SUMPRODUCT(--NOT(ISERROR(범위)))     오류가 아닌 칸을 센다
//
// 앞엣것은 COUNTIF 가 오류에 걸려 멈춰서, 뒤엣것은 ISERROR 가 범위를
// 통째로 참 하나로 접고 NOT 이 배열을 받지 못해서 되지 않았다.
//
// SUMIF 는 그대로 멈춘다. 엑셀도 그렇게 한다 — 더할 수 없는 것이 섞여
// 있으면 합계도 알 수 없다. 세는 쪽과 더하는 쪽이 갈리는 자리다.
func TestCountingAroundErrorsFollowsTheDocumentedShapes(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": 1.0,
		"A2": formulaError("#N/A", "no value"),
		"A3": 3.0,
		"A4": 5.0,
	}
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		// 조건에 맞는 것만 센다. 오류는 어느 조건에도 맞지 않는다.
		{`=COUNTIF(A1:A4,">0")`, 3},
		{`=COUNTIFS(A1:A4,">0")`, 3},
		// 오류를 이름으로 골라낼 수도 있어야 한다.
		{`=COUNTIF(A1:A4,"<>#N/A")`, 3},
		{`=COUNTIF(A1:A4,"#N/A")`, 1},
		// 범위를 칸마다 물어 배열로 답한다.
		{`=SUMPRODUCT(--ISERROR(A1:A3))`, 1},
		{`=SUMPRODUCT(--NOT(ISERROR(A1:A3)))`, 2},
		{`=SUMPRODUCT(--ISNA(A1:A3))`, 1},
		{`=SUMPRODUCT(--ISERR(A1:A3))`, 0},
		{`=COUNT(A1:A4)`, 3},
		{`=COUNTA(A1:A4)`, 4},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, cells), testCase.want, 1e-9)
	}

	// NOT 은 홑값도 그대로 뒤집는다.
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{"=NOT(TRUE)", false},
		{"=NOT(FALSE)", true},
		{"=NOT(0)", true},
		{"=INDEX(NOT({TRUE;FALSE}),2)", true},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}

	// 더하는 쪽은 그대로 멈춘다.
	for _, formula := range []string{`=SUMIF(A1:A4,">0")`, "=SUM(A1:A4)"} {
		if result := New().Evaluate(formula, cells); result.Error == nil {
			t.Errorf("%s = %v, 오류여야 한다", formula, result.Value)
		}
	}
}

// 값 하나를 받아 값 하나를 내는 함수에 배열을 주면, 칸마다 따로 셈해
// 같은 모양의 배열이 나와야 한다. 표에서 조건에 맞는 칸을 세는 가장
// 흔한 꼴이 이것을 쓴다.
//
//	=SUMPRODUCT(--ISNUMBER(A1:A100))   숫자가 든 칸의 개수
//	=SUMPRODUCT(--(LEN(A1:A100)>0))    비어 있지 않은 칸의 개수
//
// 예전에는 인수를 낱낱이 펴는 자리보다 뒤에서 셈해서, 배열 하나가 값
// 여럿이 되어 "인수는 하나여야 한다" 에 걸렸다. ISNUMBER 도 LEN 도 ABS
// 도 배열을 받으면 모두 #VALUE! 였다.
func TestScalarFunctionsSpreadOverArrays(t *testing.T) {
	t.Parallel()
	cells := map[string]any{"A1": 1.0, "A2": "글", "A3": 3.0, "A4": nil, "A5": -2.0}
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		{`=SUMPRODUCT(--ISNUMBER(A1:A5))`, 3},
		{`=SUMPRODUCT(--ISTEXT(A1:A5))`, 1},
		{`=SUMPRODUCT(--ISBLANK(A1:A5))`, 1},
		{`=SUMPRODUCT(--(LEN(A1:A5)>0))`, 4},
		{`=SUMPRODUCT(ABS(A1:A5))`, 6},
		{`=SUMPRODUCT(LEN({"ab";"cde"}))`, 5},
		{`=SUMPRODUCT(--(UPPER({"a";"b"})="A"))`, 1},
		{`=SUMPRODUCT(--(ROUND({1.4;1.6},0)=2))`, 1},
		{`=INDEX(ABS({-1;2}),1)`, 1},
		// 글자는 어떤 수보다도 크다. 엑셀이 그렇게 견준다.
		{`=SUMPRODUCT(--(A1:A5>0))`, 3},

		// 홑값은 그대로다.
		{`=ABS(-3)`, 3},
		{`=LEN("abc")`, 3},
		{`=ROUND(1.005,2)`, 1.01},
		// 모으는 함수는 배열을 하나로 줄인다. 그것이 하는 일이다.
		{`=SUM(A1:A5)`, 2},
		{`=COUNT(A1:A5)`, 3},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, cells), testCase.want, 1e-9)
	}

	// 두 인수를 함께 펼 때는 칸끼리 짝지어 셈한다.
	if result := New().Evaluate(`=INDEX(TEXT({0.5;45306},"hh:mm"),1)`, cells); result.Error != nil || result.Value != "12:00" {
		t.Errorf(`TEXT 배열 = %v err=%v, want "12:00"`, result.Value, result.Error)
	}
	// 모양이 다른 배열끼리는 짝지을 수 없다.
	if result := New().Evaluate(`=ROUND({1;2;3},{1;2})`, cells); result.Error == nil {
		t.Errorf("모양이 다른 배열 = %v, 오류여야 한다", result.Value)
	}
}

// 인수가 둘 이상인 함수도 칸마다 짝지어 셈해야 한다. 글자를 다루는
// 함수가 특히 그렇다 — 열 하나에서 어떤 글이 든 칸을 세는 꼴이 이것을
// 쓴다.
//
//	=SUMPRODUCT(--ISNUMBER(FIND("서울",A1:A100)))
//
// 이어 붙이는 함수는 넣지 않았다. CONCAT 은 배열을 받으면 칸마다 나누는
// 것이 아니라 모두 이어 붙이는 것이 하는 일이다.
func TestFunctionsWithSeveralArgumentsSpreadTogether(t *testing.T) {
	t.Parallel()
	cells := map[string]any{}
	for _, testCase := range []struct {
		formula string
		want    float64
	}{
		{`=SUMPRODUCT(LEN(LEFT({"abc";"de"},2)))`, 4},
		{`=SUMPRODUCT(MOD({5;7},{3;3}))`, 3},
		{`=SUMPRODUCT(POWER({2;3},2))`, 13},
		{`=SUMPRODUCT(--EXACT({"a";"b"},"a"))`, 1},
		// 글자가 든 칸을 세는 흔한 꼴.
		{`=SUMPRODUCT(--ISNUMBER(FIND("a",{"cat";"dog"})))`, 1},
		// 홑값은 그대로다.
		{`=MOD(5,3)`, 2},
		{`=POWER(2,3)`, 8},
	} {
		assertClose(t, testCase.formula, evaluateNumber(t, testCase.formula, cells), testCase.want, 1e-9)
	}
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{`=INDEX(RIGHT({"abc";"de"},1),1)`, "c"},
		{`=INDEX(MID({"abc";"de"},2,1),1)`, "b"},
		{`=INDEX(SUBSTITUTE({"aa";"bb"},"a","c"),1)`, "cc"},
		{`=INDEX(REPT({"a";"b"},2),2)`, "bb"},
		{`=LEFT("abc",2)`, "ab"},
		{`=SUBSTITUTE("aa","a","c")`, "cc"},
		// 이어 붙이는 함수는 모두 잇는다. 칸마다 나누지 않는다.
		{`=CONCAT({"a";"b"},"!")`, "ab!"},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}
}

// 범위에 오류가 든 칸이 하나 섞여 있어도, 칸마다 셈하는 함수는 그 칸만
// 오류로 남기고 나머지는 제 값을 내야 한다.
//
// 그러지 않으면 **조용히 틀린 답** 이 나온다. FIND 가 범위를 통째로
// 멈추면 바깥의 ISNUMBER 가 "숫자가 아니다" 라고 답해 개수가 0 이 된다.
// 오류도 아니고 맞지도 않은 답이라 눈으로 가려낼 수 없다.
func TestOneBadCellDoesNotSilenceTheWholeRange(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": 1.0,
		"A2": "서울 지점",
		"A3": 3.0,
		"A4": formulaError("#N/A", "no value"),
		"A5": "서울 본사",
	}
	// 오류 칸이 있어도 "서울" 이 든 칸을 센다. 고치기 전에는 0 이었다.
	assertClose(t, "FIND idiom", evaluateNumber(t, `=SUMPRODUCT(--ISNUMBER(FIND("서울",A1:A5)))`, cells), 2, 1e-9)
	assertClose(t, "ISNUMBER", evaluateNumber(t, `=SUMPRODUCT(--ISNUMBER(A1:A5))`, cells), 2, 1e-9)

	// 오류가 든 칸은 그 칸만 오류다.
	if result := New().Evaluate("=INDEX(ABS(A1:A5),4)", cells); result.Error == nil {
		t.Errorf("=INDEX(ABS(A1:A5),4) = %v, 그 칸은 오류여야 한다", result.Value)
	}
	if result := New().Evaluate("=INDEX(ABS({-1;-2}),2)", cells); result.Error != nil || result.Value != 2.0 {
		t.Errorf("=INDEX(ABS({-1;-2}),2) = %v err=%v, want 2", result.Value, result.Error)
	}

	// 오류에도 답하는 함수는 오류를 받아 답한다. 흘려보내지 않는다.
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{"=ISNUMBER(A4)", false},
		{"=ISBLANK(A4)", false},
		{"=TYPE(A4)", 16.0},
		{"=ERROR.TYPE(A4)", 7.0},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}
	// 흘려보내는 함수는 홑값 오류를 그대로 낸다.
	if result := New().Evaluate("=ABS(A4)", cells); result.Error == nil {
		t.Errorf("=ABS(A4) = %v, 오류여야 한다", result.Value)
	}
}

// 시각은 하루를 1 로 본 분수다. 그래야 날짜에 더할 수 있다.
//
// TIME 만 글자를 돌려주고 있었다. 칸에 그대로 보기에는 좋았지만 **더할
// 수가 없었다** — 시각을 만드는 함수를 시각에 더할 수 없으면 만들 까닭이
// 없다. 같은 라이브러리 안에서 TIMEVALUE 와 답이 갈리기도 했다.
func TestTimeIsAFractionOfADaySoItCanBeAdded(t *testing.T) {
	t.Parallel()
	cells := map[string]any{
		"A1": "2024-01-15 10:00:00",
		"A2": 45306.5, // 엑셀에서 가져온 2024-01-15 12:00
	}
	// TIME 과 TIMEVALUE 가 같은 답을 낸다.
	assertClose(t, "TIME", evaluateNumber(t, "=TIME(1,30,0)", cells), 0.0625, 1e-12)
	assertClose(t, "TIMEVALUE", evaluateNumber(t, `=TIMEVALUE("01:30:00")`, cells), 0.0625, 1e-12)
	assertClose(t, "TIME*24", evaluateNumber(t, "=TIME(12,30,0)*24", cells), 12.5, 1e-12)
	// 하루를 넘기면 돌아온다. 25시는 1시다.
	assertClose(t, "TIME wraps", evaluateNumber(t, "=TIME(25,0,0)", cells), 1.0/24.0, 1e-12)
	// 분과 초도 넘어간다. 90분은 한 시간 반이다.
	assertClose(t, "TIME rolls", evaluateNumber(t, "=TIME(0,90,0)", cells), 0.0625, 1e-12)

	// 이제 시각에 더할 수 있다. 이것이 TIME 을 두는 까닭이다.
	for _, testCase := range []struct {
		formula string
		want    any
	}{
		{`=A1+TIME(1,0,0)`, "2024-01-15 11:00:00"},
		{`=TEXT(A1+TIME(1,30,0),"yyyy-mm-dd hh:mm")`, "2024-01-15 11:30"},
		{`=TEXT(TIME(13,5,0),"hh:mm")`, "13:05"},
		{`=HOUR(A1+TIME(2,0,0))`, 12.0},
	} {
		result := New().Evaluate(testCase.formula, cells)
		if result.Error != nil {
			t.Errorf("%s: %v", testCase.formula, result.Error)
			continue
		}
		if result.Value != testCase.want {
			t.Errorf("%s = %v, want %v", testCase.formula, result.Value, testCase.want)
		}
	}
	// 엑셀에서 가져온 일련번호에도 더할 수 있다.
	assertClose(t, "serial + TIME", evaluateNumber(t, "=A2+TIME(6,0,0)", cells), 45306.75, 1e-9)
}

// 엑셀 2010 부터 STDEV.P 처럼 점 붙은 이름을 쓰고 시트도 둘 다 받는다.
// 오늘 만든 파일을 가져오면 예전 이름만 아는 엔진에서 #NAME? 이 났다 —
// 셈은 이미 있는데 이름을 몰라서였다.
func TestModernFunctionNamesComputeTheSameAsTheOldOnes(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, pair := range [][2]string{
		{"=STDEV.S(2,4,4,4,5,5,7,9)", "=STDEV(2,4,4,4,5,5,7,9)"},
		{"=STDEV.P(2,4,4,4,5,5,7,9)", "=STDEVP(2,4,4,4,5,5,7,9)"},
		{"=VAR.S(1,2,3,4)", "=VAR(1,2,3,4)"},
		{"=VAR.P(1,2,3,4)", "=VARP(1,2,3,4)"},
		{"=MODE.SNGL(1,2,2,3)", "=MODE(1,2,2,3)"},
		{"=RANK.EQ(2,{1,2,2,3})", "=RANK(2,{1,2,2,3})"},
		{"=PERCENTILE.INC({1,2,3,4},0.25)", "=PERCENTILE({1,2,3,4},0.25)"},
		{"=QUARTILE.INC({1,3,5,7},1)", "=QUARTILE({1,3,5,7},1)"},
		{"=FORECAST.LINEAR(4,{2,3,5},{1,2,3})", "=FORECAST(4,{2,3,5},{1,2,3})"},
		{"=COVARIANCE.P({1,2,3},{4,6,9})", "=COVAR({1,2,3},{4,6,9})"},
	} {
		modern, old := engine.Evaluate(pair[0], map[string]any{}), engine.Evaluate(pair[1], map[string]any{})
		if modern.Error != nil {
			t.Errorf("%s -> %s %s", pair[0], modern.Error.Code, modern.Error.Message)
			continue
		}
		if display(modern.Value) != display(old.Value) {
			t.Errorf("%s=%v 인데 %s=%v", pair[0], modern.Value, pair[1], old.Value)
		}
	}
	// 이름은 대소문자를 가리지 않는다. 붙여넣은 수식이 소문자일 수 있다.
	if result := engine.Evaluate("=stdev.p(2,4,4,4,5,5,7,9)", map[string]any{}); result.Error != nil || display(result.Value) != "2" {
		t.Errorf("소문자 별명=%#v", result)
	}
}

// 점 붙은 이름이 모두 별명인 것은 아니다. EXC 는 양 끝을 자료 밖에 두므로
// 값이 다르다. 별명으로 뭉뚱그리면 조용히 틀린 답을 준다.
func TestExclusivePercentilesDifferFromInclusiveOnes(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct {
		expression string
		expected   string
	}{
		// 네 개짜리 자료에서 포함형 1사분위는 1.75, 배타형은 1.25다.
		{"=PERCENTILE.INC({1,2,3,4},0.25)", "1.75"},
		{"=PERCENTILE.EXC({1,2,3,4},0.25)", "1.25"},
		// 가운데는 둘 다 중앙값이다.
		{"=PERCENTILE.EXC({1,2,3,4},0.5)", "2.5"},
		{"=QUARTILE.EXC({1,3,5,7},1)", "1.5"},
		{"=QUARTILE.EXC({1,3,5,7},2)", "4"},
		{"=QUARTILE.EXC({1,3,5,7},3)", "6.5"},
		// 동점은 차지한 등수의 평균을 받는다. RANK 는 둘 다 2등을 준다.
		{"=RANK.AVG(2,{1,2,2,3})", "2.5"},
		{"=RANK(2,{1,2,2,3})", "2"},
		{"=RANK.AVG(2,{1,2,2,3},1)", "2.5"},
		{"=RANK.AVG(3,{1,2,2,3})", "1"},
		// 이름 끝의 A 는 숫자가 아닌 값을 0 으로 센다.
		{"=MINA(2,3,\"x\")", "0"},
		{"=MIN(2,3,\"x\")", "2"},
		{"=MAXA(-1,-2,\"x\")", "0"},
		{"=MAX(-1,-2,\"x\")", "-1"},
		{"=MINA(TRUE,5)", "1"},
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
	// 배타형이 답할 수 없는 자리는 그렇다고 말해야 한다. 네 개짜리 자료는
	// 0.2 에서 0.8 사이만 답할 수 있다.
	for _, expression := range []string{
		"=PERCENTILE.EXC({1,2,3,4},0)", "=PERCENTILE.EXC({1,2,3,4},1)", "=PERCENTILE.EXC({1,2,3,4},0.1)",
		"=QUARTILE.EXC({1,3,5,7},0)", "=QUARTILE.EXC({1,3,5,7},4)", "=QUARTILE.EXC({1,3,5,7},1.5)",
	} {
		result := engine.Evaluate(expression, map[string]any{})
		if result.Error == nil || result.Error.Code != "#NUM!" {
			t.Errorf("%s 가 답을 냈다: %#v", expression, result)
		}
	}
	// 없는 값의 등수는 답이 없다.
	if result := engine.Evaluate("=RANK.AVG(9,{1,2,3})", map[string]any{}); result.Error == nil || result.Error.Code != "#N/A" {
		t.Errorf("없는 값의 등수=%#v", result)
	}
}

// 엑셀은 글자가 아닌 것 뒤를 낱말의 처음으로 본다. 따옴표와 숫자도
// 글자가 아니므로 그 뒤가 큰 글자가 된다. 이름·주소를 다듬으려고 쓰는
// 함수라 답이 다르면 옮겨 온 표가 조용히 어긋난다.
func TestProperCapitalisesAfterEveryNonLetter(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct {
		expression string
		expected   string
	}{
		{`=PROPER("hello kanpic")`, "Hello Kanpic"},
		{`=PROPER("o'neil")`, "O'Neil"},
		{`=PROPER("76budGet")`, "76Budget"},
		{`=PROPER("mcdonald-smith")`, "Mcdonald-Smith"},
		{`=PROPER("a.b c_d")`, "A.B C_D"},
		{`=PROPER("서울시 gangnam-gu")`, "서울시 Gangnam-Gu"},
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

// 자릿수가 음수면 소수점 왼쪽에서 반올림한다. ROUND(1234.567,-2) 가
// 1200 인 것과 같은 규칙이며, FIXED·DOLLAR 도 이를 따라야 한다.
func TestFixedAndDollarRoundLeftOfThePoint(t *testing.T) {
	t.Parallel()
	engine := New()
	for _, item := range []struct {
		expression string
		expected   string
	}{
		{`=FIXED(1234.567,-2)`, "1,200"},
		{`=FIXED(1234.567,-2,TRUE)`, "1200"},
		{`=FIXED(1250,-2)`, "1,300"},
		{`=FIXED(-1234.567,-2)`, "-1,200"},
		{`=FIXED(1234.567,-10)`, "0"},
		{`=DOLLAR(1234.567,-2)`, "₩1,200"},
		{`=FIXED(1234.567,2)`, "1,234.57"},
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
