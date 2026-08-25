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

// 값 표시와 축 범위는 사람이 뜻을 가지고 정하는 것이다. 0 에서 시작하지
// 않으면 작은 차이가 크게 보이므로 저장한 그대로 지켜야 한다.
func TestChartDataLabelsAndAxisBounds(t *testing.T) {
	t.Parallel()
	labels, low, high := true, 10.0, 90.0
	item, err := chartFromInput("wb", "key", "tester", CreateChartInput{
		SheetID: "sheet", SourceSheetID: "sheet", Type: "bar", SourceRange: "A1:B5",
		DataLabels: &labels, YAxisMin: &low, YAxisMax: &high,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !item.DataLabels || item.YAxisMin == nil || *item.YAxisMin != 10 || item.YAxisMax == nil || *item.YAxisMax != 90 {
		t.Fatalf("저장된 차트=%#v", item)
	}
	// 정하지 않으면 자료에 맞춘다.
	plain, err := chartFromInput("wb", "key2", "tester", CreateChartInput{
		SheetID: "sheet", SourceSheetID: "sheet", Type: "bar", SourceRange: "A1:B5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plain.DataLabels || plain.YAxisMin != nil || plain.YAxisMax != nil {
		t.Fatalf("기본 차트=%#v", plain)
	}
	// 아래위를 뒤집어 적으면 그릴 수 없다. 같은 값이면 눈금이 한 줄로 뭉갠다.
	for _, pair := range [][2]float64{{90, 10}, {50, 50}} {
		min, max := pair[0], pair[1]
		if _, err := chartFromInput("wb", "key3", "tester", CreateChartInput{
			SheetID: "sheet", SourceSheetID: "sheet", Type: "bar", SourceRange: "A1:B5",
			YAxisMin: &min, YAxisMax: &max,
		}); !errors.Is(err, ErrInvalid) {
			t.Errorf("최소 %v 최대 %v 가 통과했다: %v", min, max, err)
		}
	}
	// 한쪽만 정해도 된다.
	onlyLow := 5.0
	half, err := chartFromInput("wb", "key4", "tester", CreateChartInput{
		SheetID: "sheet", SourceSheetID: "sheet", Type: "bar", SourceRange: "A1:B5", YAxisMin: &onlyLow,
	})
	if err != nil || half.YAxisMin == nil || half.YAxisMax != nil {
		t.Errorf("한쪽만=%#v %v", half, err)
	}
}

// 일정표는 열을 앞에서부터 이름·시작·끝으로 읽는다. 날짜는 대개 글자다 —
// DATE() 가 글자를 내기 때문이다. 숫자로 적힌 날 수도 함께 받아야 파일에서
// 들어온 표도 그려진다.
func TestTimelineChartReadsNameStartAndEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "일정", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0]
	value := func(input any) json.RawMessage { encoded, _ := json.Marshal(input); return encoded }
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: value("일감")}, {Row: 1, Column: 2, Value: value("시작")}, {Row: 1, Column: 3, Value: value("끝")},
		{Row: 2, Column: 1, Value: value("설계")}, {Row: 2, Column: 2, Value: value("2026-01-05")}, {Row: 2, Column: 3, Value: value("2026-02-10")},
		// 끝을 적지 않으면 그 날 하루짜리 이정표다.
		{Row: 3, Column: 1, Value: value("출시")}, {Row: 3, Column: 2, Value: value("2026-06-01")},
		// 거꾸로 적어도 그릴 수 있게 바로잡는다.
		{Row: 4, Column: 1, Value: value("정리")}, {Row: 4, Column: 2, Value: value("2026-07-10")}, {Row: 4, Column: 3, Value: value("2026-07-01")},
		// 날 수로 적힌 것도 읽는다. 46027 은 2026-01-05 다.
		{Row: 5, Column: 1, Value: value("검토")}, {Row: 5, Column: 2, Value: value(46027)},
		// 시작을 읽을 수 없는 줄은 건너뛴다.
		{Row: 6, Column: 1, Value: value("미정")}, {Row: 6, Column: 2, Value: value("아직")},
	}}); err != nil {
		t.Fatal(err)
	}
	current, _ := repository.GetWorkbook(ctx, book.ID)
	chart, err := repository.CreateChart(ctx, book.ID, "alice", CreateChartInput{
		IdempotencyKey: "tl", SheetID: sheet.ID, SourceSheetID: sheet.ID, Type: "timeline",
		Title: "출시 일정", SourceRange: "A1:C6",
	})
	if err != nil {
		t.Fatalf("일정표 차트=%v (버전 %d)", err, current.Version)
	}
	data, err := repository.GetChartData(ctx, chart.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data.Warning != "" || len(data.Series) != 1 {
		t.Fatalf("일정표 자료=%#v", data)
	}
	points := data.Series[0].Points
	if len(points) != 4 {
		t.Fatalf("읽은 일감=%d개 %#v", len(points), points)
	}
	if points[0].Category != "설계" || points[0].X == nil || *points[0].X != 46027 || points[0].Value == nil || *points[0].Value != 46063 {
		t.Errorf("설계=%#v", points[0])
	}
	// 끝이 없으면 시작과 같다. 기간이 없는 일이 이정표다.
	if points[1].Category != "출시" || points[1].X == nil || points[1].Value == nil || *points[1].X != *points[1].Value {
		t.Errorf("출시=%#v", points[1])
	}
	// 거꾸로 적은 것은 바로잡아 그린다. 그대로 두면 막대가 뒤로 자란다.
	if points[2].X == nil || points[2].Value == nil || *points[2].X >= *points[2].Value {
		t.Errorf("정리=%#v", points[2])
	}
	if points[3].Category != "검토" || points[3].X == nil || *points[3].X != 46027 {
		t.Errorf("검토=%#v", points[3])
	}
}
