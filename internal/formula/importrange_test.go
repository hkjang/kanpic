package formula

import "testing"

func TestImportRangeReadsPrefetchedBlock(t *testing.T) {
	imports := map[string]ImportedRange{
		ImportKey("wb-1", "Sheet1!A1:B2"): {Rows: 2, Columns: 2, Values: []any{1.0, 2.0, 3.0, 4.0}},
	}
	evaluator := New().WithImports(imports)
	result := evaluator.Evaluate(`=SUM(IMPORTRANGE("wb-1","Sheet1!A1:B2"))`, nil)
	if result.Error != nil || result.Value != 10.0 {
		t.Fatalf("sum = %#v", result)
	}
	missing := New().Evaluate(`=IMPORTRANGE("wb-9","A1")`, nil)
	if missing.Error == nil || missing.Error.Code != "#REF!" {
		t.Fatalf("missing import = %#v", missing)
	}
	requests := ImportRequests(`=SUM(IMPORTRANGE("wb-1","Sheet1!A1:B2"))+IMPORTRANGE("wb-2","A1")`)
	if len(requests) != 2 || requests[0].Source != "wb-1" || requests[1].Range != "A1" {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestImportRangeRejectsNonLiteralArguments(t *testing.T) {
	result := New().Evaluate(`=IMPORTRANGE(A1,"A1:B2")`, map[string]any{"A1": "wb-1"})
	if result.Error == nil || result.Error.Code != "#VALUE!" {
		t.Fatalf("non-literal import = %#v", result)
	}
	if !IsVolatile(`=IMPORTRANGE("wb-1","A1")`) {
		t.Fatal("IMPORTRANGE must recalculate on every change so a refreshed source shows up")
	}
}

func TestImportedErrorSurfacesItsReason(t *testing.T) {
	imports := map[string]ImportedRange{
		ImportKey("wb-2", "A1:A2"): {Err: formulaError("#REF!", "권한이 없습니다")},
	}
	result := New().WithImports(imports).Evaluate(`=IMPORTRANGE("wb-2","A1:A2")`, nil)
	if result.Error == nil || result.Error.Message != "권한이 없습니다" {
		t.Fatalf("import error = %#v", result.Error)
	}
}
