package formula

import "testing"

func TestShiftReferencesPreservesAbsoluteAxesAndStrings(t *testing.T) {
	input := `=A1+$B2+C$3+$D$4+SUM(E1:F2)+"A1 and ""B2"""`
	want := `=C3+$B4+E$3+$D$4+SUM(G3:H4)+"A1 and ""B2"""`
	if got := ShiftReferences(input, 2, 2); got != want {
		t.Fatalf("ShiftReferences() = %q, want %q", got, want)
	}
}

func TestShiftReferencesReturnsRefForNegativeRelativeAxis(t *testing.T) {
	if got := ShiftReferences("=A1+$B$2", -1, 0); got != "=#REF!+$B$2" {
		t.Fatalf("ShiftReferences() = %q", got)
	}
}

func TestShiftReferencesDoesNotRewriteIdentifierFragments(t *testing.T) {
	if got := ShiftReferences("=name.A1X+ABC1234_value+A1", 1, 0); got != "=name.A1X+ABC1234_value+A2" {
		t.Fatalf("ShiftReferences() = %q", got)
	}
}

func TestRenameSheetReferencesPreservesStringsAndQuotesNewName(t *testing.T) {
	input := `=Sheet1!A1+'Sheet1'!B2+"Sheet1!C3"+OtherSheet1!D4`
	want := `='Sales Data'!A1+'Sales Data'!B2+"Sheet1!C3"+OtherSheet1!D4`
	if got := RenameSheetReferences(input, "Sheet1", "Sales Data"); got != want {
		t.Fatalf("RenameSheetReferences() = %q, want %q", got, want)
	}
	if got := RenameSheetReferences(`='Bob''s Data'!A1`, "Bob's Data", "Report"); got != `=Report!A1` {
		t.Fatalf("quoted RenameSheetReferences() = %q", got)
	}
}

func TestRenameNamedRangeReferences(t *testing.T) {
	input := `=SUM(Sales_Data)+Sales_Data+"Sales_Data"+'Sales_Data'!A1+Sales_Data!B1`
	want := `=SUM(Revenue)+Revenue+"Sales_Data"+'Sales_Data'!A1+Sales_Data!B1`
	if got := RenameNamedRangeReferences(input, "Sales_Data", "Revenue"); got != want {
		t.Fatalf("RenameNamedRangeReferences() = %q, want %q", got, want)
	}
}
