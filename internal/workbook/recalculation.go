package workbook

import (
	"encoding/json"
	"fmt"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

func recalculateCellInputs(existing map[cellKey]Cell, submitted []CellInput) ([]CellInput, []CellCoordinate, []CellFormulaError, error) {
	prospective := make(map[cellKey]Cell, len(existing)+len(submitted))
	for key, cell := range existing {
		prospective[key] = cloneCell(cell)
	}
	expanded := make([]CellInput, len(submitted))
	submittedIndex := make(map[cellKey]int, len(submitted))
	changed := make([]string, 0, len(submitted))
	for index, input := range submitted {
		key := cellKey{input.Row, input.Column}
		if _, duplicate := submittedIndex[key]; duplicate {
			return nil, nil, nil, fmt.Errorf("%w: duplicate cell %s in one operation", ErrInvalid, cellrange.Address(input.Row, input.Column))
		}
		input.Value = cloneJSON(input.Value)
		input.Style = cloneJSON(input.Style)
		if input.Formula != "" {
			// Formula results are always server-authoritative. Ignore a cached
			// value supplied by REST, MCP or an imported client operation.
			input.Value = nil
		}
		expanded[index] = input
		submittedIndex[key] = index
		cell := Cell{Row: input.Row, Column: input.Column, Value: cloneJSON(input.Value), Formula: input.Formula, Style: cloneJSON(input.Style)}
		if isEmptyCell(cell) {
			delete(prospective, key)
		} else {
			prospective[key] = cell
		}
		changed = append(changed, cellrange.Address(input.Row, input.Column))
	}

	states := make(map[string]formula.CellState, len(prospective))
	for key, cell := range prospective {
		var value any
		if len(cell.Value) > 0 {
			if err := json.Unmarshal(cell.Value, &value); err != nil {
				return nil, nil, nil, fmt.Errorf("%w: cell %s has invalid JSON value", ErrInvalid, cellrange.Address(key.row, key.column))
			}
		}
		states[cellrange.Address(key.row, key.column)] = formula.CellState{Value: value, Formula: cell.Formula}
	}
	graph, err := formula.New().Recalculate(states, changed)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	recalculated := make([]CellCoordinate, 0, len(graph.Cells))
	formulaErrors := make([]CellFormulaError, 0)
	for _, result := range graph.Cells {
		selected, err := cellrange.Parse(result.Address)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: invalid calculated address %s", ErrInvalid, result.Address)
		}
		coordinate := CellCoordinate{Row: selected.Start.Row, Column: selected.Start.Column}
		key := cellKey{coordinate.Row, coordinate.Column}
		value := result.Value
		if result.Error != nil {
			value = result.Error.Code
			formulaErrors = append(formulaErrors, CellFormulaError{Row: coordinate.Row, Column: coordinate.Column, Code: result.Error.Code, Message: result.Error.Message})
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, nil, nil, err
		}
		if index, submittedCell := submittedIndex[key]; submittedCell {
			expanded[index].Value = encoded
		} else {
			current := prospective[key]
			expanded = append(expanded, CellInput{Row: coordinate.Row, Column: coordinate.Column, Value: encoded, Formula: current.Formula, Style: cloneJSON(current.Style)})
		}
		recalculated = append(recalculated, coordinate)
	}
	return expanded, recalculated, formulaErrors, nil
}
