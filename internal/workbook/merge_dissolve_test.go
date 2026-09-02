package workbook

import (
	"context"
	"encoding/json"
	"testing"

	"kanpic/pkg/cellrange"
)

func mergedAt(t *testing.T, repository Repository, sheetID, selected string) int {
	t.Helper()
	cells, err := repository.ReadRange(context.Background(), sheetID, mustArrayRange(t, selected))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, cell := range cells {
		if _, merged, err := CellMerge(cell); err != nil {
			t.Fatalf("%s: %v", cellrange.Address(cell.Row, cell.Column), err)
		} else if merged {
			count++
		}
	}
	return count
}

func seedMerge(t *testing.T, repository Repository, selected string) (string, int64) {
	t.Helper()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "merged", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheetID := book.Sheets[0].ID
	inputs, err := BuildMergeCells(nil, mustArrayRange(t, selected), true)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: 1, IdempotencyKey: "merge", Cells: inputs, OperationType: "range.merge"})
	if err != nil {
		t.Fatal(err)
	}
	return sheetID, merged.ServerVersion
}

// 붙여넣은 칸만 병합을 잃고 나머지는 기억하면, 값은 병합 아래 숨고 다음 편집은
// 잘못된 메타데이터라며 거절된다. 병합에 쓰면 그 병합 전체가 같은 작업에서 풀린다.
func TestWritingIntoAMergedCellDissolvesTheWholeMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	sheetID, version := seedMerge(t, repository, "B2:C3")
	if got := mergedAt(t, repository, sheetID, "B2:C3"); got != 4 {
		t.Fatalf("seeded merge covers %d cells", got)
	}
	result, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: version, IdempotencyKey: "paste", OperationType: "cells.paste",
		Cells: []CellInput{{Row: 3, Column: 3, Value: json.RawMessage(`"hidden"`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UnmergedRanges) != 1 || result.UnmergedRanges[0] != "B2:C3" {
		t.Fatalf("unmerged = %#v", result.UnmergedRanges)
	}
	if got := mergedAt(t, repository, sheetID, "B2:C3"); got != 0 {
		t.Fatalf("%d cells still remember the merge", got)
	}
	// 풀린 칸 모두가 한 작업에 들어 있으므로 되돌리기 한 번에 병합이 돌아온다.
	if _, err := repository.UndoOperation(ctx, UndoOperationInput{OperationID: result.OperationID, ActorID: "alice", IdempotencyKey: "undo-paste"}); err != nil {
		t.Fatal(err)
	}
	if got := mergedAt(t, repository, sheetID, "B2:C3"); got != 4 {
		t.Fatalf("after undo %d cells are merged", got)
	}
}

// 채우기가 앵커를 지나가면 예전에는 나머지 칸이 사라진 병합을 가리킨 채 남아,
// 그 칸을 고치려는 다음 쓰기가 거절됐다.
func TestAFillThroughTheAnchorLeavesEveryCellEditable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	sheetID, version := seedMerge(t, repository, "B2:C3")
	fill := make([]CellInput, 0, 4)
	for row := 1; row <= 4; row++ {
		fill = append(fill, CellInput{Row: row, Column: 2, Value: json.RawMessage(`1`)})
	}
	result, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: version, IdempotencyKey: "fill", OperationType: "cells.fill", Cells: fill})
	if err != nil {
		t.Fatal(err)
	}
	if got := mergedAt(t, repository, sheetID, "A1:D4"); got != 0 {
		t.Fatalf("%d cells still remember the merge", got)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: result.ServerVersion, IdempotencyKey: "edit-c3", Cells: []CellInput{{Row: 3, Column: 3, Value: json.RawMessage(`"edited"`)}}}); err != nil {
		t.Fatalf("editing a formerly covered cell: %v", err)
	}
}

// 병합을 유지하는 쓰기 — 서식 덧칠, 같은 병합을 그대로 든 셀 쓰기, 병합 해제 자체 — 는 아무것도 풀지 않는다.
func TestWritesThatKeepTheMergeDissolveNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	sheetID, version := seedMerge(t, repository, "B2:C3")
	formatted, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: version, IdempotencyKey: "bold", OperationType: "range.format",
		StylePatch: json.RawMessage(`{"bold":true}`), Cells: []CellInput{{Row: 2, Column: 2}, {Row: 2, Column: 3}, {Row: 3, Column: 2}, {Row: 3, Column: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(formatted.UnmergedRanges) != 0 || mergedAt(t, repository, sheetID, "B2:C3") != 4 {
		t.Fatalf("formatting dissolved the merge: %#v", formatted.UnmergedRanges)
	}
	cells, err := repository.ReadRange(ctx, sheetID, mustArrayRange(t, "B2:C3"))
	if err != nil {
		t.Fatal(err)
	}
	unmerge, err := BuildMergeCells(cells, mustArrayRange(t, "B2:C3"), false)
	if err != nil {
		t.Fatal(err)
	}
	released, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheetID, ActorID: "alice", BaseVersion: formatted.ServerVersion, IdempotencyKey: "unmerge", OperationType: "range.unmerge", Cells: unmerge})
	if err != nil {
		t.Fatal(err)
	}
	if len(released.UnmergedRanges) != 0 {
		t.Fatalf("an explicit unmerge reported itself as a dissolved merge: %#v", released.UnmergedRanges)
	}
	if released.AppliedCells != 4 {
		t.Fatalf("unmerge applied %d cells", released.AppliedCells)
	}
}
