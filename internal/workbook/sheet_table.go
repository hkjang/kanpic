package workbook

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

// MaxTables 는 한 워크북에 담을 수 있는 표의 수다. 이름은 수식을 풀 때마다
// 훑으므로 한없이 늘리면 계산이 느려진다.
const MaxTables = 200

// SheetTable 은 이름을 가진 표다. 지금까지의 "테이블 서식" 은 색만 칠하는
// 것이어서 수식에서 그것을 가리킬 수 없었다.
//
//	=SUM(매출표[금액])
//
// 열을 하나 끼워 넣어도 이 수식은 그대로 맞다. 범위로 적은 =SUM(C2:C50) 은
// 사람이 옮겨 적어야 하고, 잊으면 조용히 틀린 값을 낸다.
type SheetTable struct {
	ID              string    `json:"id"`
	WorkbookID      string    `json:"workbook_id"`
	WorkbookVersion int64     `json:"workbook_version"`
	SheetID         string    `json:"sheet_id"`
	CreateKey       string    `json:"-"`
	Name            string    `json:"name"`
	Range           string    `json:"range"`
	HeaderRow       bool      `json:"header_row"`
	Theme           string    `json:"theme,omitempty"`
	Revision        int64     `json:"revision"`
	CreatedBy       string    `json:"created_by"`
	UpdatedBy       string    `json:"updated_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateSheetTableInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	SheetID        string `json:"sheet_id"`
	Name           string `json:"name"`
	Range          string `json:"range"`
	HeaderRow      *bool  `json:"header_row,omitempty"`
	Theme          string `json:"theme,omitempty"`
}

type UpdateSheetTableInput struct {
	Name             *string `json:"name,omitempty"`
	Range            *string `json:"range,omitempty"`
	HeaderRow        *bool   `json:"header_row,omitempty"`
	Theme            *string `json:"theme,omitempty"`
	ExpectedRevision *int64  `json:"expected_revision,omitempty"`
}

// normalizeSheetTable 은 담기 전에 표를 한 가지 모양으로 다듬는다.
func normalizeSheetTable(item SheetTable) (SheetTable, error) {
	name, err := normalizeTableName(item.Name)
	if err != nil {
		return SheetTable{}, err
	}
	item.Name = name
	if strings.TrimSpace(item.SheetID) == "" {
		return SheetTable{}, fmt.Errorf("%w: a table needs a sheet", ErrInvalid)
	}
	parsed, err := cellrange.Parse(strings.ReplaceAll(strings.TrimSpace(item.Range), "$", ""))
	if err != nil {
		return SheetTable{}, fmt.Errorf("%w: %s is not a range", ErrInvalid, item.Range)
	}
	// 머리글만 있고 자료가 없는 표는 열 이름은 있는데 가리킬 것이 없다.
	// 매출표[금액] 이 빈 것을 내는 것과, 표를 만들 수 없다고 말해 주는 것
	// 가운데 뒤가 낫다 — 사람은 자료를 아직 안 넣었을 뿐이기 때문이다.
	if item.HeaderRow && parsed.End.Row <= parsed.Start.Row {
		return SheetTable{}, fmt.Errorf("%w: a table with a header row needs at least one row of data", ErrInvalid)
	}
	item.Range = cellrange.Address(parsed.Start.Row, parsed.Start.Column) + ":" + cellrange.Address(parsed.End.Row, parsed.End.Column)
	item.Theme = strings.TrimSpace(item.Theme)
	return item, nil
}

// normalizeTableName 은 표 이름의 규칙이다. 이름 범위와 같은 규칙을 쓴다 —
// 수식 안에서 같은 자리에 오는 이름이므로 규칙이 다르면 사람이 헷갈린다.
func normalizeTableName(value string) (string, error) {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) == 0 || len(runes) > 255 || (!unicode.IsLetter(runes[0]) && runes[0] != '_') {
		return "", fmt.Errorf("%w: a table name must start with a letter or underscore", ErrInvalid)
	}
	for _, character := range runes[1:] {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' && character != '.' {
			return "", fmt.Errorf("%w: a table name may contain only letters, numbers, underscores, and periods", ErrInvalid)
		}
	}
	upper := strings.ToUpper(value)
	if upper == "TRUE" || upper == "FALSE" || looksLikeCellReference(upper) {
		return "", fmt.Errorf("%w: %s is a cell reference, not a name", ErrInvalid, value)
	}
	return value, nil
}

// TableColumns 는 표의 열 이름을 머리글 줄에서 읽는다. 머리글이 없거나 빈
// 칸이면 열1, 열2 로 센다 — 이름 없는 열도 가리킬 수 있어야 한다.
func TableColumns(item SheetTable, header []string) []string {
	parsed, err := cellrange.Parse(item.Range)
	if err != nil {
		return nil
	}
	width := parsed.End.Column - parsed.Start.Column + 1
	columns := make([]string, width)
	taken := make(map[string]struct{}, width)
	for index := 0; index < width; index++ {
		name := ""
		if item.HeaderRow && index < len(header) {
			name = strings.TrimSpace(header[index])
		}
		if name == "" {
			name = fmt.Sprintf("열%d", index+1)
		}
		// 머리글에 같은 글자가 둘이면 뒤엣것에 번호를 붙인다. 그대로 두면
		// 매출표[금액] 이 어느 열인지 정해지지 않는다.
		key := strings.ToUpper(name)
		if _, duplicate := taken[key]; duplicate {
			for suffix := 2; ; suffix++ {
				candidate := fmt.Sprintf("%s%d", name, suffix)
				if _, used := taken[strings.ToUpper(candidate)]; !used {
					name = candidate
					break
				}
			}
		}
		taken[strings.ToUpper(name)] = struct{}{}
		columns[index] = name
	}
	return columns
}

// transformSheetTableForStructure 는 행과 열이 움직일 때 표를 따라 옮긴다.
// 조건부 서식이나 보호 범위와 같은 자리다. 표가 제자리에 남으면 매출표[금액]
// 이 엉뚱한 열을 가리키는데, 수식은 멀쩡해 보이므로 아무도 알아채지 못한다.
func transformSheetTableForStructure(item SheetTable, input StructuralMutation, actor string, now time.Time) (SheetTable, bool, error) {
	transformed, exists, err := transformRangeAddress(item.Range, input)
	if err != nil {
		return SheetTable{}, false, fmt.Errorf("%w: table range exceeds spreadsheet bounds", ErrInvalid)
	}
	// 표가 통째로 지워졌으면 가리킬 것이 없다.
	if !exists {
		return SheetTable{}, false, nil
	}
	if transformed == item.Range {
		return item, true, nil
	}
	item.Range, item.Revision, item.UpdatedBy, item.UpdatedAt = transformed, item.Revision+1, actor, now
	// 머리글만 남을 만큼 줄었으면 표로 둘 수 없다.
	normalized, err := normalizeSheetTable(item)
	return normalized, err == nil, nil
}

// formulaTables 는 표를 엔진이 아는 꼴로 바꾼다. 열 이름은 여기서 채우지
// 않는다 — 머리글 칸을 읽어야 알 수 있고, 그것은 재계산이 고친 뒤의 칸을
// 들고 있는 자리에서 한다. tablesWithColumns 를 보라.
func formulaTables(items []SheetTable) map[string]formula.Table {
	if len(items) == 0 {
		return nil
	}
	result := make(map[string]formula.Table, len(items))
	for _, item := range items {
		result[item.Name] = formula.Table{SheetID: item.SheetID, Range: item.Range, HeaderRow: item.HeaderRow}
	}
	return result
}

// checkTableConflicts 는 이름이 겹치는지, 표끼리 서로 걸치는지 본다.
//
// 이름이 둘이면 매출표[금액] 이 어느 것을 가리키는지 사람도 기계도 모른다.
// 서로 걸치면 한 칸이 두 표에 들어가, 한쪽에서 행을 지우면 다른 쪽이 조용히
// 어그러진다. 만들 때 막는 편이 그때 가서 고치는 것보다 싸다.
func checkTableConflicts(existing []SheetTable, item SheetTable, ignoreID string) error {
	selected, err := cellrange.Parse(item.Range)
	if err != nil {
		return fmt.Errorf("%w: %s is not a range", ErrInvalid, item.Range)
	}
	for _, other := range existing {
		if other.ID == ignoreID {
			continue
		}
		if strings.EqualFold(other.Name, item.Name) {
			return fmt.Errorf("%w: a table called %s already exists", ErrDuplicateName, item.Name)
		}
		if other.SheetID != item.SheetID {
			continue
		}
		against, parseErr := cellrange.Parse(other.Range)
		if parseErr != nil {
			continue
		}
		if selected.Start.Row <= against.End.Row && against.Start.Row <= selected.End.Row &&
			selected.Start.Column <= against.End.Column && against.Start.Column <= selected.End.Column {
			return fmt.Errorf("%w: %s already covers those cells", ErrInvalid, other.Name)
		}
	}
	return nil
}

// sheetTablesFromMap 은 구조 변경이 만들어 둔 다음 상태의 표를 목록으로
// 바꾼다. 다시 셈할 때는 옮겨진 뒤의 자리를 봐야 한다.
func sheetTablesFromMap(items map[string]SheetTable, workbookID string) []SheetTable {
	result := make([]SheetTable, 0, len(items))
	for _, item := range items {
		if item.WorkbookID == workbookID {
			result = append(result, item)
		}
	}
	return result
}

// buildImportedSheetTables 는 파일이 들고 온 표를 워크북에 담을 꼴로 바꾼다.
// 이름 범위와 같이 첫 계산 전에 있어야 =SUM(매출표[금액]) 이 #NAME? 으로
// 굳지 않는다.
func buildImportedSheetTables(workbookID, actor string, sheets []ImportSheet, sheetIDsByName map[string]string, now time.Time) []SheetTable {
	items := make([]SheetTable, 0)
	for _, sheet := range sheets {
		sheetID, known := sheetIDsByName[strings.TrimSpace(sheet.Name)]
		if !known {
			continue
		}
		for index, source := range sheet.Tables {
			if len(items) >= MaxTables {
				return items
			}
			item, err := normalizeSheetTable(SheetTable{
				WorkbookID: workbookID, SheetID: sheetID,
				CreateKey: fmt.Sprintf("import:%s:%d", sheet.Name, index),
				Name:      source.Name, Range: source.Range, HeaderRow: source.HeaderRow,
				CreatedBy: actor, UpdatedBy: actor,
			})
			if err != nil {
				continue
			}
			// 파일이 겹치는 이름이나 걸치는 표를 들고 올 수 있다. 하나가
			// 잘못되었다고 파일을 못 여는 것은 사람에게 도움이 되지 않는다.
			if err := checkTableConflicts(items, item, ""); err != nil {
				continue
			}
			item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
			items = append(items, item)
		}
	}
	return items
}

// expandTablesForCells 는 표 바로 아래 줄에 값을 넣으면 표가 그 줄을 삼키게
// 한다. 구글 시트와 엑셀이 그렇게 하고, 그래야 표가 살아 있는 것이 된다.
//
// 표를 만든 뒤 자료를 한 줄 더 붙이는 것은 가장 흔한 일이다. 그때마다 사람이
// 표 범위를 손으로 늘려야 한다면, =SUM(매출표[금액]) 은 새로 넣은 줄을 빼고
// 셈한다 — 답이 나오기는 하는데 틀린 답이다.
//
// 옆으로는 늘리지 않는다. 열을 하나 더 쓰는 것은 표의 생김새를 바꾸는 일이라
// 사람이 뜻을 가지고 하는 편이 낫고, 옆 칸에 적은 메모까지 표로 삼키면
// 열 이름이 제멋대로 늘어난다.
func expandTablesForCells(tables []SheetTable, sheetID string, cells []CellInput, actor string, now time.Time) []SheetTable {
	if len(tables) == 0 || len(cells) == 0 {
		return nil
	}
	changed := make([]SheetTable, 0)
	for _, item := range tables {
		if item.SheetID != sheetID {
			continue
		}
		selected, err := cellrange.Parse(item.Range)
		if err != nil {
			continue
		}
		lastRow := selected.End.Row
		for {
			grown := false
			for _, cell := range cells {
				if cell.SheetID != "" && cell.SheetID != sheetID {
					continue
				}
				if cell.Row != lastRow+1 || cell.Column < selected.Start.Column || cell.Column > selected.End.Column {
					continue
				}
				// 지우는 것은 늘리는 까닭이 되지 않는다. 빈 칸을 삼키면
				// 아무것도 적지 않은 줄까지 표가 된다.
				if len(cell.Value) == 0 && strings.TrimSpace(cell.Formula) == "" {
					continue
				}
				lastRow++
				grown = true
				break
			}
			if !grown {
				break
			}
		}
		if lastRow == selected.End.Row || lastRow > maxSpreadsheetRows {
			continue
		}
		widened := item
		widened.Range = cellrange.Address(selected.Start.Row, selected.Start.Column) + ":" + cellrange.Address(lastRow, selected.End.Column)
		// 늘리다 남의 표를 밟으면 늘리지 않는다. 걸친 표는 한쪽에서 행을
		// 지우면 다른 쪽이 조용히 어그러진다.
		if err := checkTableConflicts(tables, widened, item.ID); err != nil {
			continue
		}
		widened.Revision, widened.UpdatedBy, widened.UpdatedAt = item.Revision+1, actor, now
		changed = append(changed, widened)
	}
	return changed
}

// mergeSheetTables 는 늘어난 표를 원래 목록 위에 덮어쓴다.
func mergeSheetTables(base, changed []SheetTable) []SheetTable {
	if len(changed) == 0 {
		return base
	}
	byID := make(map[string]SheetTable, len(changed))
	for _, item := range changed {
		byID[item.ID] = item
	}
	merged := make([]SheetTable, 0, len(base))
	for _, item := range base {
		if replacement, found := byID[item.ID]; found {
			merged = append(merged, replacement)
			continue
		}
		merged = append(merged, item)
	}
	return merged
}
