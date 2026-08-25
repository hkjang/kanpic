package formula

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// 반대 방향도 본다. 만들어 놓고 목록에 적지 않으면 자동완성에도 함수 목록에도
// 나오지 않아, 이름을 이미 아는 사람만 쓸 수 있는 함수가 된다. 만든 사람은
// 됐다고 여기고 쓰는 사람은 없는 줄 아는 것이 가장 조용한 실패다.
func TestEveryImplementedFunctionIsCatalogued(t *testing.T) {
	t.Parallel()
	// ISFORMULA 는 일부러 뺀 것이다. 칸의 값이 아니라 그 뒤의 수식을 물으므로
	// 값만 받는 미리보기에서는 답할 수 없다. 미리보기와 저장이 다른 답을 내면
	// 그것이 더 나쁘므로 #N/A 를 내고 목록에도 올리지 않는다. 파일에서 오간
	// 이름은 지켜야 하므로 excel_names.go 에는 남아 있다.
	excluded := map[string]bool{"ISFORMULA": true}
	catalogued := make(map[string]bool, len(Catalog()))
	for _, doc := range Catalog() {
		catalogued[strings.ToUpper(doc.Name)] = true
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`case ("[A-Z0-9_.]+"(?:, ?"[A-Z0-9_.]+")*):`)
	names := make(map[string]bool)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(body), -1) {
			for _, piece := range strings.Split(match[1], ",") {
				names[strings.Trim(strings.TrimSpace(piece), `"`)] = true
			}
		}
	}
	// 훑기가 깨지면 아무것도 못 찾고 조용히 통과한다. 지키는 것이 없어진 채로
	// 초록인 시험이 없는 시험보다 나쁘므로, 찾은 수가 터무니없으면 실패한다.
	if len(names) < 300 {
		t.Fatalf("훑어서 찾은 이름이 %d개뿐이다. case 문의 모양이 바뀌었는지 본다", len(names))
	}
	for name := range names {
		if catalogued[name] || excluded[name] || !IsBuiltInFunction(name) {
			continue
		}
		t.Errorf("%s 는 만들어져 있는데 함수 목록에 없다. library.go 에 적거나, 일부러 뺀 것이면 까닭과 함께 excluded 에 적는다", name)
	}
}

// 날짜를 날 수로 바꾸는 셈은 되돌릴 수 있어야 한다. 짝이 어긋나면 일정표의
// 막대가 격자에 보이는 날짜와 다른 자리에 그려진다.
func TestDateSerialRoundTripsWithSerialDate(t *testing.T) {
	t.Parallel()
	for _, sample := range []struct {
		value  any
		serial float64
	}{
		{"2026-01-05", 46027},
		// 1900 년을 윤년으로 잘못 센 자리. 60 보다 작은 번호는 하루 뒤에서
		// 세기 시작해야 1900-01-01 이 1 번이 된다.
		{"1900-01-01", 1},
		{"1900-03-01", 61},
		{"2026/02/10", 46063},
		{45000.0, 45000},
	} {
		serial, ok := DateSerial(sample.value)
		if !ok || serial != sample.serial {
			t.Errorf("DateSerial(%v) = %v (%v), want %v", sample.value, serial, ok, sample.serial)
			continue
		}
		back, valid := SerialDate(serial)
		if !valid {
			t.Errorf("SerialDate(%v) 를 되돌리지 못했다", serial)
			continue
		}
		if again, _ := DateSerial(back.Format("2006-01-02")); again != sample.serial {
			t.Errorf("%v 를 오갔더니 %v 가 되었다", sample.value, again)
		}
	}
	// 날짜가 아닌 것은 날 수가 아니다. 글로 적힌 2024 를 날 수로 보면 안 된다.
	for _, sample := range []any{"아직", "", nil, "2024년"} {
		if serial, ok := DateSerial(sample); ok {
			t.Errorf("DateSerial(%v) = %v, 날짜가 아니어야 한다", sample, serial)
		}
	}
}
