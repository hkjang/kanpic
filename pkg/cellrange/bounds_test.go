package cellrange

import "testing"

// A cell reference past the far edge of a sheet names no cell, so it is not a
// position. Before the ceiling went in, the letters were counted without one
// and the number they made sized a slice: TableColumns took the width of a
// table's range, so A1:AAAAAAAA10 asked for about 120 GB.
func TestAPositionPastTheEdgeOfTheSheetIsNotAPosition(t *testing.T) {
	for _, value := range []string{
		"XFE1",                  // one column past the last
		"AAAAA1",                // five letters
		"AAAAAAAA1",             // eight letters, ~8.3e9
		"AAAAAAAAAAAAAAAAAAAA1", // enough to wrap an int
		"A1048577",              // one row past the last
		"A99999999999",          // more rows than a sheet has
	} {
		if _, err := parsePosition(value); err == nil {
			t.Errorf("%s was read as a position, and it names no cell", value)
		}
	}
}

func TestThePositionsASheetHasAreStillRead(t *testing.T) {
	for _, item := range []struct {
		value  string
		row    int
		column int
	}{
		{"A1", 1, 1},
		{"BC12", 12, 55},
		{"XFD1048576", 1048576, 16384}, // the far corner
		{"$D$7", 7, 4},
	} {
		got, err := parsePosition(item.value)
		if err != nil {
			t.Fatalf("%s: %v", item.value, err)
		}
		if got.Row != item.row || got.Column != item.column {
			t.Errorf("%s read as row %d column %d, want row %d column %d",
				item.value, got.Row, got.Column, item.row, item.column)
		}
	}
}

func TestARangeReachingPastTheSheetIsRefused(t *testing.T) {
	for _, value := range []string{"A1:AAAAAAAA10", "A1:XFE5", "A1:A1048577"} {
		if _, err := Parse(value); err == nil {
			t.Errorf("%s was read as a range", value)
		}
	}
	if _, err := Parse("A1:XFD1048576"); err != nil {
		t.Errorf("the whole sheet is an ordinary range: %v", err)
	}
}
