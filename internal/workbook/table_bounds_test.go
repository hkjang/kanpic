package workbook

import (
	"errors"
	"testing"
)

// A table's range said how wide the table was, and TableColumns took that
// width as the size of a slice. normalizeSheetTable counted the rows and left
// the columns alone, so A1:AAAAAAAA10 was stored and then asked for about
// 120 GB the next time the table's columns were read.
func TestATableCannotCoverMoreCellsThanASheetHolds(t *testing.T) {
	for _, value := range []string{"A1:AAAAAAAA10", "A1:XFD1048576"} {
		_, err := normalizeSheetTable(SheetTable{Name: "표", SheetID: "s", Range: value, HeaderRow: true})
		if err == nil {
			t.Errorf("%s was accepted as a table range", value)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", value, err)
		}
	}
}

func TestAnOrdinaryTableRangeIsStillAccepted(t *testing.T) {
	item, err := normalizeSheetTable(SheetTable{Name: "표", SheetID: "s", Range: "A1:D20", HeaderRow: true})
	if err != nil {
		t.Fatalf("A1:D20 is an ordinary table: %v", err)
	}
	if item.Range != "A1:D20" {
		t.Errorf("range stored as %s, want A1:D20", item.Range)
	}
	if columns := TableColumns(item, []string{"가", "나"}); len(columns) != 4 {
		t.Errorf("table has %d columns, want 4", len(columns))
	}
}

// The print area is laid out one row per row by the print sheet, so a range
// nobody could have filled is a range that stops someone else's browser.
func TestAPrintAreaCannotCoverMoreCellsThanASheetHolds(t *testing.T) {
	mutation := SheetLayoutMutation{Action: "print_area_set", ExpectedRevision: 1, Axis: "row", IdempotencyKey: "k"}
	mutation.Range = "A1:XFD1048576"
	if _, err := normalizeSheetLayoutMutation(mutation); err == nil {
		t.Error("a print area covering the whole sheet was accepted")
	}
	mutation.Range = "A1:D20"
	if _, err := normalizeSheetLayoutMutation(mutation); err != nil {
		t.Errorf("A1:D20 is an ordinary print area: %v", err)
	}
}
