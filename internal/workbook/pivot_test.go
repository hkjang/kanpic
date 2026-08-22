package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestMemoryPivotAggregatesFiltersCalculatesCachesAndDrillsDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "pivot", OwnerID: "alice"})
	sheet := book.Sheets[0]
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "pivot-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"지역"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"일자"`)}, {Row: 1, Column: 3, Value: json.RawMessage(`"상품"`)}, {Row: 1, Column: 4, Value: json.RawMessage(`"매출"`)}, {Row: 1, Column: 5, Value: json.RawMessage(`"수량"`)}, {Row: 1, Column: 6, Value: json.RawMessage(`"상태"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"동부"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`"2026-01-01"`)}, {Row: 2, Column: 3, Value: json.RawMessage(`"A"`)}, {Row: 2, Column: 4, Value: json.RawMessage(`100`)}, {Row: 2, Column: 5, Value: json.RawMessage(`2`)}, {Row: 2, Column: 6, Value: json.RawMessage(`"승인"`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"동부"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`"2026-01-15"`)}, {Row: 3, Column: 3, Value: json.RawMessage(`"B"`)}, {Row: 3, Column: 4, Value: json.RawMessage(`50`)}, {Row: 3, Column: 5, Value: json.RawMessage(`1`)}, {Row: 3, Column: 6, Value: json.RawMessage(`"반려"`)},
		{Row: 4, Column: 1, Value: json.RawMessage(`"서부"`)}, {Row: 4, Column: 2, Value: json.RawMessage(`"2026-01-20"`)}, {Row: 4, Column: 3, Value: json.RawMessage(`"C"`)}, {Row: 4, Column: 4, Value: json.RawMessage(`200`)}, {Row: 4, Column: 5, Value: json.RawMessage(`4`)}, {Row: 4, Column: 6, Value: json.RawMessage(`"승인"`)},
		{Row: 5, Column: 1, Value: json.RawMessage(`"동부"`)}, {Row: 5, Column: 2, Value: json.RawMessage(`"2026-02-01"`)}, {Row: 5, Column: 3, Value: json.RawMessage(`"D"`)}, {Row: 5, Column: 4, Formula: "=100*3"}, {Row: 5, Column: 5, Value: json.RawMessage(`3`)}, {Row: 5, Column: 6, Value: json.RawMessage(`"승인"`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pivot, err := repository.CreatePivot(ctx, book.ID, "alice", CreatePivotInput{
		IdempotencyKey: "pivot-one", SheetID: sheet.ID, SourceSheetID: sheet.ID, Name: "지역별 월 매출", SourceRange: "A1:F5", RefreshMode: "manual",
		Rows: []PivotDimension{{Column: 1, Name: "지역"}}, Columns: []PivotDimension{{Column: 2, Name: "월", Group: "month"}},
		Values:  []PivotValueField{{Column: 4, Name: "매출 합계", Aggregation: "sum"}, {Column: 5, Name: "수량 합계", Aggregation: "sum"}},
		Filters: []PivotFilter{{Column: 6, Operator: "equals", Value: "승인"}}, CalculatedFields: []PivotCalculatedField{{Name: "단가", Formula: "=V1/V2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pivot.WorkbookVersion != seed.ServerVersion+1 || pivot.Revision != 1 {
		t.Fatalf("created pivot = %#v", pivot)
	}
	idempotent, err := repository.CreatePivot(ctx, book.ID, "alice", CreatePivotInput{IdempotencyKey: "pivot-one", SheetID: sheet.ID, SourceSheetID: sheet.ID, Name: "duplicate", SourceRange: "A1:B2", Values: []PivotValueField{{Column: 1, Aggregation: "count"}}})
	if err != nil || idempotent.ID != pivot.ID || idempotent.Name != pivot.Name {
		t.Fatalf("idempotent pivot = %#v, %v", idempotent, err)
	}
	data, err := repository.GetPivotData(ctx, pivot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !data.Cached || data.SourceRowCount != 3 || len(data.Rows) != 2 || len(data.Columns) != 6 || len(data.GrandTotals) != 6 {
		t.Fatalf("pivot data shape = %#v", data)
	}
	assertPivotRowValues(t, data.Rows[0], []any{100.0, 2.0, 50.0, 300.0, 3.0, 100.0})
	assertPivotRowValues(t, data.Rows[1], []any{200.0, 4.0, 50.0, nil, nil, "#DIV/0!"})
	assertPivotValues(t, data.GrandTotals, []any{300.0, 6.0, 50.0, 300.0, 3.0, 100.0})
	drill, err := repository.PivotDrilldown(ctx, pivot.ID, PivotDrilldownInput{RowKey: data.Rows[0].Key, ColumnKey: data.Columns[0].Key, Limit: 10})
	if err != nil || drill.Total != 1 || len(drill.Rows) != 1 || drill.Rows[0].SourceRow != 2 || drill.Rows[0].Values[3] != float64(100) {
		t.Fatalf("drilldown = %#v, %v", drill, err)
	}
	latest, _ := repository.GetWorkbook(ctx, book.ID)
	_, err = repository.ApplyCells(ctx, CellMutation{SheetID: sheet.ID, ActorID: "alice", BaseVersion: latest.Version, IdempotencyKey: "pivot-source-change", Cells: []CellInput{{Row: 2, Column: 4, Value: json.RawMessage(`150`)}}})
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := repository.GetPivotData(ctx, pivot.ID)
	if stale.Rows[0].Values[0] != float64(100) || stale.SourceVersion != data.SourceVersion {
		t.Fatalf("manual pivot should remain cached: %#v", stale.Rows[0].Values)
	}
	refreshed, err := repository.RefreshPivot(ctx, pivot.ID, "alice")
	if err != nil || refreshed.Rows[0].Values[0] != float64(150) || refreshed.SourceVersion == data.SourceVersion {
		t.Fatalf("refreshed pivot = %#v, %v", refreshed, err)
	}
	name := "변경"
	if _, err := repository.UpdatePivot(ctx, pivot.ID, "alice", UpdatePivotInput{Name: &name, ExpectedRevision: &pivot.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdatePivot(ctx, pivot.ID, "alice", UpdatePivotInput{Name: &name, ExpectedRevision: &pivot.Revision}); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestPivotWithoutColumnDimensionsUsesEmptyLabelArrays(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(context.Background(), CreateWorkbookInput{Title: "labels", OwnerID: "alice"})
	pivot, err := repository.CreatePivot(context.Background(), book.ID, "alice", CreatePivotInput{IdempotencyKey: "labels", SheetID: book.Sheets[0].ID, SourceSheetID: book.Sheets[0].ID, Name: "labels", SourceRange: "A1:B2", Rows: []PivotDimension{{Column: 1}}, Values: []PivotValueField{{Column: 2, Aggregation: "sum"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := repository.GetPivotData(context.Background(), pivot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Columns) != 1 || data.Columns[0].Labels == nil || len(data.Rows) != 1 || data.Rows[0].Labels == nil {
		t.Fatalf("pivot labels must be JSON arrays: %#v", data)
	}
}

func TestPivotCustomGroupingValidation(t *testing.T) {
	t.Parallel()
	base := Pivot{SheetID: "sheet", SourceSheetID: "sheet", Name: "custom", SourceRange: "A1:B2", Values: []PivotValueField{{Column: 2, Aggregation: "sum"}}}
	for name, dimension := range map[string]PivotDimension{
		"empty groups":     {Column: 1, Group: "custom"},
		"empty values":     {Column: 1, Group: "custom", CustomGroups: []PivotCustomGroup{{Name: "A"}}},
		"duplicate names":  {Column: 1, Group: "custom", CustomGroups: []PivotCustomGroup{{Name: "A", Values: []string{"1"}}, {Name: "a", Values: []string{"2"}}}},
		"duplicate values": {Column: 1, Group: "custom", CustomGroups: []PivotCustomGroup{{Name: "A", Values: []string{"One"}}, {Name: "B", Values: []string{"one"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Rows = []PivotDimension{dimension}
			if _, err := normalizePivot(input, false); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid custom grouping, got %v", err)
			}
		})
	}
	input := base
	input.Rows = []PivotDimension{{Column: 1, Group: "none", CustomGroups: []PivotCustomGroup{{Name: "ignored", Values: []string{"A"}}}}}
	normalized, err := normalizePivot(input, false)
	if err != nil || normalized.Rows[0].CustomGroups != nil {
		t.Fatalf("non-custom groups should discard custom configuration: %#v, %v", normalized.Rows, err)
	}
}

func TestMemoryPivotGroupingStructureRestoreDuplicationAndBrokenSource(t *testing.T) {
	t.Parallel()
	custom := PivotDimension{Column: 1, Group: "custom", CustomGroups: []PivotCustomGroup{{Name: "핵심", Values: []string{"A", "B"}}}}
	if groupPivotValue("A", custom) != "핵심" || groupPivotValue("C", custom) != "기타" {
		t.Fatal("custom grouping failed")
	}
	if groupPivotValue(float64(24), PivotDimension{Column: 1, Group: "number", Interval: 10}) != "20 – 30" {
		t.Fatal("numeric grouping failed")
	}
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, _ := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "pivot lifecycle", OwnerID: "alice"})
	placement := book.Sheets[0]
	source, _ := repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: "Data"})
	pivot, err := repository.CreatePivot(ctx, book.ID, "alice", CreatePivotInput{IdempotencyKey: "life", SheetID: placement.ID, SourceSheetID: source.ID, Name: "수명", SourceRange: "A1:B3", Values: []PivotValueField{{Column: 2, Aggregation: "sum"}}})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := repository.ApplyStructure(ctx, StructuralMutation{SheetID: source.ID, ActorID: "alice", BaseVersion: pivot.WorkbookVersion, IdempotencyKey: "pivot-insert", Axis: "row", Action: "insert", Index: 2, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	moved, _ := repository.GetPivot(ctx, pivot.ID)
	if moved.SourceRange != "A1:B4" || moved.Revision != 2 {
		t.Fatalf("moved pivot = %#v", moved)
	}
	if _, err := repository.RefreshPivot(ctx, pivot.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	version, _ := repository.CreateVersion(ctx, book.ID, "피벗 기준", "alice")
	changed := "임시"
	updated, _ := repository.UpdatePivot(ctx, pivot.ID, "alice", UpdatePivotInput{Name: &changed, ExpectedRevision: &moved.Revision})
	if _, err := repository.RestoreVersion(ctx, version.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	restored, _ := repository.GetPivot(ctx, pivot.ID)
	if restored.Name != "수명" || restored.SourceRange != "A1:B4" || restored.Revision != moved.Revision || restored.SourceVersion != 0 || restored.LastRefreshedAt != nil {
		t.Fatalf("restored=%#v changed=%#v", restored, updated)
	}
	duplicated, err := repository.DuplicateWorkbook(ctx, book.ID, DuplicateWorkbookInput{OwnerID: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := repository.ListPivots(ctx, duplicated.ID, "")
	if err != nil || len(copied) != 1 || copied[0].WorkbookID != duplicated.ID || copied[0].SheetID == placement.ID || copied[0].SourceSheetID == source.ID {
		t.Fatalf("copied pivots = %#v, %v", copied, err)
	}
	latest, _ := repository.GetWorkbook(ctx, book.ID)
	_ = inserted
	if latest.Version <= moved.WorkbookVersion {
		t.Fatalf("unexpected latest version %d", latest.Version)
	}
	if _, err := repository.DeleteSheet(ctx, source.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	broken, err := repository.GetPivotData(ctx, pivot.ID)
	if err != nil || broken.Warning != "#REF!" || broken.Pivot.SourceSheetID != "" || broken.Pivot.SourceRange != "#REF!" {
		t.Fatalf("broken pivot = %#v, %v", broken, err)
	}
}

func assertPivotRowValues(t *testing.T, row PivotResultRow, expected []any) {
	t.Helper()
	assertPivotValues(t, row.Values, expected)
}

func assertPivotValues(t *testing.T, actual, expected []any) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("values length=%d want=%d: %#v", len(actual), len(expected), actual)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("value[%d]=%#v want %#v; all=%#v", index, actual[index], expected[index], actual)
		}
	}
}

// A manually refreshed pivot keeps showing the numbers it was built from, so
// the only way to know they are old is to be told.
func TestMemoryPivotReportsStaleSourceUntilRefreshed(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "피벗 신선도", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	sheet := book.Sheets[0].ID
	seed, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: book.Version, IdempotencyKey: "pivot-seed", Cells: []CellInput{
		{Row: 1, Column: 1, Value: json.RawMessage(`"지역"`)}, {Row: 1, Column: 2, Value: json.RawMessage(`"매출"`)},
		{Row: 2, Column: 1, Value: json.RawMessage(`"서울"`)}, {Row: 2, Column: 2, Value: json.RawMessage(`100`)},
		{Row: 3, Column: 1, Value: json.RawMessage(`"부산"`)}, {Row: 3, Column: 2, Value: json.RawMessage(`50`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pivot, err := repository.CreatePivot(ctx, book.ID, "alice", CreatePivotInput{
		IdempotencyKey: "pivot-stale", SheetID: sheet, SourceSheetID: sheet, Name: "지역 합계", SourceRange: "A1:B3",
		Rows: []PivotDimension{{Column: 1}}, Values: []PivotValueField{{Column: 2, Aggregation: "sum"}}, RefreshMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := repository.GetPivotData(ctx, pivot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Stale {
		t.Fatalf("a pivot built just now is not stale: %#v", fresh)
	}
	if _, err := repository.ApplyCells(ctx, CellMutation{SheetID: sheet, ActorID: "alice", BaseVersion: seed.ServerVersion + 1, IdempotencyKey: "pivot-change", Cells: []CellInput{{Row: 2, Column: 2, Value: json.RawMessage(`900`)}}}); err != nil {
		t.Fatal(err)
	}
	stale, err := repository.GetPivotData(ctx, pivot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale || stale.SourceChangedAt == nil {
		t.Fatalf("changed source must be reported: %#v", stale)
	}
	// The cached numbers are still what the pivot was built from.
	if !stale.Cached {
		t.Fatalf("manual pivot should still serve its cache: %#v", stale)
	}
	refreshed, err := repository.RefreshPivot(ctx, pivot.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Stale {
		t.Fatalf("refreshed pivot = %#v", refreshed)
	}
	after, err := repository.GetPivotData(ctx, pivot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stale {
		t.Fatalf("pivot went stale again right after a refresh: %#v", after)
	}
}
