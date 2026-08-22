package formula

import (
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
		`=TIME(13,5,0)`:                           "13:05:00",
		`=HOUR("13:05:00")`:                       13.0,
		`=SPLIT("a,b,c",",")`:                     nil,
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
		`=FIXED(1234.5,,TRUE)`:        "1234.50",
		`=SUM(1,,2)`:                  3.0,
		`=XLOOKUP(2,A1:A3,B1:B3,,0)`:  "y",
		`=XMATCH(2,A1:A3,,1)`:         2.0,
		`=ADDRESS(1,2,,"Sheet1")`:     "Sheet1!$B$1",
		`=OFFSET(A1,1,0,,1)`:          2.0,
		// Payments at the start of the period carry no interest in the first
		// one; reading the trailing 1 as the future value gave -100 instead.
		`=IPMT(0.1,1,3,1000,,1)`:      0.0,
		`=SPLIT("a,b",",",,FALSE)`:    nil,
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
		`=TEXTBEFORE(A1,": ")`:                "이름",
		`=TEXTAFTER(A1,": ")`:                 "홍길동",
		`=TEXTBEFORE("a-b-c","-",2)`:          "a-b",
		`=TEXTAFTER("a-b-c","-",-1)`:          "c",
		`=TEXTBEFORE("abc","-",1,0,0,"없음")`: "없음",
		`=TEXTAFTER("A-b","-",1,1)`:           "b",
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
		`=VSTACK(A2:B2,A3:B3)`:     {{"부산", 80.0}, {"서울", 120.0}},
		`=HSTACK(A2:A3,B2:B3)`:     {{"부산", 80.0}, {"서울", 120.0}},
		`=TAKE(A1:B4,2)`:           {{"지역", "매출"}, {"부산", 80.0}},
		`=TAKE(A1:B4,-2)`:          {{"서울", 120.0}, {"대구", 95.0}},
		`=DROP(A1:B4,1)`:           {{"부산", 80.0}, {"서울", 120.0}, {"대구", 95.0}},
		`=DROP(A1:B4,,-1)`:         {{"지역"}, {"부산"}, {"서울"}, {"대구"}},
		`=CHOOSEROWS(A1:B4,1,-1)`:  {{"지역", "매출"}, {"대구", 95.0}},
		`=CHOOSECOLS(A1:B4,2)`:     {{"매출"}, {80.0}, {120.0}, {95.0}},
		`=SORTBY(A2:B4,B2:B4,-1)`:  {{"서울", 120.0}, {"대구", 95.0}, {"부산", 80.0}},
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
		`=LET(x,5,x*2)`:                                 10.0,
		`=LET(x,5,y,x+1,x*y)`:                           30.0,
		`=LET(total,SUM(A1:A3),total/COUNT(A1:A3))`:     20.0,
		`=LET(x,A1,IF(x>5,"큼","작음"))`:                 "큼",
		`=LAMBDA(x,x+1)(4)`:                             5.0,
		`=LET(double,LAMBDA(x,x*2),double(21))`:         42.0,
		`=LET(area,LAMBDA(w,h,w*h),area(3,4)+area(2,2))`: 16.0,
		`=SUM(LET(x,2,x),3)`:                            5.0,
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
		`=MAP(A1:A3,LAMBDA(x,x*2))`:            {{2.0}, {4.0}, {6.0}},
		`=MAP(A1:A3,B1:B3,LAMBDA(x,y,x+y))`:    {{11.0}, {22.0}, {33.0}},
		`=BYROW(A1:B3,LAMBDA(row,SUM(row)))`:   {{11.0}, {22.0}, {33.0}},
		`=BYCOL(A1:B3,LAMBDA(col,SUM(col)))`:   {{6.0, 60.0}},
		`=SCAN(0,A1:A3,LAMBDA(acc,x,acc+x))`:   {{1.0}, {3.0}, {6.0}},
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
