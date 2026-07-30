package cellrange

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

var ErrInvalidRange = errors.New("invalid A1 range")

type Position struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

func Parse(value string) (Range, error) {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(value)), ":")
	if len(parts) > 2 || len(parts) == 0 {
		return Range{}, ErrInvalidRange
	}
	start, err := parsePosition(parts[0])
	if err != nil {
		return Range{}, err
	}
	end := start
	if len(parts) == 2 {
		end, err = parsePosition(parts[1])
		if err != nil {
			return Range{}, err
		}
	}
	if start.Row > end.Row || start.Column > end.Column {
		return Range{}, fmt.Errorf("%w: range must be top-left to bottom-right", ErrInvalidRange)
	}
	return Range{Start: start, End: end}, nil
}

func parsePosition(value string) (Position, error) {
	value = strings.ReplaceAll(value, "$", "")
	if value == "" {
		return Position{}, ErrInvalidRange
	}
	i := 0
	column := 0
	for i < len(value) && unicode.IsLetter(rune(value[i])) {
		column = column*26 + int(value[i]-'A'+1)
		i++
	}
	if i == 0 || i == len(value) {
		return Position{}, ErrInvalidRange
	}
	row, err := strconv.Atoi(value[i:])
	if err != nil || row < 1 || column < 1 {
		return Position{}, ErrInvalidRange
	}
	return Position{Row: row, Column: column}, nil
}

func (r Range) Contains(row, column int) bool {
	return row >= r.Start.Row && row <= r.End.Row && column >= r.Start.Column && column <= r.End.Column
}

func Address(row, column int) string {
	if row < 1 || column < 1 {
		return ""
	}
	letters := ""
	for column > 0 {
		column--
		letters = string(rune('A'+column%26)) + letters
		column /= 26
	}
	return letters + strconv.Itoa(row)
}
