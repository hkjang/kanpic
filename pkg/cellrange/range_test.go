package cellrange

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  Range
	}{
		{"A1", Range{Position{1, 1}, Position{1, 1}}},
		{"$B$2:D10", Range{Position{2, 2}, Position{10, 4}}},
		{"AA12:AB14", Range{Position{12, 27}, Position{14, 28}}},
	}
	for _, tt := range tests {
		got, err := Parse(tt.input)
		if err != nil || got != tt.want {
			t.Fatalf("Parse(%q) = %#v, %v; want %#v", tt.input, got, err, tt.want)
		}
	}
}

func TestParseRejectsInvalidRanges(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "1A", "A0", "B2:A1", "A", "A1:B"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", input)
		}
	}
}

func TestAddress(t *testing.T) {
	t.Parallel()
	if got := Address(12, 28); got != "AB12" {
		t.Fatalf("Address() = %q", got)
	}
}
