package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestMemoryChartLifecycleUsesLatestCellValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "charts", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "chart-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"월"`)},
		{Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 1, Column: 3, Value: json.RawMessage(`"목표"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"1월"`)},
		{Row: 2, Column: 2, Value: json.RawMessage(`10`)},
		{Row: 2, Column: 3, Value: json.RawMessage(`12`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"2월"`)},
		{Row: 3, Column: 2, Value: json.RawMessage(`20`)},
		{Row: 3, Column: 3, Formula: "=B3+5"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateChart(ctx, book.ID, "alice", CreateChartInput{IdempotencyKey: "chart-one", SheetID: sheet.ID, SourceSheetID: sheet.ID, Type: "bar", Title: "월별 실적", SourceRange: "A1:C3"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.WorkbookVersion != seed.ServerVersion+1 || created.Position != defaultChartPosition() {
		t.Fatalf("created chart = %#v", created)
	}
	duplicate, err := repository.CreateChart(ctx, book.ID, "alice", CreateChartInput{IdempotencyKey: "chart-one", SheetID: sheet.ID, SourceSheetID: sheet.ID, Type: "line", SourceRange: "A1:B2"})
	if err != nil || duplicate.ID != created.ID || duplicate.Type != "bar" {
		t.Fatalf("idempotent chart = %#v, %v", duplicate, err)
	}
	data, err := repository.GetChartData(ctx, created.ID)
	if err != nil || len(data.Series) != 2 || data.Series[0].Name != "매출" || len(data.Series[0].Points) != 2 || data.Series[0].Points[1].Value == nil || *data.Series[0].Points[1].Value != 20 || data.Series[1].Points[1].Value == nil || *data.Series[1].Points[1].Value != 25 {
		t.Fatalf("chart data = %#v, %v", data, err)
	}
	latest, _ := repository.GetWorkbook(ctx, book.ID)
	changed, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: latest.Version, IdempotencyKey: "chart-data-change", Cells: []CellInput{{Row: 2, Column: 2, Value: json.RawMessage(`15`)}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err = repository.GetChartData(ctx, created.ID)
	if err != nil || data.WorkbookVersion != changed.ServerVersion || data.Series[0].Points[0].Value == nil || *data.Series[0].Points[0].Value != 15 {
		t.Fatalf("live chart data = %#v, %v", data, err)
	}
	title := "갱신된 실적"
	updated, err := repository.UpdateChart(ctx, created.ID, "alice", UpdateChartInput{Title: &title, ExpectedRevision: &created.Revision})
	if err != nil || updated.Title != title || updated.Revision != 2 {
		t.Fatalf("updated chart = %#v, %v", updated, err)
	}
	if _, err := repository.UpdateChart(ctx, created.ID, "alice", UpdateChartInput{Title: &title, ExpectedRevision: &created.Revision}); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale chart update error = %v", err)
	}
	if err := repository.DeleteChart(ctx, created.ID, "alice", &created.Revision); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale chart deletion error = %v", err)
	}
	if err := repository.DeleteChart(ctx, created.ID, "alice", &updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetChart(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted chart error = %v", err)
	}
}

func TestMemoryScatterChartUsesFirstColumnAsXValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "scatter", OwnerID: "owner"})
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "owner", BaseVersion: book.Version, IdempotencyKey: "scatter-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"X"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`10`)}, {Row: 2, Column: 2, Value: json.RawMessage(`100`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`20`)}, {Row: 3, Column: 2, Value: json.RawMessage(`250`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	chart, err := repository.CreateChart(ctx, book.ID, "owner", CreateChartInput{IdempotencyKey: "scatter-chart", SheetID: sheet.ID, SourceSheetID: sheet.ID, SourceRange: "A1:B3", Type: "scatter"})
	if err != nil {
		t.Fatal(err)
	}
	if chart.WorkbookVersion != seed.ServerVersion+1 {
		t.Fatalf("chart version = %d, want %d", chart.WorkbookVersion, seed.ServerVersion+1)
	}
	data, err := repository.GetChartData(ctx, chart.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Series) != 1 || data.Series[0].Name != "매출" || len(data.Series[0].Points) != 2 || data.Series[0].Points[0].X == nil || *data.Series[0].Points[0].X != 10 || data.Series[0].Points[0].Value == nil || *data.Series[0].Points[0].Value != 100 {
		t.Fatalf("unexpected scatter data: %#v", data)
	}
}

func TestMemoryChartFollowsStructureVersionRestoreAndDuplication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "chart structure", OwnerID: "alice"})
	sheet := book.Sheets[0]
	chart, err := repository.CreateChart(ctx, book.ID, "alice", CreateChartInput{IdempotencyKey: "structure-chart", SheetID: sheet.ID, SourceSheetID: sheet.ID, Type: "line", Title: "원본", SourceRange: "A1:B3"})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: chart.WorkbookVersion, IdempotencyKey: "chart-row-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	moved, _ := repository.GetChart(ctx, chart.ID)
	if moved.SourceRange != "A1:B4" || moved.Revision != 2 {
		t.Fatalf("moved chart = %#v", moved)
	}
	target, err := repository.CreateVersion(ctx, book.ID, "차트 기준", "alice")
	if err != nil {
		t.Fatal(err)
	}
	title := "임시 변경"
	changed, err := repository.UpdateChart(ctx, chart.ID, "alice", UpdateChartInput{Title: &title, ExpectedRevision: &moved.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RestoreVersion(ctx, target.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	restored, _ := repository.GetChart(ctx, chart.ID)
	if restored.Title != "원본" || restored.SourceRange != "A1:B4" || restored.Revision != moved.Revision {
		t.Fatalf("restored chart = %#v; changed=%#v", restored, changed)
	}
	duplicated, err := repository.DuplicateWorkbook(ctx, book.ID, DuplicateWorkbookInput{OwnerID: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := repository.ListCharts(ctx, duplicated.ID, "")
	if err != nil || len(copied) != 1 || copied[0].WorkbookID != duplicated.ID || copied[0].SheetID == sheet.ID || copied[0].SourceSheetID != copied[0].SheetID {
		t.Fatalf("copied charts = %#v, %v", copied, err)
	}
	latest, _ := repository.GetWorkbook(ctx, book.ID)
	deleted, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: latest.Version, IdempotencyKey: "chart-row-delete", Axis: "row", Action: "delete", Index: 1, Count: 4})
	if err != nil {
		t.Fatal(err)
	}
	_ = inserted
	broken, _ := repository.GetChart(ctx, chart.ID)
	if broken.SourceRange != "#REF!" || broken.Revision != 3 || broken.WorkbookVersion != deleted.ServerVersion {
		t.Fatalf("broken chart = %#v", broken)
	}
}

func TestMemoryChartPreservesBrokenSourceWhenSourceSheetIsDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "chart source deletion"})
	placement := book.Sheets[0]
	source, _ := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "Data"})
	chart, err := repository.CreateChart(ctx, book.ID, "alice", CreateChartInput{IdempotencyKey: "cross-sheet-chart", SheetID: placement.ID, SourceSheetID: source.ID, Type: "pie", SourceRange: "A1:B2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteSheet(ctx, source.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	broken, err := repository.GetChartData(ctx, chart.ID)
	if err != nil || broken.Chart.SourceSheetID != "" || broken.Chart.SourceRange != "#REF!" || broken.Warning != "#REF!" {
		t.Fatalf("broken source chart = %#v, %v", broken, err)
	}
}

// 가져온 차트를 모두 같은 자리에 두면, 두 번째부터는 첫 번째 뒤에 숨는다.
// 사람 눈에는 차트 하나만 들어온 것으로 보이고, 나머지는 가져오지 못한
// 줄 안다. 자리를 다르게 두어 겹치지 않게 한다.
//
// 그림이 원래 놓여 있던 자리는 그리기 관계를 따라가야 알 수 있다. 자리는
// 자료가 아니라 배치이므로, 겹치지 않는 것만 지키면 된다.
func TestImportedChartsDoNotSitOnTopOfEachOther(t *testing.T) {
	t.Parallel()
	seen := map[string]struct{}{}
	for index := 0; index < 4; index++ {
		created, ok := importedChartInput(ImportChart{Type: "bar", SourceRange: "A1:B4"}, "sheet-1", index)
		if !ok {
			t.Fatalf("%d번째 차트를 옮기지 못했다", index)
		}
		if created.Position == nil {
			t.Fatalf("%d번째 차트에 자리가 없다", index)
		}
		key := fmt.Sprintf("%d:%d", created.Position.X, created.Position.Y)
		if _, repeated := seen[key]; repeated {
			t.Fatalf("%d번째 차트가 앞의 차트와 같은 자리(%s)에 놓였다", index, key)
		}
		seen[key] = struct{}{}
	}

	// 종류를 알 수 없거나 범위가 없으면 만들지 않는다.
	for _, item := range []ImportChart{{Type: "", SourceRange: "A1:B4"}, {Type: "bar", SourceRange: ""}, {Type: "없는종류", SourceRange: "A1:B4"}} {
		if _, ok := importedChartInput(item, "sheet-1", 0); ok {
			t.Errorf("%#v 가 받아들여졌다", item)
		}
	}
}
