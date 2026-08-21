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
