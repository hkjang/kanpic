// Package delimited holds the one rule that decides how a delimited file is
// cut into columns. Two doors lead in — the importer that takes an uploaded
// CSV and IMPORTDATA that takes one over https — and the same file has to be
// read the same way whichever door it comes in by.
package delimited

import "strings"

// separators are what a CSV in the wild is cut by, comma first so a line that
// holds as many of another separator as it holds commas stays a comma file.
var separators = []rune{',', ';', '\t'}

// Delimiter answers which separator cuts the first line into columns:
// whichever appears most often outside quotes, comma when none appears.
// Excel on a locale whose decimal mark is a comma writes and expects
// semicolons, so a file exported there arrives as one column per row unless
// ';' is counted too — a whole sheet in column A, with =SUM over it adding
// nothing up.
func Delimiter(body string) rune {
	line, _, _ := strings.Cut(body, "\n")
	best, bestCount := ',', -1
	for _, separator := range separators {
		if count := countOutsideQuotes(line, byte(separator)); count > bestCount {
			best, bestCount = separator, count
		}
	}
	return best
}

// countOutsideQuotes counts a separator only where it separates. A quoted
// field may hold any of the candidates as ordinary text — `이름,"가,나;다;라"`
// is two columns cut by a comma, not four cut by a semicolon — and counting
// what is inside the quotes would pick the wrong separator for the whole file.
// An unbalanced quote leaves the rest of the line looking quoted, which counts
// nothing and so falls back to comma.
func countOutsideQuotes(line string, separator byte) int {
	count, quoted := 0, false
	for index := 0; index < len(line); index++ {
		switch {
		case line[index] == '"':
			quoted = !quoted
		case !quoted && line[index] == separator:
			count++
		}
	}
	return count
}
