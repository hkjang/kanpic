package workbook

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"kanpic/pkg/cellrange"
)

const (
	DefaultSearchLimit  = 50
	MaxSearchLimit      = 200
	MaxSearchOffset     = 1_000_000
	MaxSearchQueryRunes = 256
)

// SearchWorkbookInput is shared by REST, MCP and repository implementations.
// Offset pagination keeps the wire contract simple while limit + 1 fetching
// avoids an additional count over large cell-block tables.
type SearchWorkbookInput struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type WorkbookSearchMatch struct {
	SheetID       string          `json:"sheet_id"`
	SheetName     string          `json:"sheet_name"`
	Address       string          `json:"address"`
	Row           int             `json:"row"`
	Column        int             `json:"column"`
	Value         json.RawMessage `json:"value,omitempty"`
	Formula       string          `json:"formula,omitempty"`
	MatchedFields []string        `json:"matched_fields"`
}

type WorkbookSearchResult struct {
	WorkbookID      string                `json:"workbook_id"`
	WorkbookVersion int64                 `json:"workbook_version"`
	Query           string                `json:"query"`
	Items           []WorkbookSearchMatch `json:"items"`
	NextOffset      *int                  `json:"next_offset,omitempty"`
}

func normalizeSearchInput(input SearchWorkbookInput) (SearchWorkbookInput, error) {
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		return SearchWorkbookInput{}, fmt.Errorf("%w: query is required", ErrInvalid)
	}
	if utf8.RuneCountInString(input.Query) > MaxSearchQueryRunes {
		return SearchWorkbookInput{}, fmt.Errorf("%w: query exceeds %d characters", ErrInvalid, MaxSearchQueryRunes)
	}
	if input.Limit == 0 {
		input.Limit = DefaultSearchLimit
	}
	if input.Limit < 1 || input.Limit > MaxSearchLimit {
		return SearchWorkbookInput{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, MaxSearchLimit)
	}
	if input.Offset < 0 || input.Offset > MaxSearchOffset {
		return SearchWorkbookInput{}, fmt.Errorf("%w: offset must be between 0 and %d", ErrInvalid, MaxSearchOffset)
	}
	return input, nil
}

func searchValueText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

func matchSearchCell(cell Cell, normalizedQuery string) []string {
	fields := make([]string, 0, 2)
	if strings.Contains(strings.ToLower(searchValueText(cell.Value)), normalizedQuery) {
		fields = append(fields, "value")
	}
	if strings.Contains(strings.ToLower(cell.Formula), normalizedQuery) {
		fields = append(fields, "formula")
	}
	return fields
}

func newSearchMatch(sheet Sheet, cell Cell, fields []string) WorkbookSearchMatch {
	return WorkbookSearchMatch{
		SheetID: sheet.ID, SheetName: sheet.Name, Address: cellrange.Address(cell.Row, cell.Column),
		Row: cell.Row, Column: cell.Column, Value: cloneJSON(cell.Value), Formula: cell.Formula,
		MatchedFields: fields,
	}
}
