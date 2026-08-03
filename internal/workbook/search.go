package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"kanpic/pkg/cellrange"
)

const (
	DefaultSearchLimit    = 50
	MaxSearchLimit        = 200
	MaxSearchOffset       = 1_000_000
	MaxSearchQueryRunes   = 256
	MaxReplacementRunes   = 256
	MaxReplaceCells       = 5_000
	MaxReplacePreviewRows = 200
)

// SearchWorkbookInput is shared by REST, MCP and repository implementations.
// Offset pagination keeps the wire contract simple while limit + 1 fetching
// avoids an additional count over large cell-block tables.
//
// The zero value keeps the original contract: a case-insensitive substring
// search over every sheet, matching both stored values and formulas.
type SearchWorkbookInput struct {
	Query        string `json:"query"`
	SheetID      string `json:"sheet_id,omitempty"`
	MatchCase    bool   `json:"match_case,omitempty"`
	WholeCell    bool   `json:"whole_cell,omitempty"`
	UseRegex     bool   `json:"use_regex,omitempty"`
	SkipFormulas bool   `json:"skip_formulas,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
}

type WorkbookSearchMatch struct {
	SheetID       string          `json:"sheet_id"`
	SheetName     string          `json:"sheet_name"`
	Address       string          `json:"address"`
	Row           int             `json:"row"`
	Column        int             `json:"column"`
	Value         json.RawMessage `json:"value,omitempty"`
	Formula       string          `json:"formula,omitempty"`
	Style         json.RawMessage `json:"style,omitempty"`
	SpillSource   string          `json:"spill_source,omitempty"`
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
	// A regular expression may legitimately anchor on leading or trailing
	// whitespace, so only plain queries are trimmed.
	if !input.UseRegex {
		input.Query = strings.TrimSpace(input.Query)
	}
	input.SheetID = strings.TrimSpace(input.SheetID)
	if input.Query == "" {
		return SearchWorkbookInput{}, fmt.Errorf("%w: query is required", ErrInvalid)
	}
	if utf8.RuneCountInString(input.Query) > MaxSearchQueryRunes {
		return SearchWorkbookInput{}, fmt.Errorf("%w: query exceeds %d characters", ErrInvalid, MaxSearchQueryRunes)
	}
	if input.UseRegex {
		if _, err := compileSearchPattern(input); err != nil {
			return SearchWorkbookInput{}, err
		}
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

func compileSearchPattern(input SearchWorkbookInput) (*regexp.Regexp, error) {
	expression := input.Query
	if input.WholeCell {
		expression = `\A(?:` + expression + `)\z`
	}
	if !input.MatchCase {
		expression = `(?i)` + expression
	}
	pattern, err := regexp.Compile(expression)
	if err != nil {
		return nil, fmt.Errorf("%w: query is not a valid regular expression", ErrInvalid)
	}
	return pattern, nil
}

// searchMatcher evaluates one normalized query against cell text so the
// in-memory repository, the replace planner and the PostgreSQL result
// verification all agree on what counts as a match.
type searchMatcher struct {
	needle       string
	matchCase    bool
	wholeCell    bool
	skipFormulas bool
	pattern      *regexp.Regexp
}

func newSearchMatcher(input SearchWorkbookInput) (searchMatcher, error) {
	matcher := searchMatcher{needle: input.Query, matchCase: input.MatchCase, wholeCell: input.WholeCell, skipFormulas: input.SkipFormulas}
	if !input.MatchCase {
		matcher.needle = strings.ToLower(input.Query)
	}
	if input.UseRegex {
		pattern, err := compileSearchPattern(input)
		if err != nil {
			return searchMatcher{}, err
		}
		matcher.pattern = pattern
	}
	return matcher, nil
}

func (m searchMatcher) matchesText(text string) bool {
	if m.pattern != nil {
		return m.pattern.MatchString(text)
	}
	if m.matchCase {
		if m.wholeCell {
			return text == m.needle
		}
		return strings.Contains(text, m.needle)
	}
	lowered := strings.ToLower(text)
	if m.wholeCell {
		return lowered == m.needle
	}
	return strings.Contains(lowered, m.needle)
}

// replaceText returns the rewritten text and whether anything changed.
func (m searchMatcher) replaceText(text, replacement string) (string, bool) {
	if m.pattern != nil {
		result := m.pattern.ReplaceAllString(text, replacement)
		return result, result != text
	}
	if m.wholeCell {
		if !m.matchesText(text) {
			return text, false
		}
		return replacement, replacement != text
	}
	if m.matchCase {
		result := strings.ReplaceAll(text, m.needle, replacement)
		return result, result != text
	}
	result := replaceFold(text, m.needle, replacement)
	return result, result != text
}

// replaceFold performs a case-insensitive replacement while preserving the
// untouched parts of the original text byte for byte. Lowercasing rune by rune
// can change byte lengths, so the offset table maps every lowered byte back to
// the byte where its source rune starts.
func replaceFold(text, loweredNeedle, replacement string) string {
	if loweredNeedle == "" {
		return text
	}
	var lowered strings.Builder
	offsets := make([]int, 0, len(text)+1)
	for index, character := range text {
		start := lowered.Len()
		lowered.WriteRune(unicode.ToLower(character))
		for position := start; position < lowered.Len(); position++ {
			offsets = append(offsets, index)
		}
	}
	offsets = append(offsets, len(text))
	source := lowered.String()
	var builder strings.Builder
	position := 0
	for position+len(loweredNeedle) <= len(source) {
		index := strings.Index(source[position:], loweredNeedle)
		if index < 0 {
			break
		}
		start := position + index
		builder.WriteString(text[offsets[position]:offsets[start]])
		builder.WriteString(replacement)
		position = start + len(loweredNeedle)
	}
	builder.WriteString(text[offsets[position]:])
	return builder.String()
}

func (m searchMatcher) fields(cell Cell) []string {
	fields := make([]string, 0, 2)
	if m.matchesText(searchValueText(cell.Value)) {
		fields = append(fields, "value")
	}
	if !m.skipFormulas && cell.Formula != "" && m.matchesText(cell.Formula) {
		fields = append(fields, "formula")
	}
	return fields
}

func newSearchMatch(sheet Sheet, cell Cell, fields []string) WorkbookSearchMatch {
	return WorkbookSearchMatch{
		SheetID: sheet.ID, SheetName: sheet.Name, Address: cellrange.Address(cell.Row, cell.Column),
		Row: cell.Row, Column: cell.Column, Value: cloneJSON(cell.Value), Formula: cell.Formula,
		Style: cloneJSON(cell.Style), SpillSource: cell.SpillSource, MatchedFields: fields,
	}
}

// ReplaceWorkbookInput reuses the search predicate so a preview and the applied
// change always select the same cells.
type ReplaceWorkbookInput struct {
	Query          string `json:"query"`
	Replacement    string `json:"replacement"`
	SheetID        string `json:"sheet_id,omitempty"`
	Range          string `json:"range,omitempty"`
	MatchCase      bool   `json:"match_case,omitempty"`
	WholeCell      bool   `json:"whole_cell,omitempty"`
	UseRegex       bool   `json:"use_regex,omitempty"`
	SkipFormulas   bool   `json:"skip_formulas,omitempty"`
	Preview        bool   `json:"preview,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
	ActorID        string `json:"-"`
}

type ReplacePreviewItem struct {
	SheetID   string `json:"sheet_id"`
	SheetName string `json:"sheet_name"`
	Address   string `json:"address"`
	Row       int    `json:"row"`
	Column    int    `json:"column"`
	Field     string `json:"field"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

type ReplaceSheetOperation struct {
	SheetID       string         `json:"sheet_id"`
	SheetName     string         `json:"sheet_name"`
	ReplacedCells int            `json:"replaced_cells"`
	Operation     MutationResult `json:"operation"`
	Cells         []CellInput    `json:"-"`
}

type ReplaceWorkbookResult struct {
	WorkbookID      string                  `json:"workbook_id"`
	WorkbookVersion int64                   `json:"workbook_version"`
	Query           string                  `json:"query"`
	Replacement     string                  `json:"replacement"`
	Preview         bool                    `json:"preview"`
	MatchedCells    int                     `json:"matched_cells"`
	PlannedCells    int                     `json:"planned_cells"`
	ReplacedCells   int                     `json:"replaced_cells"`
	SkippedCells    int                     `json:"skipped_cells"`
	Items           []ReplacePreviewItem    `json:"items"`
	Sheets          []ReplaceSheetOperation `json:"sheets"`
	ServerVersion   int64                   `json:"server_version"`
}

// ReplaceRepository is the narrow slice of Repository the replace planner
// needs, which keeps the planner testable against the in-memory store.
type ReplaceRepository interface {
	SearchWorkbook(context.Context, string, SearchWorkbookInput) (WorkbookSearchResult, error)
	ApplyCells(context.Context, CellMutation) (MutationResult, error)
}

type plannedReplacement struct {
	match WorkbookSearchMatch
	field string
	after string
	input CellInput
}

func (input ReplaceWorkbookInput) searchInput() SearchWorkbookInput {
	return SearchWorkbookInput{
		Query: input.Query, SheetID: input.SheetID, MatchCase: input.MatchCase,
		WholeCell: input.WholeCell, UseRegex: input.UseRegex, SkipFormulas: input.SkipFormulas,
		Limit: MaxSearchLimit,
	}
}

// replacedValue keeps the stored JSON type stable: text stays text, while a
// number or boolean is re-parsed so a replaced literal does not silently turn
// every affected cell into a string.
func replacedValue(original json.RawMessage, text string) (json.RawMessage, error) {
	var decoded any
	textual := true
	if len(original) > 0 {
		if err := json.Unmarshal(original, &decoded); err == nil {
			_, textual = decoded.(string)
		}
	}
	if !textual {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			var candidate any
			if err := json.Unmarshal([]byte(trimmed), &candidate); err == nil {
				switch candidate.(type) {
				case float64, bool:
					return json.RawMessage(trimmed), nil
				}
			}
		}
	}
	if text == "" {
		return nil, nil
	}
	return json.Marshal(text)
}

func normalizeReplaceInput(input ReplaceWorkbookInput) (ReplaceWorkbookInput, error) {
	if utf8.RuneCountInString(input.Replacement) > MaxReplacementRunes {
		return ReplaceWorkbookInput{}, fmt.Errorf("%w: replacement exceeds %d characters", ErrInvalid, MaxReplacementRunes)
	}
	normalized, err := normalizeSearchInput(input.searchInput())
	if err != nil {
		return ReplaceWorkbookInput{}, err
	}
	input.Query = normalized.Query
	input.SheetID = normalized.SheetID
	input.Range = strings.TrimSpace(input.Range)
	if input.Range != "" {
		if input.SheetID == "" {
			return ReplaceWorkbookInput{}, fmt.Errorf("%w: range requires sheet_id", ErrInvalid)
		}
		if _, err := cellrange.Parse(input.Range); err != nil {
			return ReplaceWorkbookInput{}, fmt.Errorf("%w: range is not a valid A1 reference", ErrInvalid)
		}
	}
	if !input.Preview && strings.TrimSpace(input.IdempotencyKey) == "" {
		return ReplaceWorkbookInput{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	return input, nil
}

// planReplacements walks every search page and turns matches into concrete cell
// inputs. Formula cells rewrite the formula so the server recalculates the
// value; array-formula results are reported as skipped instead of edited.
func planReplacements(ctx context.Context, repository ReplaceRepository, workbookID string, input ReplaceWorkbookInput, matcher searchMatcher) ([]plannedReplacement, int, int, int64, error) {
	search := input.searchInput()
	planned := make([]plannedReplacement, 0, 64)
	matched, skipped := 0, 0
	var version int64
	var scope *cellrange.Range
	if input.Range != "" {
		parsed, err := cellrange.Parse(input.Range)
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("%w: range is not a valid A1 reference", ErrInvalid)
		}
		scope = &parsed
	}
	for offset := 0; ; {
		search.Offset = offset
		page, err := repository.SearchWorkbook(ctx, workbookID, search)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		version = page.WorkbookVersion
		for _, match := range page.Items {
			if scope != nil && !scope.Contains(match.Row, match.Column) {
				continue
			}
			matched++
			if match.SpillSource != "" {
				skipped++
				continue
			}
			field, before := "value", searchValueText(match.Value)
			if match.Formula != "" {
				if input.SkipFormulas {
					skipped++
					continue
				}
				field, before = "formula", match.Formula
			}
			after, changed := matcher.replaceText(before, input.Replacement)
			if !changed {
				skipped++
				continue
			}
			cell := CellInput{Row: match.Row, Column: match.Column, Style: cloneJSON(match.Style)}
			if field == "formula" {
				if !strings.HasPrefix(strings.TrimSpace(after), "=") {
					skipped++
					continue
				}
				cell.Formula = after
			} else {
				value, err := replacedValue(match.Value, after)
				if err != nil {
					return nil, 0, 0, 0, fmt.Errorf("%w: replacement produces an unsupported value", ErrInvalid)
				}
				cell.Value = value
			}
			planned = append(planned, plannedReplacement{match: match, field: field, after: after, input: cell})
			if len(planned) > MaxReplaceCells {
				return nil, 0, 0, 0, fmt.Errorf("%w: replacement affects more than %d cells, narrow the search first", ErrInvalid, MaxReplaceCells)
			}
		}
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	return planned, matched, skipped, version, nil
}

// ReplaceWorkbookCells previews or applies a find-and-replace over a workbook.
// Each sheet is changed by one atomic, undoable cell mutation so the existing
// conflict, recalculation and audit contracts stay intact.
func ReplaceWorkbookCells(ctx context.Context, repository ReplaceRepository, workbookID string, input ReplaceWorkbookInput) (ReplaceWorkbookResult, error) {
	normalized, err := normalizeReplaceInput(input)
	if err != nil {
		return ReplaceWorkbookResult{}, err
	}
	matcher, err := newSearchMatcher(normalized.searchInput())
	if err != nil {
		return ReplaceWorkbookResult{}, err
	}
	planned, matched, skipped, version, err := planReplacements(ctx, repository, workbookID, normalized, matcher)
	if err != nil {
		return ReplaceWorkbookResult{}, err
	}
	result := ReplaceWorkbookResult{
		WorkbookID: workbookID, WorkbookVersion: version, Query: normalized.Query, Replacement: normalized.Replacement,
		Preview: normalized.Preview, MatchedCells: matched, PlannedCells: len(planned), SkippedCells: skipped,
		Items: make([]ReplacePreviewItem, 0, min(len(planned), MaxReplacePreviewRows)), Sheets: make([]ReplaceSheetOperation, 0, 4),
		ServerVersion: version,
	}
	for _, item := range planned {
		if len(result.Items) >= MaxReplacePreviewRows {
			break
		}
		before := searchValueText(item.match.Value)
		if item.field == "formula" {
			before = item.match.Formula
		}
		result.Items = append(result.Items, ReplacePreviewItem{
			SheetID: item.match.SheetID, SheetName: item.match.SheetName, Address: item.match.Address,
			Row: item.match.Row, Column: item.match.Column, Field: item.field, Before: before, After: item.after,
		})
	}
	if normalized.Preview || len(planned) == 0 {
		return result, nil
	}
	order := make([]string, 0, 4)
	bySheet := make(map[string][]plannedReplacement, 4)
	names := make(map[string]string, 4)
	for _, item := range planned {
		if _, seen := bySheet[item.match.SheetID]; !seen {
			order = append(order, item.match.SheetID)
			names[item.match.SheetID] = item.match.SheetName
		}
		bySheet[item.match.SheetID] = append(bySheet[item.match.SheetID], item)
	}
	baseVersion := version
	for _, sheetID := range order {
		items := bySheet[sheetID]
		cells := make([]CellInput, 0, len(items))
		for _, item := range items {
			cells = append(cells, item.input)
		}
		operation, err := repository.ApplyCells(ctx, CellMutation{
			SheetID: sheetID, ActorID: normalized.ActorID, ClientID: normalized.ClientID, BaseVersion: baseVersion,
			IdempotencyKey: normalized.IdempotencyKey + ":" + sheetID, Cells: cells, OperationType: "cells.replace",
		})
		if err != nil {
			return ReplaceWorkbookResult{}, err
		}
		baseVersion = operation.ServerVersion
		result.ServerVersion = operation.ServerVersion
		result.ReplacedCells += operation.AppliedCells
		result.Sheets = append(result.Sheets, ReplaceSheetOperation{
			SheetID: sheetID, SheetName: names[sheetID], ReplacedCells: operation.AppliedCells, Operation: operation, Cells: cells,
		})
	}
	return result, nil
}
