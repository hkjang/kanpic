package formula

import "strings"

// Scope resolves user-visible sheet names to stable sheet identifiers. The
// formula graph uses stable identifiers so renaming a sheet does not change
// the identity of its cells while formulas keep their familiar Sheet!A1 text.
type Scope struct {
	CurrentSheet string
	Sheets       map[string]string
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

func newScope(currentSheet string, sheets map[string]string) Scope {
	result := Scope{CurrentSheet: strings.ToUpper(strings.TrimSpace(currentSheet))}
	if sheets != nil {
		result.Sheets = make(map[string]string, len(sheets))
		for name, id := range sheets {
			result.Sheets[normalizeSheetName(name)] = strings.ToUpper(strings.TrimSpace(id))
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
