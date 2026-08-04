package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Templates are ready-made workbooks: header rows, sample data, working
// formulas, number formats, column widths and a frozen header. They are defined
// on the server so REST clients, MCP agents and the web app all create the same
// thing.

type TemplateCell struct {
	Row     int            `json:"row"`
	Column  int            `json:"column"`
	Value   any            `json:"value,omitempty"`
	Formula string         `json:"formula,omitempty"`
	Style   map[string]any `json:"style,omitempty"`
}

type TemplateSheet struct {
	Name       string         `json:"name"`
	Color      string         `json:"color,omitempty"`
	FrozenRows int            `json:"frozen_rows"`
	Widths     []float64      `json:"widths,omitempty"`
	Cells      []TemplateCell `json:"cells"`
}

type Template struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Category string          `json:"category"`
	Summary  string          `json:"summary"`
	Columns  []string        `json:"columns"`
	Sheets   []TemplateSheet `json:"sheets"`
}

// TemplateSummary is the catalog entry the gallery lists. It leaves the cells
// out so browsing the catalog stays cheap.
type TemplateSummary struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Summary  string   `json:"summary"`
	Columns  []string `json:"columns"`
	Sheets   []string `json:"sheets"`
}

func (t Template) summary() TemplateSummary {
	names := make([]string, 0, len(t.Sheets))
	for _, sheet := range t.Sheets {
		names = append(names, sheet.Name)
	}
	return TemplateSummary{ID: t.ID, Name: t.Name, Category: t.Category, Summary: t.Summary, Columns: t.Columns, Sheets: names}
}

// TemplateCatalog lists every template in gallery order.
func TemplateCatalog() []TemplateSummary {
	items := make([]TemplateSummary, 0, len(templates))
	for _, template := range templates {
		items = append(items, template.summary())
	}
	return items
}

// TemplateByID finds one template, reporting whether it exists.
func TemplateByID(id string) (Template, bool) {
	for _, template := range templates {
		if strings.EqualFold(template.ID, strings.TrimSpace(id)) {
			return template, true
		}
	}
	return Template{}, false
}

var (
	titleStyle  = map[string]any{"bold": true, "font_size": float64(15)}
	noteStyle   = map[string]any{"color": "#5f6f79", "font_size": float64(10)}
	headerStyle = map[string]any{"bold": true, "background": "#0f766e", "color": "#ffffff", "horizontal_align": "center"}
	totalStyle  = map[string]any{"bold": true, "background": "#eef4f4"}
)

const (
	formatMoney   = "₩#,##0"
	formatNumber  = "#,##0"
	formatDecimal = "#,##0.00"
	formatPercent = "0.0%"
	formatDate    = "yyyy-mm-dd"
)

// sheetSpec builds one template sheet. Rows are numbered as they are added, and
// formulas use {r} for the current row, {p} for the row above and {first} and
// {last} for the data
// block, so inserting a row above never breaks a template definition.
type sheetSpec struct {
	name        string
	color       string
	widths      []float64
	formats     map[int]string
	titleText   string
	noteText    string
	headers     []string
	dataRows    [][]any
	totalRow    []any
	summaryRows [][]any
}

func sheet(name string) *sheetSpec { return &sheetSpec{name: name, formats: map[int]string{}} }

func (s *sheetSpec) tab(value string) *sheetSpec { s.color = value; return s }

func (s *sheetSpec) cols(values ...float64) *sheetSpec { s.widths = values; return s }

func (s *sheetSpec) title(text string) *sheetSpec { s.titleText = text; return s }

func (s *sheetSpec) note(text string) *sheetSpec { s.noteText = text; return s }

func (s *sheetSpec) head(labels ...string) *sheetSpec { s.headers = labels; return s }

func (s *sheetSpec) rows(values ...[]any) *sheetSpec {
	s.dataRows = append(s.dataRows, values...)
	return s
}

func (s *sheetSpec) total(values ...any) *sheetSpec { s.totalRow = values; return s }

// summary appends a label and value block under the table, which is where a
// spreadsheet usually keeps its counts and ratios.
func (s *sheetSpec) summary(pairs ...[]any) *sheetSpec {
	s.summaryRows = append(s.summaryRows, pairs...)
	return s
}

// format assigns a number format to one-based column numbers.
func (s *sheetSpec) format(numberFormat string, columns ...int) *sheetSpec {
	for _, column := range columns {
		s.formats[column] = numberFormat
	}
	return s
}

func row(values ...any) []any { return values }

// formatted pins a number format to one value, which is how summary rows keep
// their currency and percentage formatting outside the table columns.
type formatted struct {
	value        any
	numberFormat string
}

func won(value any) any { return formatted{value, formatMoney} }
func pct(value any) any { return formatted{value, formatPercent} }
func num(value any) any { return formatted{value, formatNumber} }
func dec(value any) any { return formatted{value, formatDecimal} }

func unwrap(value any, columnFormat string) (any, string) {
	if wrapped, ok := value.(formatted); ok {
		return wrapped.value, wrapped.numberFormat
	}
	return value, columnFormat
}

func (s *sheetSpec) build() TemplateSheet {
	cells := make([]TemplateCell, 0, len(s.dataRows)*len(s.headers)+8)
	line := 1
	if s.titleText != "" {
		cells = append(cells, TemplateCell{Row: line, Column: 1, Value: s.titleText, Style: titleStyle})
		line++
	}
	if s.noteText != "" {
		cells = append(cells, TemplateCell{Row: line, Column: 1, Value: s.noteText, Style: noteStyle})
		line++
	}
	headerRow := line
	for index, label := range s.headers {
		cells = append(cells, TemplateCell{Row: headerRow, Column: index + 1, Value: label, Style: headerStyle})
	}
	firstData := headerRow + 1
	lastData := firstData + len(s.dataRows) - 1
	for offset, values := range s.dataRows {
		cells = append(cells, s.cellsFor(firstData+offset, firstData, lastData, values, nil)...)
	}
	next := lastData + 1
	if len(s.totalRow) > 0 {
		cells = append(cells, s.cellsFor(next, firstData, lastData, s.totalRow, totalStyle)...)
		next++
	}
	for offset, pair := range s.summaryRows {
		line := next + 1 + offset
		cells = append(cells, TemplateCell{Row: line, Column: 1, Value: pair[0], Style: map[string]any{"bold": true}})
		for index, value := range pair[1:] {
			cells = append(cells, s.summaryCell(line, index+2, firstData, lastData, value))
		}
	}
	return TemplateSheet{Name: s.name, Color: s.color, FrozenRows: headerRow, Widths: s.widths, Cells: cells}
}

func (s *sheetSpec) summaryCell(line, column, first, last int, raw any) TemplateCell {
	value, numberFormat := unwrap(raw, s.formats[column])
	cell := TemplateCell{Row: line, Column: column, Style: mergeStyle(numberFormat, nil)}
	if text, ok := value.(string); ok && strings.HasPrefix(text, "=") {
		cell.Formula = expandFormula(text, line, first, last)
		return cell
	}
	cell.Value = value
	return cell
}

func (s *sheetSpec) cellsFor(line, first, last int, values []any, extra map[string]any) []TemplateCell {
	cells := make([]TemplateCell, 0, len(values))
	for index, raw := range values {
		column := index + 1
		if raw == nil {
			continue
		}
		value, numberFormat := unwrap(raw, s.formats[column])
		cell := TemplateCell{Row: line, Column: column}
		if text, ok := value.(string); ok && strings.HasPrefix(text, "=") {
			cell.Formula = expandFormula(text, line, first, last)
		} else {
			cell.Value = value
		}
		cell.Style = mergeStyle(numberFormat, extra)
		cells = append(cells, cell)
	}
	return cells
}

func mergeStyle(numberFormat string, extra map[string]any) map[string]any {
	if numberFormat == "" && extra == nil {
		return nil
	}
	style := map[string]any{}
	for key, value := range extra {
		style[key] = value
	}
	if numberFormat != "" {
		style["number_format"] = numberFormat
	}
	return style
}

func expandFormula(formula string, line, first, last int) string {
	replacer := strings.NewReplacer(
		"{r}", strconv.Itoa(line),
		"{p}", strconv.Itoa(line-1),
		"{first}", strconv.Itoa(first),
		"{last}", strconv.Itoa(last),
	)
	return replacer.Replace(formula)
}

func tmpl(id, name, category, summary string, sheets ...*sheetSpec) Template {
	built := make([]TemplateSheet, 0, len(sheets))
	for _, spec := range sheets {
		built = append(built, spec.build())
	}
	template := Template{ID: id, Name: name, Category: category, Summary: summary, Sheets: built}
	if len(sheets) > 0 {
		template.Columns = append([]string(nil), sheets[0].headers...)
	}
	return template
}

// ApplyTemplate fills a freshly created workbook with the template content. It
// works through the repository interface so both storage backends behave the
// same way.
func ApplyTemplate(ctx context.Context, repository Repository, book Workbook, template Template, actorID string) error {
	version := book.Version
	for index, spec := range template.Sheets {
		target, err := templateSheet(ctx, repository, book, index, spec)
		if err != nil {
			return err
		}
		cells := make([]CellInput, 0, len(spec.Cells))
		for _, cell := range spec.Cells {
			input := CellInput{Row: cell.Row, Column: cell.Column, Formula: cell.Formula}
			if cell.Formula == "" && cell.Value != nil {
				encoded, err := json.Marshal(cell.Value)
				if err != nil {
					return fmt.Errorf("%w: template cell value", ErrInvalid)
				}
				input.Value = encoded
			}
			if len(cell.Style) > 0 {
				encoded, err := json.Marshal(cell.Style)
				if err != nil {
					return fmt.Errorf("%w: template cell style", ErrInvalid)
				}
				input.Style = encoded
			}
			cells = append(cells, input)
		}
		if len(cells) > 0 {
			result, err := repository.ApplyCells(ctx, CellMutation{
				SheetID: target.ID, ActorID: actorID, BaseVersion: version,
				IdempotencyKey: fmt.Sprintf("template:%s:%s:%d", book.ID, template.ID, index),
				Cells:          cells,
			})
			if err != nil {
				return err
			}
			version = result.ServerVersion
		}
		if err := applyTemplateLayout(ctx, repository, target, spec, actorID); err != nil {
			return err
		}
	}
	return nil
}

func templateSheet(ctx context.Context, repository Repository, book Workbook, index int, spec TemplateSheet) (Sheet, error) {
	if index > 0 {
		return repository.CreateSheet(ctx, book.ID, CreateSheetInput{Name: spec.Name, Color: spec.Color})
	}
	if len(book.Sheets) == 0 {
		return Sheet{}, fmt.Errorf("%w: workbook has no sheet to fill", ErrInvalid)
	}
	name := spec.Name
	input := UpdateSheetInput{Name: &name}
	if spec.Color != "" {
		color := spec.Color
		input.Color = &color
	}
	return repository.UpdateSheet(ctx, book.Sheets[0].ID, input)
}

// Column widths are applied as spans so a table of equally sized columns costs
// one mutation instead of one per column.
func applyTemplateLayout(ctx context.Context, repository Repository, target Sheet, spec TemplateSheet, actorID string) error {
	revision := target.Layout.Revision
	if revision < 1 {
		revision = 1
	}
	apply := func(mutation SheetLayoutMutation) error {
		mutation.SheetID = target.ID
		mutation.ActorID = actorID
		mutation.ExpectedRevision = revision
		mutation.IdempotencyKey = fmt.Sprintf("template-layout:%s:%s:%d", target.ID, mutation.Action, revision)
		result, err := repository.ApplySheetLayout(ctx, mutation)
		if err != nil {
			return err
		}
		revision = result.Layout.Revision
		return nil
	}
	for start := 0; start < len(spec.Widths); {
		end := start
		for end+1 < len(spec.Widths) && spec.Widths[end+1] == spec.Widths[start] {
			end++
		}
		if spec.Widths[start] > 0 {
			if err := apply(SheetLayoutMutation{Action: "resize", Axis: "column", Start: start + 1, Count: end - start + 1, Size: spec.Widths[start]}); err != nil {
				return err
			}
		}
		start = end + 1
	}
	if spec.FrozenRows > 0 {
		if err := apply(SheetLayoutMutation{Action: "freeze", FrozenRows: spec.FrozenRows}); err != nil {
			return err
		}
	}
	return nil
}
