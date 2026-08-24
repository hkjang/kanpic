package workbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

func cellsEqual(left, right Cell) bool {
	return bytes.Equal(bytes.TrimSpace(left.Value), bytes.TrimSpace(right.Value)) &&
		left.Formula == right.Formula &&
		bytes.Equal(bytes.TrimSpace(left.Style), bytes.TrimSpace(right.Style)) &&
		left.SpillSource == right.SpillSource &&
		left.Note == right.Note
}

func submittedCoordinates(inputs []CellInput) []CellCoordinate {
	result := make([]CellCoordinate, 0, len(inputs))
	for _, input := range inputs {
		result = append(result, CellCoordinate{Row: input.Row, Column: input.Column})
	}
	return result
}

func inputFromCell(row, column int, cell Cell) CellInput {
	return CellInput{SheetID: cell.SheetID, Row: row, Column: column, Value: cloneJSON(cell.Value), Formula: cell.Formula, Style: cloneJSON(cell.Style), Note: cell.Note, SpillSource: cell.SpillSource}
}

type scopedCellKey struct {
	sheetID string
	cellKey
}

func parseCoordinateKey(value string) (CellCoordinate, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return CellCoordinate{}, fmt.Errorf("invalid coordinate %q", value)
	}
	row, rowErr := strconv.Atoi(parts[0])
	column, columnErr := strconv.Atoi(parts[1])
	if rowErr != nil || columnErr != nil || row < 1 || column < 1 {
		return CellCoordinate{}, fmt.Errorf("invalid coordinate %q", value)
	}
	return CellCoordinate{Row: row, Column: column}, nil
}

// recalculateCellInputs recalculates the formulas a mutation disturbs. imports
// carries the cross-workbook blocks IMPORTRANGE asked for; it is nil on the
// paths that have no repository to fetch them with, and those formulas then
// report that the source could not be reached rather than showing stale data.
// nameContext 는 수식을 풀 때 필요한 워크북 차원의 이름들이다. 하나씩
// 인수로 늘리면 부르는 자리 열한 곳을 매번 함께 고쳐야 한다.
type nameContext struct {
	Ranges    map[string]formula.NamedRange
	Functions map[string]formula.NamedFunction
	Imports   map[string]formula.ImportedRange
}

func recalculateCellInputs(sheets map[string]Sheet, existing map[string]map[cellKey]Cell, currentSheetID string, submitted []CellInput, forceAll bool, names nameContext) ([]CellInput, []CellCoordinate, []CellFormulaError, error) {
	prospective := cloneAllCells(existing)
	if prospective[currentSheetID] == nil {
		return nil, nil, nil, ErrNotFound
	}
	submittedKeys := make(map[scopedCellKey]struct{}, len(submitted))
	changed := make(map[scopedCellKey]struct{}, len(submitted))
	// Overwriting a cell that produced an array result has to clear what it
	// spilled. Looking for those cells by scanning the sheet once per written
	// cell makes a sort quadratic: sorting 16,000 rows took 18 seconds, and
	// almost all of it was this. One pass builds the same answer.
	spilledBySource := spillIndex(prospective[currentSheetID])
	for _, input := range submitted {
		input.SheetID = currentSheetID
		key := cellKey{input.Row, input.Column}
		scoped := scopedCellKey{sheetID: currentSheetID, cellKey: key}
		if _, duplicate := submittedKeys[scoped]; duplicate {
			return nil, nil, nil, fmt.Errorf("%w: duplicate cell %s in one operation", ErrInvalid, cellrange.Address(input.Row, input.Column))
		}
		current := prospective[currentSheetID][key]
		if current.SpillSource != "" && input.SpillSource != current.SpillSource {
			return nil, nil, nil, fmt.Errorf("%w: %s is part of the array result from %s; edit the source formula instead", ErrInvalid, cellrange.Address(input.Row, input.Column), current.SpillSource)
		}
		if address := cellrange.Address(input.Row, input.Column); len(spilledBySource[address]) > 0 {
			for _, cleared := range clearSpilledKeys(prospective[currentSheetID], spilledBySource[address]) {
				changed[scopedCellKey{sheetID: currentSheetID, cellKey: cleared}] = struct{}{}
			}
			delete(spilledBySource, address)
		}
		input.Value = cloneJSON(input.Value)
		input.Style = cloneJSON(input.Style)
		if input.Formula != "" {
			// Formula results are always server-authoritative. Ignore a cached
			// value supplied by REST, MCP or an imported client operation.
			input.Value = nil
			input.SpillSource = ""
		}
		submittedKeys[scoped] = struct{}{}
		cell := Cell{SheetID: currentSheetID, Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style), Note: input.Note, SpillSource: input.SpillSource}
		if isEmptyCell(cell) {
			delete(prospective[currentSheetID], key)
		} else {
			prospective[currentSheetID][key] = cell
		}
		changed[scoped] = struct{}{}
	}
	// A #SPILL! formula is also affected when a user clears a cell that used to
	// block its result. The blocker is not a formula dependency, so retry these
	// anchors on every ordinary mutation until expansion succeeds.
	for sheetID, cells := range prospective {
		for key, cell := range cells {
			if cell.Formula != "" && string(bytes.TrimSpace(cell.Value)) == `"#SPILL!"` {
				changed[scopedCellKey{sheetID: sheetID, cellKey: key}] = struct{}{}
			}
			if forceAll && cell.Formula != "" {
				changed[scopedCellKey{sheetID: sheetID, cellKey: key}] = struct{}{}
			}
			// INDIRECT, OFFSET and the clock and random functions do not depend
			// only on the cells they name, so they are recalculated on every
			// mutation rather than only when a dependency changes.
			if cell.Formula != "" && formula.IsVolatile(cell.Formula) {
				changed[scopedCellKey{sheetID: sheetID, cellKey: key}] = struct{}{}
			}
		}
	}

	sheetNames := make(map[string]string, len(sheets))
	sheetIDs := make(map[string]string, len(sheets))
	for sheetID, sheet := range sheets {
		sheetNames[sheet.Name] = sheetID
		sheetIDs[strings.ToUpper(sheetID)] = sheetID
	}
	evaluator := formula.NewScopedWithNames("", sheetNames, names.Ranges).WithImports(names.Imports)
	evaluator.SetNamedFunctions(names.Functions)
	forcedSpills := make(map[string]*formula.Error)
	recalculatedSet := make(map[scopedCellKey]struct{})
	formulaErrors := make(map[scopedCellKey]CellFormulaError)
	pending := changed
	stabilized := false
	for iteration := 0; iteration < 8; iteration++ {
		states, err := formulaStates(prospective, forcedSpills)
		if err != nil {
			return nil, nil, nil, err
		}
		graph, err := evaluator.Recalculate(states, addressesFromKeys(pending))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		next := make(map[scopedCellKey]struct{})
		for _, result := range graph.Cells {
			formulaSheetID, address, valid := formula.SplitCellKey(result.Address)
			sheetID, found := sheetIDs[formulaSheetID]
			selected, parseErr := cellrange.Parse(address)
			if !valid || !found || parseErr != nil {
				return nil, nil, nil, fmt.Errorf("%w: invalid calculated address %s", ErrInvalid, result.Address)
			}
			key := cellKey{selected.Start.Row, selected.Start.Column}
			scoped := scopedCellKey{sheetID: sheetID, cellKey: key}
			recalculatedSet[scoped] = struct{}{}
			current := prospective[sheetID][key]
			if result.Error != nil {
				for _, cleared := range clearSpillCells(prospective[sheetID], address) {
					clearedKey := scopedCellKey{sheetID: sheetID, cellKey: cleared}
					next[clearedKey] = struct{}{}
					recalculatedSet[clearedKey] = struct{}{}
				}
				encoded, _ := json.Marshal(result.Error.Code)
				current.Value, current.SpillSource = encoded, ""
				prospective[sheetID][key] = current
				formulaErrors[scoped] = formulaErrorResult(currentSheetID, sheetID, key, result.Error)
				continue
			}
			delete(formulaErrors, scoped)
			if matrix, array := formulaResultMatrix(result.Value); array {
				spillChanges, spillMessage, spillErr := materializeSpill(prospective[sheetID], key, matrix)
				if spillErr != nil {
					return nil, nil, nil, spillErr
				}
				for _, spillKey := range spillChanges {
					spillScoped := scopedCellKey{sheetID: sheetID, cellKey: spillKey}
					next[spillScoped] = struct{}{}
					recalculatedSet[spillScoped] = struct{}{}
				}
				if spillMessage != "" {
					forced := &formula.Error{Code: "#SPILL!", Message: spillMessage}
					forcedSpills[result.Address] = forced
					formulaErrors[scoped] = formulaErrorResult(currentSheetID, sheetID, key, forced)
					next[scoped] = struct{}{}
				} else {
					delete(forcedSpills, result.Address)
				}
				continue
			}
			delete(forcedSpills, result.Address)
			for _, cleared := range clearSpillCells(prospective[sheetID], address) {
				clearedKey := scopedCellKey{sheetID: sheetID, cellKey: cleared}
				next[clearedKey] = struct{}{}
				recalculatedSet[clearedKey] = struct{}{}
			}
			encoded, marshalErr := json.Marshal(result.Value)
			if marshalErr != nil {
				return nil, nil, nil, marshalErr
			}
			current = prospective[sheetID][key]
			current.Value, current.SpillSource = encoded, ""
			prospective[sheetID][key] = current
		}
		if len(next) == 0 {
			stabilized = true
			break
		}
		pending = next
	}
	if !stabilized {
		return nil, nil, nil, fmt.Errorf("%w: array formula recalculation did not stabilize", ErrInvalid)
	}

	expanded := changedCellInputs(existing, prospective, submittedKeys)
	recalculated := coordinatesFromKeys(currentSheetID, recalculatedSet)
	errors := make([]CellFormulaError, 0, len(formulaErrors))
	for _, formulaErr := range formulaErrors {
		errors = append(errors, formulaErr)
	}
	sort.Slice(errors, func(i, j int) bool {
		if errors[i].SheetID != errors[j].SheetID {
			return errors[i].SheetID < errors[j].SheetID
		}
		if errors[i].Row == errors[j].Row {
			return errors[i].Column < errors[j].Column
		}
		return errors[i].Row < errors[j].Row
	})
	return expanded, recalculated, errors, nil
}

func formulaErrorResult(currentSheetID, sheetID string, key cellKey, formulaErr *formula.Error) CellFormulaError {
	result := CellFormulaError{Row: key.row, Column: key.column, Code: formulaErr.Code, Message: formulaErr.Message}
	if sheetID != currentSheetID {
		result.SheetID = sheetID
	}
	return result
}

func inputsForSheet(inputs []CellInput, sheetID string) []CellInput {
	result := make([]CellInput, 0, len(inputs))
	for _, input := range inputs {
		if input.SheetID == sheetID || input.SheetID == "" {
			result = append(result, input)
		}
	}
	return result
}

func formulaStates(cells map[string]map[cellKey]Cell, forced map[string]*formula.Error) (map[string]formula.CellState, error) {
	states := make(map[string]formula.CellState)
	for sheetID, sheetCells := range cells {
		for key, cell := range sheetCells {
			address := formula.CellKey(sheetID, cellrange.Address(key.row, key.column))
			var value any
			if len(cell.Value) > 0 {
				if err := json.Unmarshal(cell.Value, &value); err != nil {
					return nil, fmt.Errorf("%w: cell %s has invalid JSON value", ErrInvalid, address)
				}
			}
			states[address] = formula.CellState{Value: value, Formula: cell.Formula, ForcedError: forced[address]}
		}
	}
	return states, nil
}

// spillIndex groups the cells an array formula produced under the address of
// the formula that produced them.
func spillIndex(cells map[cellKey]Cell) map[string][]cellKey {
	index := make(map[string][]cellKey)
	for key, cell := range cells {
		if cell.SpillSource == "" {
			continue
		}
		index[cell.SpillSource] = append(index[cell.SpillSource], key)
	}
	return index
}

// clearSpilledKeys empties the cells the index already named, which is the
// same work clearSpillCells does without hunting for them again.
func clearSpilledKeys(cells map[cellKey]Cell, keys []cellKey) []cellKey {
	cleared := make([]cellKey, 0, len(keys))
	for _, key := range keys {
		cell, found := cells[key]
		if !found {
			continue
		}
		cell.Value, cell.Formula, cell.SpillSource = nil, "", ""
		if isEmptyCell(cell) {
			delete(cells, key)
		} else {
			cells[key] = cell
		}
		cleared = append(cleared, key)
	}
	return cleared
}

func clearSpillCells(cells map[cellKey]Cell, source string) []cellKey {
	changed := make([]cellKey, 0)
	for key, cell := range cells {
		if cell.SpillSource != source {
			continue
		}
		cell.Value, cell.Formula, cell.SpillSource = nil, "", ""
		if isEmptyCell(cell) {
			delete(cells, key)
		} else {
			cells[key] = cell
		}
		changed = append(changed, key)
	}
	return changed
}

func formulaResultMatrix(value any) ([][]any, bool) {
	switch typed := value.(type) {
	case [][]any:
		return typed, true
	case []any:
		rows := make([][]any, len(typed))
		for index, item := range typed {
			rows[index] = []any{item}
		}
		return rows, true
	default:
		return nil, false
	}
}

func materializeSpill(cells map[cellKey]Cell, anchor cellKey, matrix [][]any) ([]cellKey, string, error) {
	rows := len(matrix)
	if rows == 0 {
		return nil, "array result is empty", nil
	}
	columns := len(matrix[0])
	if columns == 0 || rows > MaxPasteCells || columns > MaxPasteCells || rows > MaxPasteCells/columns {
		return nil, fmt.Sprintf("array result exceeds %d cells", MaxPasteCells), nil
	}
	for _, row := range matrix {
		if len(row) != columns {
			return nil, "array result is not rectangular", nil
		}
	}
	if anchor.row+rows-1 > MaxValidationRows || anchor.column+columns-1 > MaxValidationColumns {
		return nil, "array result exceeds the sheet boundary", nil
	}
	source := cellrange.Address(anchor.row, anchor.column)
	before := make(map[cellKey]Cell)
	for key, cell := range cells {
		if cell.SpillSource == source {
			before[key] = cloneCell(cell)
		}
	}
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			key := cellKey{anchor.row + row, anchor.column + column}
			if _, captured := before[key]; !captured {
				before[key] = cloneCell(cells[key])
			}
		}
	}
	clearSpillCells(cells, source)
	if rows*columns > 1 {
		for row := 0; row < rows; row++ {
			for column := 0; column < columns; column++ {
				key := cellKey{anchor.row + row, anchor.column + column}
				current := cells[key]
				if key == anchor {
					if _, merged, err := CellMerge(current); err != nil {
						return nil, "", err
					} else if merged {
						setFormulaError(cells, anchor, "#SPILL!")
						return changedSpillKeys(before, cells, anchor), "array result cannot expand from a merged cell", nil
					}
					continue
				}
				_, merged, mergeErr := CellMerge(current)
				if mergeErr != nil {
					return nil, "", mergeErr
				}
				if len(bytes.TrimSpace(current.Value)) > 0 || current.Formula != "" || current.SpillSource != "" || merged {
					setFormulaError(cells, anchor, "#SPILL!")
					return changedSpillKeys(before, cells, anchor), fmt.Sprintf("array result would overwrite %s", cellrange.Address(key.row, key.column)), nil
				}
			}
		}
	}
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			key := cellKey{anchor.row + row, anchor.column + column}
			encoded, err := json.Marshal(matrix[row][column])
			if err != nil {
				return nil, "", err
			}
			current := cells[key]
			current.Row, current.Column, current.Value = key.row, key.column, encoded
			if key == anchor {
				current.SpillSource = ""
			} else {
				current.Formula, current.SpillSource = "", source
			}
			cells[key] = current
		}
	}
	return changedSpillKeys(before, cells, anchor), "", nil
}

func setFormulaError(cells map[cellKey]Cell, anchor cellKey, code string) {
	current := cells[anchor]
	current.Value, _ = json.Marshal(code)
	current.SpillSource = ""
	cells[anchor] = current
}

func changedSpillKeys(before map[cellKey]Cell, after map[cellKey]Cell, anchor cellKey) []cellKey {
	keys := make(map[cellKey]struct{}, len(before))
	for key := range before {
		if key != anchor {
			keys[key] = struct{}{}
		}
	}
	for key, cell := range after {
		if key != anchor && cell.SpillSource == cellrange.Address(anchor.row, anchor.column) {
			keys[key] = struct{}{}
		}
	}
	changed := make([]cellKey, 0, len(keys))
	for key := range keys {
		if !cellsEqual(before[key], after[key]) {
			changed = append(changed, key)
		}
	}
	return changed
}

func changedCellInputs(existing, prospective map[string]map[cellKey]Cell, submitted map[scopedCellKey]struct{}) []CellInput {
	keys := make(map[scopedCellKey]struct{}, len(submitted))
	for sheetID, cells := range existing {
		for key := range cells {
			keys[scopedCellKey{sheetID: sheetID, cellKey: key}] = struct{}{}
		}
	}
	for sheetID, cells := range prospective {
		for key := range cells {
			keys[scopedCellKey{sheetID: sheetID, cellKey: key}] = struct{}{}
		}
	}
	for key := range submitted {
		keys[key] = struct{}{}
	}
	ordered := make([]scopedCellKey, 0, len(keys))
	for key := range keys {
		if _, required := submitted[key]; required || !cellsEqual(existing[key.sheetID][key.cellKey], prospective[key.sheetID][key.cellKey]) {
			ordered = append(ordered, key)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].sheetID != ordered[j].sheetID {
			return ordered[i].sheetID < ordered[j].sheetID
		}
		if ordered[i].cellKey.row == ordered[j].cellKey.row {
			return ordered[i].cellKey.column < ordered[j].cellKey.column
		}
		return ordered[i].cellKey.row < ordered[j].cellKey.row
	})
	result := make([]CellInput, 0, len(ordered))
	for _, key := range ordered {
		input := inputFromCell(key.cellKey.row, key.cellKey.column, prospective[key.sheetID][key.cellKey])
		input.SheetID = key.sheetID
		result = append(result, input)
	}
	return result
}

func addressesFromKeys(keys map[scopedCellKey]struct{}) []string {
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, formula.CellKey(key.sheetID, cellrange.Address(key.cellKey.row, key.cellKey.column)))
	}
	sort.Strings(result)
	return result
}

func coordinatesFromKeys(currentSheetID string, keys map[scopedCellKey]struct{}) []CellCoordinate {
	result := make([]CellCoordinate, 0, len(keys))
	for key := range keys {
		coordinate := CellCoordinate{Row: key.cellKey.row, Column: key.cellKey.column}
		if key.sheetID != currentSheetID {
			coordinate.SheetID = key.sheetID
		}
		result = append(result, coordinate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SheetID != result[j].SheetID {
			return result[i].SheetID < result[j].SheetID
		}
		if result[i].Row == result[j].Row {
			return result[i].Column < result[j].Column
		}
		return result[i].Row < result[j].Row
	})
	return result
}
