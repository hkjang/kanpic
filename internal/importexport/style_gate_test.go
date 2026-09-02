package importexport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
)

// 가져온 서식은 이 서비스가 스스로 받아 주는 것이어야 한다. 검사보다 넉넉하게
// 저장하면, 나중에 그 셀에 글자 하나만 넣어도 서식이 그대로 되돌아가 400 이 된다.
func TestImportedStyleIsOneThisServiceAccepts(t *testing.T) {
	long := strings.Repeat("0", 200)
	cases := []struct {
		name  string
		style *excelize.Style
	}{
		{"long custom number format", &excelize.Style{CustomNumFmt: &long}},
		{"long font family", &excelize.Style{Font: &excelize.Font{Family: long}}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			raw := canonicalStyleFromXLSX(item.style)
			if len(raw) == 0 {
				return
			}
			if err := workbook.ValidateCellStyle(workbook.CellInput{Row: 1, Column: 1, Style: raw}); err != nil {
				t.Fatalf("가져온 서식을 서버가 거절한다: %v (%s)", err, string(raw))
			}
		})
	}
}

// 걸리는 칸만 버리고 나머지는 남아야 한다. 파일 하나 때문에 굵게와 색까지 잃지 않는다.
func TestImportedStyleKeepsWhatItCan(t *testing.T) {
	long := strings.Repeat("0", 200)
	raw := canonicalStyleFromXLSX(&excelize.Style{Font: &excelize.Font{Bold: true, Family: long}, CustomNumFmt: &long})
	var style map[string]any
	if err := json.Unmarshal(raw, &style); err != nil {
		t.Fatalf("서식을 읽지 못한다: %v", err)
	}
	if style["bold"] != true {
		t.Fatalf("굵게가 사라졌다: %v", style)
	}
	if _, exists := style["font_family"]; exists {
		t.Fatalf("서버가 거절할 글꼴 이름이 남았다: %v", style)
	}
	if _, exists := style["number_format"]; exists {
		t.Fatalf("서버가 거절할 표시 형식이 남았다: %v", style)
	}
}

// 실제 파일을 통과시켜, 저장까지 간 서식이 서버가 받아 주는 것인지 본다. 여기가
// 어긋나면 가져온 셀에 글자 하나만 넣어도 붙여넣기 전체가 400 으로 떨어진다.
func TestImportedFileLeavesOnlyEditableCells(t *testing.T) {
	file := excelize.NewFile()
	defer file.Close()
	longFormat := `"` + strings.Repeat("가", 60) + `"0`
	styleID, err := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Strike: true}, CustomNumFmt: &longFormat})
	if err != nil {
		t.Fatal(err)
	}
	_ = file.SetCellValue("Sheet1", "A1", 1200)
	_ = file.SetCellStyle("Sheet1", "A1", "A1", styleID)
	buffer, err := file.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := parseXLSX("긴형식.xlsx", "긴 형식", buffer.Bytes(), 1_000_000)
	if err != nil {
		t.Fatalf("파일을 읽지 못한다: %v", err)
	}
	cells := parsed.Sheets[0].Cells
	if len(cells) == 0 {
		t.Fatal("셀이 하나도 들어오지 않았다")
	}
	for _, cell := range cells {
		if err := workbook.ValidateCellStyle(cell); err != nil {
			t.Fatalf("가져온 셀을 서버가 거절한다: %v (%s)", err, string(cell.Style))
		}
	}
	// 걸린 칸만 버리고 나머지 서식은 남아야 한다.
	var style map[string]any
	if err := json.Unmarshal(cells[0].Style, &style); err != nil {
		t.Fatalf("서식을 읽지 못한다: %v", err)
	}
	if style["bold"] != true || style["strike"] != true {
		t.Fatalf("굵게와 취소선까지 함께 사라졌다: %v", style)
	}
}
