package formula

import "strings"

// Scope resolves user-visible sheet names to stable sheet identifiers. The
// formula graph uses stable identifiers so renaming a sheet does not change
// the identity of its cells while formulas keep their familiar Sheet!A1 text.
type Scope struct {
	CurrentSheet string
	Sheets       map[string]string
	NamedRanges  map[string]NamedRange
	// Extent bounds whole-column references such as A:A to the rows a sheet
	// actually uses, keyed by stable sheet identifier. Without it a single
	// A:A would name a million cells.
	Extent map[string]SheetExtent
	// Anchor is the address of the cell being parsed, which is what ROW() and
	// COLUMN() report when they are called without an argument.
	Anchor string
	// Imports holds the cross-workbook blocks IMPORTRANGE asked for, fetched
	// and permission-checked by the workbook layer before evaluation starts.
	Imports map[string]ImportedRange
	// External holds the WEBSERVICE and IMPORTDATA responses, fetched under
	// the administrator's policy before evaluation starts.
	External map[string]ExternalResult
	// NamedFunctions 는 워크북에 저장해 둔, 이름으로 부르는 수식이다.
	NamedFunctions map[string]NamedFunction
	// Tables 는 이름을 가진 표다. 매출표[금액] 처럼 열 이름으로 가리키면
	// 열이 끼워지고 지워져도 수식이 그대로 맞다.
	Tables map[string]Table
}

// Table 은 수식이 가리킬 수 있는 표다. 열 이름은 만들 때 머리글에서 읽어
// 둔다 — 수식을 풀 때마다 머리글 칸을 다시 읽으면 그 칸이 바뀔 때마다
// 표 전체가 다시 계산되어야 한다.
type Table struct {
	SheetID   string
	Range     string
	HeaderRow bool
	// TotalsRow 는 마지막 줄이 합계 줄인지다. 열을 가리킬 때 그 줄을 빼야
	// 합계 칸의 =SUM(매출표[금액]) 이 제 자신을 더하지 않는다.
	TotalsRow bool
	Columns   []string
}

// tableSpecifier 는 대괄호 안에 올 수 있는 몫이다. 파일에서 들어온 수식은
// 영문으로 적혀 있으므로 둘 다 받는다.
func tableSpecifier(text string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(text)) {
	case "#전체", "#ALL":
		return "all", true
	case "#머리글", "#HEADERS":
		return "headers", true
	case "#자료", "#데이터", "#DATA":
		return "data", true
	case "#합계", "#TOTALS":
		return "totals", true
	}
	return "", false
}

// splitTableReference 는 매출표[금액] 을 표 이름과 지정자로 가른다.
func splitTableReference(text string) (string, string, bool) {
	open := strings.Index(text, "[")
	if open <= 0 || !strings.HasSuffix(text, "]") {
		return "", "", false
	}
	inner := strings.TrimSpace(text[open+1 : len(text)-1])
	// 엑셀은 지정자를 한 번 더 감싸기도 한다. 매출표[[금액]] 도 같은 뜻이다.
	for strings.HasPrefix(inner, "[") && strings.HasSuffix(inner, "]") {
		inner = strings.TrimSpace(inner[1 : len(inner)-1])
	}
	return strings.TrimSpace(text[:open]), inner, true
}

// SheetExtent is the largest row and column a sheet has content in.
type SheetExtent struct {
	Rows    int
	Columns int
}

// NamedRange points a workbook-level name at a stable sheet identifier and
// normalized A1 range. Keeping the sheet identifier out of formula text makes
// names resilient to sheet renames.
type NamedRange struct {
	SheetID string
	Range   string
}

func normalizeSheetName(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeCellAddress(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "$", ""))
}

// CellKey returns the canonical graph key for a cell. Unscoped evaluator keys
// remain ordinary A1 addresses for backward compatibility.
func CellKey(sheetID, address string) string {
	address = normalizeCellAddress(address)
	if strings.TrimSpace(sheetID) == "" {
		return address
	}
	return strings.ToUpper(strings.TrimSpace(sheetID)) + "!" + address
}

// SplitCellKey separates a canonical graph key into its optional sheet and A1
// address components.
func SplitCellKey(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	index := strings.LastIndexByte(value, '!')
	if index < 0 {
		address := normalizeCellAddress(value)
		return "", address, isReference(address)
	}
	sheet := strings.ToUpper(strings.TrimSpace(value[:index]))
	address := normalizeCellAddress(value[index+1:])
	return sheet, address, sheet != "" && isReference(address)
}

func newScope(currentSheet string, sheets map[string]string, namedRanges map[string]NamedRange) Scope {
	result := Scope{CurrentSheet: strings.ToUpper(strings.TrimSpace(currentSheet))}
	if sheets != nil {
		result.Sheets = make(map[string]string, len(sheets))
		for name, id := range sheets {
			result.Sheets[normalizeSheetName(name)] = strings.ToUpper(strings.TrimSpace(id))
		}
	}
	if namedRanges != nil {
		result.NamedRanges = make(map[string]NamedRange, len(namedRanges))
		for name, target := range namedRanges {
			target.SheetID = strings.ToUpper(strings.TrimSpace(target.SheetID))
			target.Range = normalizeCellAddress(target.Range)
			result.NamedRanges[normalizeSheetName(name)] = target
		}
	}
	return result
}

func (s Scope) resolveCell(qualifier, address string) (string, error) {
	sheetID := s.CurrentSheet
	if qualifier != "" {
		if s.Sheets == nil {
			sheetID = normalizeSheetName(qualifier)
		} else {
			var found bool
			sheetID, found = s.Sheets[normalizeSheetName(qualifier)]
			if !found {
				return "", formulaError("#REF!", "unknown sheet "+qualifier)
			}
		}
	}
	return CellKey(sheetID, address), nil
}

// extentOf reports how far a sheet's content reaches. A sheet with no content
// still spans one cell so an unbounded reference stays a valid, empty range
// rather than an error.
func (s Scope) extentOf(sheetID string) SheetExtent {
	extent := s.Extent[strings.ToUpper(strings.TrimSpace(sheetID))]
	if extent.Rows < 1 {
		extent.Rows = 1
	}
	if extent.Columns < 1 {
		extent.Columns = 1
	}
	return extent
}

func (s Scope) resolveNamedRange(name string) (NamedRange, error) {
	target, found := s.NamedRanges[normalizeSheetName(name)]
	if !found {
		return NamedRange{}, formulaError("#NAME?", "unknown name "+name)
	}
	if target.SheetID == "" || target.Range == "" {
		return NamedRange{}, formulaError("#REF!", "named range "+name+" has an invalid target")
	}
	return target, nil
}
