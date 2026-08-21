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

// Moving a column has to carry every reference to it along, slide whatever the
// column passed over back into the gap, and leave references on other sheets
// and inside strings untouched.
func TestTransformStructuralReferencesMoveColumnForward(t *testing.T) {
	change := StructuralChange{Axis: "column", Action: "move", Index: 2, Count: 1, Destination: 5, CurrentSheet: "Data", TargetSheet: "Data"}
	input := `=B1+A1+C1+D1+E1+Data!$B$2+Other!B1+"B1"`
	want := `=D1+A1+B1+C1+E1+Data!$D$2+Other!B1+"B1"`
	if got := TransformStructuralReferences(input, change); got != want {
		t.Fatalf("move column forward = %q, want %q", got, want)
	}
}

func TestTransformStructuralReferencesMoveRowsBackward(t *testing.T) {
	change := StructuralChange{Axis: "row", Action: "move", Index: 5, Count: 2, Destination: 2, CurrentSheet: "Data", TargetSheet: "Data"}
	input := `=A5+A6+A2+A3+A4+A7`
	want := `=A2+A3+A4+A5+A6+A7`
	if got := TransformStructuralReferences(input, change); got != want {
		t.Fatalf("move rows backward = %q, want %q", got, want)
	}
}

// A range that wholly contains the moved band still covers the same cells, and
// one the move tears in half widens rather than silently dropping cells.
func TestTransformRangeAddressMove(t *testing.T) {
	forward := StructuralChange{Axis: "column", Action: "move", Index: 2, Count: 1, Destination: 6}
	if got, exists, err := TransformRangeAddress("A1:E4", forward); err != nil || !exists || got != "A1:E4" {
		t.Fatalf("containing range = %q, %v, %v", got, exists, err)
	}
	if got, _, _ := TransformRangeAddress("B1:C4", forward); got != "B1:E4" {
		t.Fatalf("torn range = %q, want B1:E4", got)
	}
	if got, _, _ := TransformRangeAddress("D1:E4", forward); got != "C1:D4" {
		t.Fatalf("passed-over range = %q, want C1:D4", got)
	}
	backward := StructuralChange{Axis: "row", Action: "move", Index: 8, Count: 2, Destination: 3}
	if got, _, _ := TransformRangeAddress("A3:B7", backward); got != "A5:B9" {
		t.Fatalf("shifted rows = %q, want A5:B9", got)
	}
	if got, _, _ := TransformRangeAddress("A8:B9", backward); got != "A3:B4" {
		t.Fatalf("moved rows = %q, want A3:B4", got)
	}
}

// A move never destroys a row or a column, so nothing it touches can become
// #REF! and a destination inside the band is simply a no-op.
func TestMovePositionsAreABijection(t *testing.T) {
	change := StructuralChange{Axis: "column", Action: "move", Index: 3, Count: 4, Destination: 12}
	seen := make(map[int]int, 20)
	for position := 1; position <= 20; position++ {
		mapped, exists := TransformPosition(position, change)
		if !exists {
			t.Fatalf("move dropped position %d", position)
		}
		if previous, clash := seen[mapped]; clash {
			t.Fatalf("positions %d and %d both moved to %d", previous, position, mapped)
		}
		seen[mapped] = position
	}
	noop := StructuralChange{Axis: "row", Action: "move", Index: 4, Count: 3, Destination: 5}
	for position := 1; position <= 10; position++ {
		if mapped, _ := TransformPosition(position, noop); mapped != position {
			t.Fatalf("destination inside the band moved %d to %d", position, mapped)
		}
	}
}
