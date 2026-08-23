package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"kanpic/pkg/cellrange"
)

func TestBuildSortCellsUsesStableMultiKeyAndMovesFormulasAndStyles(t *testing.T) {
	selected, _ := cellrange.Parse("A1:C4")
	existing := []Cell{
		{Row: 1, Column: 1, Value: json.RawMessage(`"Name"`)},
		{Row: 1, Column: 2, Value: json.RawMessage(`"Quantity"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"beta"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`2`)}, {Row: 2, Column: 3, Value: json.RawMessage(`20`), Formula: "=B2*10", Style: json.RawMessage(`{"background":"#fff000"}`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"Alpha"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`10`)}, {Row: 3, Column: 3, Value: json.RawMessage(`100`), Formula: "=B3*10", Style: json.RawMessage(`{"bold":true}`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"alpha"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`5`)}, {Row: 4, Column: 3, Value: json.RawMessage(`50`), Formula: "=B4*10"},
	}
	inputs, err := BuildSortCells(existing, selected, SortOptions{HeaderRows: 1, Keys: []SortKey{{Column: 1, Direction: "asc"}, {Column: 2, Direction: "desc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 9 {
		t.Fatalf("sorted inputs = %d", len(inputs))
	}
	if string(inputs[0].Value) != `"Alpha"` || string(inputs[3].Value) != `"alpha"` || string(inputs[6].Value) != `"beta"` {
		t.Fatalf("row order: %#v", inputs)
	}
	if inputs[2].Formula != "=B2*10" || inputs[5].Formula != "=B3*10" || inputs[8].Formula != "=B4*10" {
		t.Fatalf("shifted formulas: %q %q %q", inputs[2].Formula, inputs[5].Formula, inputs[8].Formula)
	}
	if string(inputs[2].Style) != `{"bold":true}` || string(inputs[8].Style) != `{"background":"#fff000"}` {
		t.Fatalf("styles did not move with rows: %#v", inputs)
	}
}

func TestBuildSortCellsKeepsBlanksLastForDescendingSort(t *testing.T) {
	selected, _ := cellrange.Parse("A1:A4")
	inputs, err := BuildSortCells([]Cell{{Row: 1, Column: 1, Value: json.RawMessage(`2`)}, {Row: 3, Column: 1, Value: json.RawMessage(`9`)}, {Row: 4, Column: 1, Value: json.RawMessage(`1`)}}, selected, SortOptions{Keys: []SortKey{{Column: 1, Direction: "desc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(inputs[0].Value) != "9" || string(inputs[1].Value) != "2" || string(inputs[2].Value) != "1" || len(inputs[3].Value) != 0 {
		t.Fatalf("descending order with blank: %#v", inputs)
	}
}

func TestBuildSortCellsRejectsInvalidKeysMergedCellsAndOversizedRange(t *testing.T) {
	selected, _ := cellrange.Parse("A1:B3")
	if _, err := BuildSortCells(nil, selected, SortOptions{Keys: []SortKey{{Column: 3, Direction: "asc"}}}); err == nil {
		t.Fatal("expected outside key rejection")
	}
	merged, err := BuildMergeCells(nil, selected, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSortCells(inputsAsCells(merged), selected, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}}); err == nil {
		t.Fatal("expected merged range rejection")
	}
	if _, err := BuildSortCells([]Cell{{Row: 2, Column: 1, Value: json.RawMessage(`2`), SpillSource: "A1"}}, selected, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}}); err == nil {
		t.Fatal("expected array result sort rejection")
	}
	// Sorting has its own ceiling, above the paste limit, because it rewrites
	// a whole range in one deliberate action.
	large, _ := cellrange.Parse(fmt.Sprintf("A1:B%d", MaxSortCells/2+1))
	if _, err := BuildSortCells(nil, large, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}}); err == nil {
		t.Fatal("expected operation limit rejection")
	}
	withinLimit, _ := cellrange.Parse("A1:B5001")
	if _, err := BuildSortCells(nil, withinLimit, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}}); err != nil {
		t.Fatalf("a range inside the sort limit was refused: %v", err)
	}
}

// Overwriting a cell that produced an array result has to clear what it
// spilled. That used to be found by scanning the whole sheet once per written
// cell, which made a sort quadratic — 16,000 rows took 18 seconds. The
// clearing still has to happen; only the hunting is gone.
func TestOverwritingAnArrayFormulaStillClearsWhatItSpilled(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "스필", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "owner", IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`1`)}, {Row: 2, Column: 1, Value: json.RawMessage(`2`)}, {Row: 3, Column: 1, Value: json.RawMessage(`3`)},
		{Row: 1, Column: 3, Formula: "=A1:A3"},
	}}); err != nil {
		t.Fatal(err)
	}
	spilled, _ := cellrange.Parse("C1:C3")
	filled, err := repository.ReadRange(ctx, sheet, spilled)
	if err != nil || len(filled) != 3 {
		t.Fatalf("the array formula did not spill: %#v, %v", filled, err)
	}
	// Writing a plain value over the source has to take the spill with it.
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "owner", IdempotencyKey: "overwrite",
		Cells: []CellInput{{Row: 1, Column: 3, Value: json.RawMessage(`"보통 값"`)}}}); err != nil {
		t.Fatal(err)
	}
	after, err := repository.ReadRange(ctx, sheet, spilled)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Row != 1 || string(after[0].Value) != `"보통 값"` {
		t.Fatalf("C1:C3 after overwriting the source = %#v", after)
	}
}

// 글자 그대로 견주면 월 이름이 10월, 12월, 1월 순으로 늘어선다. 값 안의
// 숫자를 하나의 수로 세면 사람이 읽는 순서가 된다.
func TestSortReadsTheNumbersInsideText(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		values  []string
		literal bool
		want    []string
	}{
		{"월 이름", []string{"2월", "10월", "1월", "12월", "3월"}, false, []string{"1월", "2월", "3월", "10월", "12월"}},
		{"글자 그대로", []string{"2월", "10월", "1월"}, true, []string{"10월", "1월", "2월"}},
		{"번호가 붙은 이름", []string{"항목10", "항목2", "항목1"}, false, []string{"항목1", "항목2", "항목10"}},
		{"가운데 숫자", []string{"a10b", "a2b", "a2a"}, false, []string{"a2a", "a2b", "a10b"}},
		{"숫자가 없는 글자", []string{"나", "가", "다"}, false, []string{"가", "나", "다"}},
		// 자릿수를 맞춰 쓴 값은 같은 수라도 순서가 흔들리면 안 된다.
		{"앞자리 0", []string{"7호", "07호"}, false, []string{"07호", "7호"}},
		// 마흔 자리 계좌번호는 수량이 아니므로 글자로 견준다.
		{"아주 긴 숫자", []string{"9999999999999999999", "1111111111111111111"}, false, []string{"1111111111111111111", "9999999999999999999"}},
		// 정렬은 화면에서 먼저 반영하고 서버가 다시 확정한다. 둘이 어긋나면
		// 줄이 눈앞에서 한 번 튄다. 자바스크립트의 기본 문자열 비교는 UTF-16
		// 조각을 견주므로 이모지를 ￦(U+FFE6) 앞에 놓지만, 여기서는 코드포인트
		// 차례대로 뒤에 놓는다. web/src/lib/naturalOrder.test.ts 가 **같은 값**
		// 을 고정하고 있으니 한쪽만 고치면 양쪽 다 걸린다.
		{"이모지와 전각 글자", []string{"￦100", "😀항목", "＀", "🍎사과"}, false, []string{"＀", "￦100", "🍎사과", "😀항목"}},
	} {
		cells := make([]Cell, 0, len(testCase.values))
		for index, value := range testCase.values {
			encoded, _ := json.Marshal(value)
			cells = append(cells, Cell{Row: index + 1, Column: 1, Value: encoded})
		}
		selected, _ := cellrange.Parse(fmt.Sprintf("A1:A%d", len(testCase.values)))
		sorted, err := BuildSortCells(cells, selected, SortOptions{Keys: []SortKey{{Column: 1, Direction: "asc"}}, LiteralOrder: testCase.literal})
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		got := make([]string, len(testCase.values))
		for _, cell := range sorted {
			var text string
			_ = json.Unmarshal(cell.Value, &text)
			got[cell.Row-1] = text
		}
		if !reflect.DeepEqual(got, testCase.want) {
			t.Fatalf("%s = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}
