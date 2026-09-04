package importexport

import (
	"encoding/json"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Go 의 ParseFloat 는 스프레드시트보다 넓다 — "NaN", "Inf", "1_000", "0x1p4"
// 를 모두 받는다. 가져오기가 그것을 그대로 쓰는 바람에 두 가지가 났다.
//
// 첫째, pandas 나 R 이 내보낸 CSV 의 빠진 값("NaN")이 부동소수점 NaN 이
// 되었는데 그 값은 JSON 으로 적을 수 없어, 그 한 칸 때문에 파일이 통째로
// 들어오지 못했다 — 사람이 본 것은 "json: unsupported value: NaN" 뿐이다.
// 둘째, "1_000" 이 조용히 1000 이 되어 파일에 적힌 것과 다른 값이 칸에 남았다.
//
// 수식 엔진과 격자는 이미 이 자를 쓴다(internal/formula 의 decimalText,
// web/src/lib/spreadsheetNumber.ts). 가져오기만 넓었다.
func TestParseScalarKeepsGoOnlyNumbersAsText(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"NaN", "nan", "Inf", "-Inf", "Infinity", "+Infinity", "1_000", "1_0.5", "0x1p4", "0x1P-2"} {
		got := parseScalar(text)
		if got != any(text) {
			t.Errorf("parseScalar(%q) = %#v, 글자 그대로여야 한다", text, got)
		}
		if _, err := json.Marshal(got); err != nil {
			t.Errorf("parseScalar(%q) 를 JSON 으로 적지 못한다: %v", text, err)
		}
	}
	// 평범한 수는 그대로 수다.
	for _, testCase := range []struct {
		text string
		want float64
	}{{"1000", 1000}, {"-2.5", -2.5}, {"1e3", 1000}, {".5", 0.5}, {"5.", 5}} {
		if got := parseScalar(testCase.text); got != any(testCase.want) {
			t.Errorf("parseScalar(%q) = %#v, want %v", testCase.text, got, testCase.want)
		}
	}
}

// 증상 그대로. "NaN" 한 칸이 들어 있는 CSV 는 통째로 들어오지 못했다.
func TestOneExportedNaNDoesNotStopTheWholeImport(t *testing.T) {
	t.Parallel()
	csv := "지역,매출\n서울,1200\n부산,NaN\n대구,inf\n광주,800\n"
	parsed, err := Parse("매출.csv", []byte(csv), 0)
	if err != nil {
		t.Fatalf("가져오지 못했다: %v", err)
	}
	values := map[string]string{}
	for _, cell := range parsed.Sheets[0].Cells {
		values[coordinateKey(cell.Row, cell.Column)] = string(cell.Value)
	}
	for key, want := range map[string]string{
		"2:2": "1200",
		"3:2": `"NaN"`,
		"4:2": `"inf"`,
		"5:2": "800",
	} {
		if values[key] != want {
			t.Errorf("%s 칸 = %s, want %s", key, values[key], want)
		}
	}
}

// XLSX 도 같은 자를 쓴다. 형식이 적히지 않은 칸은 수로 읽는 것이 맞지만,
// 거기 적힌 것이 "NaN" 이면 그것은 수가 아니다.
func TestParseXLSXValueKeepsGoOnlyNumbersAsText(t *testing.T) {
	t.Parallel()
	for _, cellType := range []excelize.CellType{excelize.CellTypeUnset, excelize.CellTypeNumber} {
		for _, text := range []string{"NaN", "Inf", "1_000", "0x1p4"} {
			if got := parseXLSXValue(text, cellType); got != any(text) {
				t.Errorf("parseXLSXValue(%q, %v) = %#v, 글자 그대로여야 한다", text, cellType, got)
			}
		}
		if got := parseXLSXValue("1200.5", cellType); got != any(1200.5) {
			t.Errorf("parseXLSXValue(\"1200.5\", %v) = %#v", cellType, got)
		}
	}
}

// CSV 로 들어오는 값도 붙여넣기와 같은 한도를 지켜야 한다. 스무 자리
// 계좌번호를 실수로 읽으면 파일에 적힌 것과 다른 값이 칸에 들어가고,
// 되돌릴 방법이 없다.
//
// 짝: web/src/lib/spreadsheetNumber.ts 의 significantDigits,
// web/src/lib/clipboardNumber.ts 의 parsePastedNumber.
func TestParseScalarKeepsNumbersItCannotHoldExactly(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		text string
		want any
	}{
		{"1000", 1000.0},
		{"1.5", 1.5},
		{"999999999999999", 999999999999999.0},
		{"12345678901234567890", "12345678901234567890"},
		{"-12345678901234567890", "-12345678901234567890"},
		{"1234567890.12345678", "1234567890.12345678"},
		// 앞의 0 은 번호를 뜻하므로 예전부터 글자로 두었다.
		{"007", "007"},
		// 지수로 적은 것은 사람이 수로 적은 것이다.
		{"1e30", 1e30},
	} {
		if got := parseScalar(testCase.text); got != testCase.want {
			t.Errorf("parseScalar(%q) = %#v, want %#v", testCase.text, got, testCase.want)
		}
	}
}

// 가져오기 미리보기가 글자로 담긴 숫자를 세어 알려 준다. 조용히 두면
// =SUM 이 그 칸들을 빼고 셈하는데 이유가 아무 데도 적히지 않는다.
func TestLooksLikeNumberStoredAsText(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"1,234", "₩5,000", "(500)", "50%", "1,234원", "$1,234.50"} {
		if !looksLikeNumberStoredAsText(text) {
			t.Errorf("%q 는 글자로 담긴 숫자로 세어야 한다", text)
		}
	}
	for _, text := range []string{"", "사과", "1000", "1.5", "-", "007", "abc,def"} {
		if looksLikeNumberStoredAsText(text) {
			t.Errorf("%q 는 글자로 담긴 숫자가 아니다", text)
		}
	}
}
