package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// 이름 있는 수식은 저장해 두고 부르는 것이라, 정의가 바뀌면 그것을 쓰는
// 칸이 함께 바뀌어야 한다. 지우면 그 칸은 #NAME? 이 되어야 한다 — 조용히
// 예전 값을 남기면 사람은 아직 살아 있는 줄 안다.
func TestNamedFunctionChangesFlowThroughToCells(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "이름 있는 수식", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "tester", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: value(100)}, {Row: 1, Column: 2, Value: value(60)},
	}}); err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateNamedFunction(ctx, book.ID, "tester", CreateNamedFunctionInput{
		IdempotencyKey: "fn-1", Name: "마진율", Parameters: []string{"매출", "원가"}, Body: "=(매출-원가)/매출",
	})
	if err != nil {
		t.Fatalf("만들기: %v", err)
	}
	// 저장할 때 = 는 떼고 담는다. 부를 때마다 붙였다 뗐다 하면 두 벌이 된다.
	if created.Body != "(매출-원가)/매출" || len(created.Parameters) != 2 {
		t.Fatalf("저장된 정의=%#v", created)
	}
	// 같은 열쇠로 다시 만들면 같은 것이 돌아온다.
	again, err := repository.CreateNamedFunction(ctx, book.ID, "tester", CreateNamedFunctionInput{
		IdempotencyKey: "fn-1", Name: "다른이름", Parameters: []string{"a"}, Body: "a",
	})
	if err != nil || again.ID != created.ID {
		t.Fatalf("멱등성=%#v, %v", again, err)
	}
	current, _ := repository.GetWorkbook(ctx, book.ID)
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "tester", BaseVersion: current.Version, IdempotencyKey: "use", Cells: []CellInput{
		{Row: 1, Column: 3, Formula: "=마진율(A1,B1)"},
	}}); err != nil {
		t.Fatalf("쓰기: %v", err)
	}
	read := func() string {
		cells, readErr := repository.ReadRange(ctx, sheet, mustRange(t, "C1"))
		if readErr != nil || len(cells) != 1 {
			t.Fatalf("C1 읽기=%v %v", cells, readErr)
		}
		return string(cells[0].Value)
	}
	if actual := read(); actual != "0.4" {
		t.Fatalf("C1=%s, 기대=0.4", actual)
	}
	// 정의를 바꾸면 쓰던 칸이 따라 바뀐다.
	body := "=(매출-원가)/원가"
	if _, err := repository.UpdateNamedFunction(ctx, created.ID, "tester", UpdateNamedFunctionInput{Body: &body}); err != nil {
		t.Fatalf("고치기: %v", err)
	}
	if actual := read(); actual != "0.666666666666667" {
		t.Fatalf("고친 뒤 C1=%s", actual)
	}
	// 지우면 쓰던 칸이 #NAME? 이 된다.
	if err := repository.DeleteNamedFunction(ctx, created.ID, "tester", nil); err != nil {
		t.Fatalf("지우기: %v", err)
	}
	if actual := read(); actual != `"#NAME?"` {
		t.Fatalf("지운 뒤 C1=%s", actual)
	}
}

// 셈이 되지 않는 정의를 저장하면 그것을 쓰는 모든 칸이 한꺼번에 깨진다.
// 저장하기 전에 막는다.
func TestNamedFunctionRefusesDefinitionsThatCannotWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "검사", OwnerID: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		reason string
		input  CreateNamedFunctionInput
	}{
		{"이미 있는 함수", CreateNamedFunctionInput{IdempotencyKey: "a", Name: "SUM", Parameters: []string{"x"}, Body: "x"}},
		{"셀 주소", CreateNamedFunctionInput{IdempotencyKey: "b", Name: "A1", Parameters: []string{"x"}, Body: "x"}},
		{"매개변수 겹침", CreateNamedFunctionInput{IdempotencyKey: "c", Name: "DUP", Parameters: []string{"x", "x"}, Body: "x"}},
		{"본문이 수식이 아님", CreateNamedFunctionInput{IdempotencyKey: "d", Name: "BADBODY", Parameters: []string{"x"}, Body: "x+"}},
		{"본문 없음", CreateNamedFunctionInput{IdempotencyKey: "e", Name: "EMPTY", Parameters: []string{"x"}, Body: ""}},
		{"자기 자신을 부름", CreateNamedFunctionInput{IdempotencyKey: "f", Name: "SELF", Parameters: []string{"x"}, Body: "SELF(x)"}},
		{"이름 없음", CreateNamedFunctionInput{IdempotencyKey: "g", Name: "", Parameters: nil, Body: "1"}},
	} {
		if _, err := repository.CreateNamedFunction(ctx, book.ID, "tester", item.input); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", item.reason, err)
		}
	}
	// 같은 이름은 하나뿐이다. 대소문자는 가리지 않는다 — 수식에서 부를 때도
	// 가리지 않기 때문이다.
	if _, err := repository.CreateNamedFunction(ctx, book.ID, "tester", CreateNamedFunctionInput{
		IdempotencyKey: "ok", Name: "마진율", Parameters: []string{"a"}, Body: "a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateNamedFunction(ctx, book.ID, "tester", CreateNamedFunctionInput{
		IdempotencyKey: "dup", Name: "마진율", Parameters: []string{"b"}, Body: "b",
	}); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("같은 이름=%v", err)
	}
	// 이름 있는 수식끼리 부를 수 있다.
	if _, err := repository.CreateNamedFunction(ctx, book.ID, "tester", CreateNamedFunctionInput{
		IdempotencyKey: "chain", Name: "두배마진", Parameters: []string{"a"}, Body: "마진율(a)*2",
	}); err != nil {
		t.Errorf("이어 부르기=%v", err)
	}
}
