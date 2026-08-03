package formula

import (
	"testing"
	"time"
)

func TestLibraryFunctions(t *testing.T) {
	t.Parallel()
	evaluator := New()
	cells := map[string]any{"A1": 4.0, "A2": nil, "A3": "메모", "A4": 9.0}
	for expression, want := range map[string]any{
		`=COUNTA(A1:A4)`:                            3.0,
		`=COUNTBLANK(A1:A4)`:                        1.0,
		`=MEDIAN(1,5,9,13)`:                         7.0,
		`=PRODUCT(A1,A4)`:                           36.0,
		`=ROUNDUP(2.011,2)`:                         2.02,
		`=ROUNDDOWN(-2.019,2)`:                      -2.01,
		`=ABS(-7)+INT(3.9)+SQRT(A4)`:                13.0,
		`=MOD(-1,3)`:                                2.0,
		`=POWER(2,10)`:                              1024.0,
		`=NOT(FALSE)`:                               true,
		`=LEN("스프레드시트")`:                            6.0,
		`=TRIM("  두   칸  ")`:                        "두 칸",
		`=UPPER("kanpic")&LOWER("SHEET")`:           "KANPICsheet",
		`=PROPER("hello kanpic")`:                   "Hello Kanpic",
		`=SUBSTITUTE("2026-08-04","-","/")`:         "2026/08/04",
		`=FIND("pic","kanpic")`:                     4.0,
		`=SEARCH("PIC","kanpic")`:                   4.0,
		`=REPT("가",3)`:                              "가가가",
		`=TEXTJOIN(", ",TRUE,"가","","나")`:           "가, 나",
		`=VALUE("1234")`:                            1234.0,
		`=HYPERLINK("https://kanpic.example","문서")`: "문서",
		`=YEAR("2026-08-04")`:                       2026.0,
		`=MONTH(DATE(2026,8,4))`:                    8.0,
		`=DAY("2026-08-04")`:                        4.0,
		`=WEEKDAY("2026-08-04")`:                    3.0,
		`=IFERROR(1/0,"오류")`:                        "오류",
		`=IFERROR(SUM(A1,A4),"오류")`:                 13.0,
	} {
		result := evaluator.Evaluate(expression, cells)
		if result.Error != nil {
			t.Errorf("%s returned %v", expression, result.Error)
			continue
		}
		if number, ok := want.(float64); ok {
			got, isNumber := result.Value.(float64)
			if !isNumber || got-number > 1e-9 || number-got > 1e-9 {
				t.Errorf("%s = %#v; want %v", expression, result.Value, number)
			}
			continue
		}
		if result.Value != want {
			t.Errorf("%s = %#v; want %#v", expression, result.Value, want)
		}
	}
}

func TestLibraryClockFunctions(t *testing.T) {
	original := now
	now = func() time.Time { return time.Date(2026, 8, 4, 13, 45, 30, 0, time.UTC) }
	defer func() { now = original }()
	evaluator := New()
	if result := evaluator.Evaluate(`=TODAY()`, nil); result.Error != nil || result.Value != "2026-08-04" {
		t.Fatalf("TODAY = %#v, %v", result.Value, result.Error)
	}
	if result := evaluator.Evaluate(`=NOW()`, nil); result.Error != nil || result.Value != "2026-08-04 13:45:30" {
		t.Fatalf("NOW = %#v, %v", result.Value, result.Error)
	}
}

func TestUnknownFunctionStillFails(t *testing.T) {
	t.Parallel()
	result := New().Evaluate(`=NOSUCHFUNCTION(1)`, nil)
	if result.Error == nil || result.Error.Code != "#NAME?" {
		t.Fatalf("error = %#v", result.Error)
	}
}

func TestCatalogCoversDocumentedFunctions(t *testing.T) {
	t.Parallel()
	evaluator := New()
	for _, doc := range Catalog() {
		if doc.Name == "" || doc.Syntax == "" || doc.Summary == "" || doc.Category == "" {
			t.Errorf("incomplete catalog entry %#v", doc)
		}
		// A catalogued name must never be reported as unknown; argument errors
		// are fine because the probe deliberately passes no arguments.
		result := evaluator.Evaluate("="+doc.Name+"()", nil)
		if result.Error != nil && result.Error.Code == "#NAME?" {
			t.Errorf("%s is catalogued but unknown to the evaluator", doc.Name)
		}
	}
}
