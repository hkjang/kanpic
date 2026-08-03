package workbook

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxSheetNameRunes = 100

// SheetStats describes one sheet for the sheet manager: how much data it holds,
// where that data ends and when it last changed.
type SheetStats struct {
	SheetID       string     `json:"sheet_id"`
	Name          string     `json:"name"`
	Position      int        `json:"position"`
	Hidden        bool       `json:"hidden"`
	Color         string     `json:"color,omitempty"`
	NonEmptyCells int        `json:"non_empty_cells"`
	FormulaCells  int        `json:"formula_cells"`
	MaxRow        int        `json:"max_row"`
	MaxColumn     int        `json:"max_column"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// CopySheetInput copies a sheet into another workbook, which is how a template
// sheet moves between workbooks without exporting and importing a file.
type CopySheetInput struct {
	TargetWorkbookID string `json:"target_workbook_id"`
	Name             string `json:"name,omitempty"`
	ActorID          string `json:"-"`
}

func validateCopySheetInput(input CopySheetInput) (CopySheetInput, error) {
	input.TargetWorkbookID = strings.TrimSpace(input.TargetWorkbookID)
	input.Name = strings.TrimSpace(input.Name)
	if input.TargetWorkbookID == "" {
		return CopySheetInput{}, fmt.Errorf("%w: target_workbook_id is required", ErrInvalid)
	}
	if utf8.RuneCountInString(input.Name) > MaxSheetNameRunes {
		return CopySheetInput{}, fmt.Errorf("%w: name exceeds %d characters", ErrInvalid, MaxSheetNameRunes)
	}
	return input, nil
}

// availableSheetName appends a numeric suffix until the name is free, matching
// how spreadsheets name copies.
func availableSheetName(requested, fallback string, taken map[string]struct{}) string {
	base := strings.TrimSpace(requested)
	if base == "" {
		base = strings.TrimSpace(fallback)
	}
	if base == "" {
		base = "Sheet"
	}
	if _, exists := taken[strings.ToLower(base)]; !exists {
		return base
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s (%d)", base, index)
		if _, exists := taken[strings.ToLower(candidate)]; !exists {
			return candidate
		}
	}
}

// visibleSheetCount counts the sheets that stay visible when the given sheet is
// excluded, which keeps at least one sheet reachable in the editor.
func visibleSheetCount(sheets map[string]Sheet, excluding string) int {
	count := 0
	for id, sheet := range sheets {
		if id == excluding || sheet.Hidden {
			continue
		}
		count++
	}
	return count
}
