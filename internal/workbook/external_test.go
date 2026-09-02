package workbook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

type stubFetcher struct {
	calls   int
	answers map[string]formula.ExternalResult
}

func (s *stubFetcher) Resolve(_ context.Context, requests []formula.ExternalRequest) map[string]formula.ExternalResult {
	s.calls++
	out := make(map[string]formula.ExternalResult, len(requests))
	for _, request := range requests {
		key := formula.ExternalKey(request.Function, request.URL)
		if answer, ok := s.answers[request.URL]; ok {
			out[key] = answer
		} else {
			out[key] = formula.ExternalResult{Err: &formula.Error{Code: "#N/A", Message: "허용되지 않은 호스트입니다"}}
		}
	}
	return out
}

func externalCell(t *testing.T, repository Repository, sheetID string, address string) Cell {
	t.Helper()
	cells, err := repository.ReadRange(context.Background(), sheetID, mustArrayRange(t, address+":"+address))
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) == 0 {
		return Cell{}
	}
	return cells[0]
}

// 수식 엔진은 연결을 열지 않는다. 저장소가 설치된 가져오기에 묻고, 답을 평가 전에
// 넘긴다. 아무것도 설치되어 있지 않으면 빈 값이 아니라 이유가 적힌 오류다.
func TestExternalCallsAreResolvedBeforeEvaluation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	fetcher := &stubFetcher{answers: map[string]formula.ExternalResult{
		"https://api.example.com/greeting":  {Text: "안녕"},
		"https://api.example.com/sales.csv": {Rows: 2, Columns: 2, Values: []any{"품목", "금액", "연필", 1200.0}},
	}}
	repository.SetExternalFetcher(fetcher)
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "외부", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	applied, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "ext", Cells: []CellInput{
		{Row: 1, Column: 1, Formula: `=WEBSERVICE("https://api.example.com/greeting")`},
		{Row: 2, Column: 1, Formula: `=SUM(IMPORTDATA("https://api.example.com/sales.csv"))`},
		{Row: 3, Column: 1, Formula: `=WEBSERVICE("https://evil.example.net/")`},
		{Row: 4, Column: 1, Formula: `=WEBSERVICE(B4)`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(externalCell(t, repository, sheet, "A1").Value) != `"안녕"` {
		t.Errorf("A1 = %s", externalCell(t, repository, sheet, "A1").Value)
	}
	if string(externalCell(t, repository, sheet, "A2").Value) != `1200` {
		t.Errorf("A2 = %s", externalCell(t, repository, sheet, "A2").Value)
	}
	codes := map[string]string{}
	for _, failure := range applied.FormulaErrors {
		codes[cellrange.Address(failure.Row, failure.Column)] = failure.Message
	}
	if !strings.Contains(codes["A3"], "허용되지 않은") {
		t.Errorf("A3 는 정책 오류를 말해야 한다: %q", codes["A3"])
	}
	if !strings.Contains(codes["A4"], "큰따옴표") {
		t.Errorf("A4 는 리터럴 주소를 요구해야 한다: %q", codes["A4"])
	}
	if fetcher.calls == 0 {
		t.Fatal("가져오기가 불리지 않았다")
	}
}

func TestWithoutAFetcherEveryExternalCallSaysSo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "외부 없음", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	applied, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "ext", Cells: []CellInput{
		{Row: 1, Column: 1, Formula: `=WEBSERVICE("https://api.example.com/greeting")`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.FormulaErrors) != 1 || !strings.Contains(applied.FormulaErrors[0].Message, "설치되어") {
		t.Fatalf("이유가 적혀야 한다: %#v", applied.FormulaErrors)
	}
	if value := externalCell(t, repository, sheet, "A1").Value; !strings.Contains(string(value), "#N/A") {
		t.Fatalf("A1 = %s", value)
	}
	_ = json.Valid
}
