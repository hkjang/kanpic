package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"kanpic/pkg/cellrange"
)

// AgentContext is the bounded, structured workbook view sent to an AI model.
// Cell contents remain data inside SelectedRange; they are never promoted to
// instructions. Workbook-wide context contains metadata and semantic profiles
// only, which keeps large workbooks useful without exporting every cell.
type AgentContext struct {
	WorkbookID       string           `json:"workbook_id"`
	WorkbookTitle    string           `json:"workbook_title"`
	WorkbookVersion  int64            `json:"workbook_version"`
	ActiveSheet      AgentSheet       `json:"active_sheet"`
	Selection        string           `json:"selection"`
	SelectedRange    AgentRange       `json:"selected_range"`
	Sheets           []AgentSheet     `json:"sheets"`
	SemanticMap      []SemanticColumn `json:"semantic_map"`
	SuggestedPrompts []string         `json:"suggested_prompts"`
}

type AgentSheet struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Position      int    `json:"position"`
	Hidden        bool   `json:"hidden"`
	UsedRange     string `json:"used_range"`
	RowCount      int    `json:"row_count"`
	ColumnCount   int    `json:"column_count"`
	NonEmptyCells int    `json:"non_empty_cells"`
	FormulaCells  int    `json:"formula_cells"`
}

type AgentRange struct {
	Address      string  `json:"address"`
	CellCount    int     `json:"cell_count"`
	NonEmpty     int     `json:"non_empty"`
	FormulaCount int     `json:"formula_count"`
	BlankCount   int     `json:"blank_count"`
	FormulaRatio float64 `json:"formula_ratio"`
}

type SemanticColumn struct {
	Column       int      `json:"column"`
	Address      string   `json:"address"`
	Header       string   `json:"header,omitempty"`
	DataType     string   `json:"data_type"`
	SemanticType string   `json:"semantic_type,omitempty"`
	NonEmpty     int      `json:"non_empty"`
	NullCount    int      `json:"null_count"`
	UniqueCount  int      `json:"unique_count"`
	FormulaCount int      `json:"formula_count"`
	Examples     []string `json:"examples,omitempty"`
}

type agentContextRepository interface {
	GetWorkbook(context.Context, string) (Workbook, error)
	SheetStats(context.Context, string) ([]SheetStats, error)
	ReadRange(context.Context, string, cellrange.Range) ([]Cell, error)
}

// BuildAgentContext prioritizes the current selection while still describing
// every sheet. The caller has already checked workbook ACLs.
func BuildAgentContext(ctx context.Context, repository agentContextRepository, workbookID, sheetID, selection string) (AgentContext, error) {
	book, err := repository.GetWorkbook(ctx, strings.TrimSpace(workbookID))
	if err != nil {
		return AgentContext{}, err
	}
	selected, err := cellrange.Parse(strings.TrimSpace(selection))
	if err != nil {
		return AgentContext{}, fmt.Errorf("%w: selection is invalid", ErrInvalid)
	}
	var active Sheet
	found := false
	for _, sheet := range book.Sheets {
		if sheet.ID == strings.TrimSpace(sheetID) {
			active, found = sheet, true
			break
		}
	}
	if !found {
		return AgentContext{}, ErrNotFound
	}
	stats, err := repository.SheetStats(ctx, book.ID)
	if err != nil {
		return AgentContext{}, err
	}
	statByID := make(map[string]SheetStats, len(stats))
	for _, item := range stats {
		statByID[item.SheetID] = item
	}
	sheets := make([]AgentSheet, 0, len(book.Sheets))
	for _, sheet := range book.Sheets {
		stat := statByID[sheet.ID]
		used := "A1"
		if stat.MaxRow > 0 && stat.MaxColumn > 0 {
			used = "A1:" + cellrange.Address(stat.MaxRow, stat.MaxColumn)
		}
		sheets = append(sheets, AgentSheet{ID: sheet.ID, Name: sheet.Name, Position: sheet.Position, Hidden: sheet.Hidden, UsedRange: used, RowCount: stat.MaxRow, ColumnCount: stat.MaxColumn, NonEmptyCells: stat.NonEmptyCells, FormulaCells: stat.FormulaCells})
	}
	sort.Slice(sheets, func(i, j int) bool { return sheets[i].Position < sheets[j].Position })
	cells, err := repository.ReadRange(ctx, active.ID, selected)
	if err != nil {
		return AgentContext{}, err
	}
	rows := selected.End.Row - selected.Start.Row + 1
	columns := selected.End.Column - selected.Start.Column + 1
	cellCount := rows * columns
	formulaCount := 0
	for _, cell := range cells {
		if strings.TrimSpace(cell.Formula) != "" {
			formulaCount++
		}
	}
	activeContext := AgentSheet{ID: active.ID, Name: active.Name, Position: active.Position, Hidden: active.Hidden}
	for _, item := range sheets {
		if item.ID == active.ID {
			activeContext = item
			break
		}
	}
	semantic := semanticColumns(selected, cells)
	return AgentContext{
		WorkbookID: book.ID, WorkbookTitle: book.Title, WorkbookVersion: book.Version,
		ActiveSheet: activeContext, Selection: normalizedRange(selected), Sheets: sheets,
		SelectedRange: AgentRange{Address: normalizedRange(selected), CellCount: cellCount, NonEmpty: len(cells), FormulaCount: formulaCount, BlankCount: cellCount - len(cells), FormulaRatio: ratio(formulaCount, cellCount)},
		SemanticMap:   semantic, SuggestedPrompts: contextSuggestions(semantic, formulaCount),
	}, nil
}

func semanticColumns(selected cellrange.Range, cells []Cell) []SemanticColumn {
	byCoordinate := make(map[string]Cell, len(cells))
	for _, cell := range cells {
		byCoordinate[fmt.Sprintf("%d:%d", cell.Row, cell.Column)] = cell
	}
	items := make([]SemanticColumn, 0, selected.End.Column-selected.Start.Column+1)
	for column := selected.Start.Column; column <= selected.End.Column; column++ {
		item := SemanticColumn{Column: column, Address: cellrange.Address(1, column), DataType: "empty"}
		types := map[string]int{}
		unique := map[string]struct{}{}
		for row := selected.Start.Row; row <= selected.End.Row; row++ {
			cell, ok := byCoordinate[fmt.Sprintf("%d:%d", row, column)]
			if !ok {
				item.NullCount++
				continue
			}
			text, dataType := agentCellValue(cell)
			if row == selected.Start.Row && text != "" {
				item.Header = text
			}
			item.NonEmpty++
			types[dataType]++
			unique[dataType+":"+text] = struct{}{}
			if cell.Formula != "" {
				item.FormulaCount++
			}
			if len(item.Examples) < 3 && text != "" && !(row == selected.Start.Row && item.Header == text) {
				item.Examples = append(item.Examples, text)
			}
		}
		item.UniqueCount = len(unique)
		item.DataType = dominantType(types)
		item.SemanticType = semanticType(item.Header)
		items = append(items, item)
	}
	return items
}

func agentCellValue(cell Cell) (string, string) {
	if cell.Formula != "" {
		return cell.Formula, "formula"
	}
	if len(cell.Value) == 0 {
		return "", "empty"
	}
	var value any
	if json.Unmarshal(cell.Value, &value) != nil {
		return string(cell.Value), "text"
	}
	switch typed := value.(type) {
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), "number"
	case bool:
		return strconv.FormatBool(typed), "boolean"
	case string:
		if _, err := time.Parse("2006-01-02", typed); err == nil {
			return typed, "date"
		}
		return typed, "text"
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded), "text"
	}
}

func dominantType(types map[string]int) string {
	best, count := "empty", 0
	for _, candidate := range []string{"formula", "number", "date", "boolean", "text"} {
		if types[candidate] > count {
			best, count = candidate, types[candidate]
		}
	}
	return best
}

func semanticType(header string) string {
	value := strings.ToLower(strings.TrimSpace(header))
	patterns := []struct {
		semantic string
		words    []string
	}{
		{"date", []string{"일자", "날짜", "월", "date", "day"}},
		{"customer_id", []string{"고객id", "고객 id", "customer_id", "customer id"}},
		{"region", []string{"지역", "권역", "region"}},
		{"quantity", []string{"수량", "quantity", "qty"}},
		{"unit_price", []string{"단가", "unit price"}},
		{"revenue", []string{"매출", "금액", "revenue", "sales", "amount"}},
		{"category", []string{"분류", "구분", "카테고리", "category"}},
	}
	for _, pattern := range patterns {
		for _, word := range pattern.words {
			if strings.Contains(value, word) {
				return pattern.semantic
			}
		}
	}
	return ""
}

func contextSuggestions(columns []SemanticColumn, formulaCount int) []string {
	items := []string{"이 선택 범위를 분석해줘", "이 데이터를 보기 좋게 정리해줘"}
	if formulaCount > 0 {
		items = append(items, "잘못된 수식을 찾아줘")
	} else {
		items = append(items, "필요한 계산 열을 수식으로 채워줘")
	}
	for _, column := range columns {
		if column.SemanticType == "date" {
			items = append(items, "시간 흐름을 보여주는 차트를 만들어줘")
			break
		}
	}
	return items
}

func normalizedRange(selected cellrange.Range) string {
	start, end := cellrange.Address(selected.Start.Row, selected.Start.Column), cellrange.Address(selected.End.Row, selected.End.Column)
	if start == end {
		return start
	}
	return start + ":" + end
}

func ratio(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}
