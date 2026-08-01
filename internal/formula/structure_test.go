package formula

import "testing"

func TestTransformStructuralReferencesInsertRowsAcrossSheets(t *testing.T) {
	change := StructuralChange{Axis: "row", Action: "insert", Index: 2, Count: 2, CurrentSheet: "Input", TargetSheet: "Input"}
	input := `=A1+$A$2+SUM(B2:C4)+Other!A2+Input!D3+'Input'!E1+LOG10(A2)+"A2"`
	want := `=A1+$A$4+SUM(B4:C6)+Other!A2+Input!D5+'Input'!E1+LOG10(A4)+"A2"`
	if got := TransformStructuralReferences(input, change); got != want {
		t.Fatalf("insert rows = %q, want %q", got, want)
	}
	other := change
	other.CurrentSheet = "Report"
	if got := TransformStructuralReferences(`=A2+Input!B2+'Input'!C3`, other); got != `=A2+Input!B4+'Input'!C5` {
		t.Fatalf("other sheet insert = %q", got)
	}
}

func TestTransformStructuralReferencesDeleteColumnsProducesRefAndContractsRanges(t *testing.T) {
	change := StructuralChange{Axis: "column", Action: "delete", Index: 2, Count: 2, CurrentSheet: "Data", TargetSheet: "Data"}
	input := `=A1+B1+D1+SUM(A1:E5)+SUM(B1:C5)+Other!D1+Data!$E$2`
	want := `=A1+#REF!+B1+SUM(A1:C5)+SUM(#REF!)+Other!D1+Data!$C$2`
	if got := TransformStructuralReferences(input, change); got != want {
		t.Fatalf("delete columns = %q, want %q", got, want)
	}
}

func TestTransformStructuralReferencesPreservesIdentifiersAndEscapedQuotes(t *testing.T) {
	change := StructuralChange{Axis: "row", Action: "insert", Index: 1, Count: 1, CurrentSheet: "Sheet1", TargetSheet: "Sheet1"}
	input := `=Sales2026+A0+SUM(A1)+"A1 and ""B2"""+'Other''s Data'!A1`
	want := `=Sales2026+A0+SUM(A2)+"A1 and ""B2"""+'Other''s Data'!A1`
	if got := TransformStructuralReferences(input, change); got != want {
		t.Fatalf("preserve identifiers = %q, want %q", got, want)
	}
}

func TestTransformRangeAddress(t *testing.T) {
	insert := StructuralChange{Axis: "row", Action: "insert", Index: 3, Count: 2}
	if got, exists, err := TransformRangeAddress("A2:B5", insert); err != nil || !exists || got != "A2:B7" {
		t.Fatalf("insert range = %q, %v, %v", got, exists, err)
	}
	deleteChange := StructuralChange{Axis: "column", Action: "delete", Index: 2, Count: 2}
	if got, exists, err := TransformRangeAddress("B2:C5", deleteChange); err != nil || exists || got != "" {
		t.Fatalf("deleted range = %q, %v, %v", got, exists, err)
	}
}

func TestTransformStructuralReferencesConvertsOverflowToRef(t *testing.T) {
	rowChange := StructuralChange{Axis: "row", Action: "insert", Index: 1, Count: 1, CurrentSheet: "Sheet1", TargetSheet: "Sheet1"}
	if got := TransformStructuralReferences("=A1048576", rowChange); got != "=#REF!" {
		t.Fatalf("row overflow = %q", got)
	}
	columnChange := StructuralChange{Axis: "column", Action: "insert", Index: 1, Count: 1, CurrentSheet: "Sheet1", TargetSheet: "Sheet1"}
	if got := TransformStructuralReferences("=XFD1", columnChange); got != "=#REF!" {
		t.Fatalf("column overflow = %q", got)
	}
	if _, _, err := TransformRangeAddress("A1048576", rowChange); err == nil {
		t.Fatal("metadata range overflow must be rejected")
	}
}
