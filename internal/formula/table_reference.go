package formula

import (
	"strings"

	"kanpic/pkg/cellrange"
)

// tableReference 는 매출표[금액] 을 실제 칸들로 바꾼다. 이름 범위와 같은
// 자리를 지나지만, 가리키는 칸이 표의 어디인지는 지정자가 정한다.
//
//	매출표          자료 줄만 (엑셀도 그렇다)
//	매출표[금액]     그 열의 자료 줄만
//	매출표[#전체]    머리글까지 통째로
//	매출표[#머리글]  머리글 줄만
//
// 머리글을 뺀 자료 줄이 기본인 까닭은 =SUM(매출표[금액]) 이 사람이 바라는
// 것이기 때문이다. 머리글까지 더하면 글자 하나가 섞여 답이 달라진다.
func (p *parser) tableReference(text string) (node, bool, error) {
	name, specifier, bracketed := splitTableReference(text)
	if !bracketed {
		name, specifier = text, ""
	}
	table, found := p.scope.Tables[normalizeSheetName(name)]
	if !found {
		return nil, false, nil
	}
	selected, err := cellrange.Parse(table.Range)
	if err != nil {
		return nil, true, formulaError("#REF!", "table "+name+" has an invalid range")
	}
	firstRow, lastRow := selected.Start.Row, selected.End.Row
	firstColumn, lastColumn := selected.Start.Column, selected.End.Column
	dataFirstRow := firstRow
	if table.HeaderRow {
		dataFirstRow = firstRow + 1
	}
	// 합계 줄은 자료가 아니다. 빼지 않으면 그 줄의 =SUM(매출표[금액]) 이
	// 제 자신을 더해 순환이 된다.
	dataLastRow := lastRow
	if table.TotalsRow {
		dataLastRow = lastRow - 1
	}
	switch kind, known := tableSpecifier(specifier); {
	case specifier == "":
		firstRow, lastRow = dataFirstRow, dataLastRow
	case known && kind == "all":
	case known && kind == "headers":
		if !table.HeaderRow {
			return nil, true, formulaError("#REF!", "table "+name+" has no header row")
		}
		lastRow = firstRow
	case known && kind == "totals":
		if !table.TotalsRow {
			return nil, true, formulaError("#REF!", "table "+name+" has no totals row")
		}
		firstRow = lastRow
	case known && kind == "data":
		firstRow, lastRow = dataFirstRow, dataLastRow
	default:
		// 지정자가 아니면 열 이름이다.
		index := tableColumnIndex(table.Columns, specifier)
		if index < 0 {
			return nil, true, formulaError("#REF!", "table "+name+" has no column called "+specifier)
		}
		firstRow, lastRow = dataFirstRow, dataLastRow
		firstColumn, lastColumn = selected.Start.Column+index, selected.Start.Column+index
	}
	if firstRow > lastRow {
		return nil, true, formulaError("#REF!", "table "+name+" has no rows there")
	}
	// 머리글 줄에도 기댄다. 열 이름이 머리글에서 오므로, 금액 을 매출액 으로
	// 고치면 매출표[금액] 은 그 자리에서 #REF! 가 되어야 한다. 기대 두지
	// 않으면 다시 셈할 까닭이 없어, 없어진 열의 옛 답이 그대로 남는다.
	if table.HeaderRow {
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			p.dependencies[CellKey(table.SheetID, cellrange.Address(selected.Start.Row, column))] = struct{}{}
		}
	}
	result, buildErr := p.buildRangeAt(table.SheetID, firstRow, firstColumn, lastRow, lastColumn)
	return result, true, buildErr
}

func tableColumnIndex(columns []string, name string) int {
	target := normalizeSheetName(name)
	for index, column := range columns {
		if normalizeSheetName(column) == target {
			return index
		}
	}
	return -1
}

// TableDefinitions 는 워크북이 들고 있는 표를 엔진이 아는 꼴로 바꾼다.
func TableDefinitions(items map[string]Table) map[string]Table {
	if len(items) == 0 {
		return nil
	}
	definitions := make(map[string]Table, len(items))
	for name, item := range items {
		definitions[normalizeSheetName(name)] = item
	}
	return definitions
}

// IsTableReference 는 글자가 표를 가리키는 모양인지 본다. 이름 뒤에 대괄호가
// 붙어 있으면 그렇다.
func IsTableReference(text string) bool {
	_, _, bracketed := splitTableReference(strings.TrimSpace(text))
	return bracketed
}
