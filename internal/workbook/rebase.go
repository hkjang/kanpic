package workbook

import (
	"kanpic/internal/formula"
)

// A cell write names a row and a column. If somebody deletes a row between the
// moment a client read the sheet and the moment its write arrives, those
// numbers point somewhere else than the author meant: a write to A5 lands on
// what used to be A6, quietly overwriting a value nobody touched.
//
// The structural changes since the client's base version are known, so the
// incoming addresses are moved through them the same way formula references
// are. A write to a row that was deleted has no landing place at all and is
// dropped rather than misplaced.
func rebaseCellInputs(cells []CellInput, changes []formula.StructuralChange) ([]CellInput, []CellCoordinate, int) {
	if len(changes) == 0 {
		return cells, nil, 0
	}
	rebased := make([]CellInput, 0, len(cells))
	dropped := make([]CellCoordinate, 0)
	moved := 0
	for _, input := range cells {
		row, column, alive := rebasePosition(input.Row, input.Column, changes)
		if !alive {
			dropped = append(dropped, CellCoordinate{Row: input.Row, Column: input.Column})
			continue
		}
		if row != input.Row || column != input.Column {
			moved++
		}
		input.Row, input.Column = row, column
		rebased = append(rebased, input)
	}
	return rebased, dropped, moved
}

func rebasePosition(row, column int, changes []formula.StructuralChange) (int, int, bool) {
	for _, change := range changes {
		if change.Axis == "column" {
			next, alive := formula.TransformPosition(column, change)
			if !alive {
				return 0, 0, false
			}
			column = next
			continue
		}
		next, alive := formula.TransformPosition(row, change)
		if !alive {
			return 0, 0, false
		}
		row = next
	}
	return row, column, true
}

// structuralChangeFromResult reads a recorded operation back into the shape the
// transform understands. Only row and column edits move addresses.
func structuralChangeFromResult(result MutationResult) (formula.StructuralChange, bool) {
	if result.StructuralAxis == "" || result.StructuralAction == "" || result.StructuralCount < 1 || result.StructuralIndex < 1 {
		return formula.StructuralChange{}, false
	}
	return formula.StructuralChange{
		Axis:        result.StructuralAxis,
		Action:      result.StructuralAction,
		Index:       result.StructuralIndex,
		Count:       result.StructuralCount,
		Destination: result.StructuralDestination,
	}, true
}
