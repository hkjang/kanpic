// Package delimited holds the one rule that decides how a delimited file is
// cut into columns. Two doors lead in — the importer that takes an uploaded
// CSV and IMPORTDATA that takes one over https — and the same file has to be
// read the same way whichever door it comes in by.
package delimited

import "strings"

// separators are what a CSV in the wild is cut by, comma first so a file that
// no candidate cuts more evenly than another stays a comma file.
var separators = []rune{',', ';', '\t'}

// maxSampledLines bounds how far into the body the rule reads. Ten lines is
// enough for a title or a blank line at the top to be outvoted by the rows
// under it, and it keeps the rule from walking a twenty-megabyte body.
const maxSampledLines = 10

// Delimiter answers which separator cuts the body into columns: whichever cuts
// the most of the first few lines into one and the same number of columns,
// comma when none of them does or when two do equally well.
//
// Excel on a locale whose decimal mark is a comma writes and expects
// semicolons, so a file exported there arrives as one column per row unless
// ';' is counted too — a whole sheet in column A, with =SUM over it adding
// nothing up. Reading past the first line is what makes that hold for the
// files reporting tools actually write: a report title or a blank line above
// the header cuts into nothing at all, and letting that one line decide put
// the same whole sheet back into column A.
func Delimiter(body string) rune {
	lines := headLines(body)
	best, bestAgreeing := ',', -1
	for _, separator := range separators {
		if agreeing := agreement(lines, byte(separator)); agreeing > bestAgreeing {
			best, bestAgreeing = separator, agreeing
		}
	}
	return best
}

// headLines takes the first few lines without copying the rest of the body.
func headLines(body string) []string {
	lines := make([]string, 0, maxSampledLines)
	for len(lines) < maxSampledLines && body != "" {
		line, rest, _ := strings.Cut(body, "\n")
		lines, body = append(lines, line), rest
	}
	return lines
}

// agreement counts the lines a separator cuts into one and the same number of
// columns — the most any single column count musters. A line it does not cut at
// all says nothing about it and is left out, which is what lets the rows under
// a one-cell title outvote the title.
func agreement(lines []string, separator byte) int {
	linesWith := make(map[int]int, len(lines))
	best := 0
	for _, line := range lines {
		count := countOutsideQuotes(line, separator)
		if count == 0 {
			continue
		}
		linesWith[count]++
		if linesWith[count] > best {
			best = linesWith[count]
		}
	}
	return best
}

// countOutsideQuotes counts a separator only where it separates. A quoted
// field may hold any of the candidates as ordinary text — `이름,"가,나;다;라"`
// is two columns cut by a comma, not four cut by a semicolon — and counting
// what is inside the quotes would pick the wrong separator for the whole file.
// Each line is read on its own, so an unbalanced quote loses only the line it
// is on rather than everything under it; a file that is nothing but such lines
// counts nothing and so falls back to comma.
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
