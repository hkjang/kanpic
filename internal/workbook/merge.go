package workbook

import (
	"bytes"
	"encoding/json"
	"fmt"

	"kanpic/pkg/cellrange"
)

const mergeStyleKey = "merge"

// MergeMetadata is stored on every cell covered by a merged range. Keeping the
// complete range on covered cells lets clients resolve a merge even when its
// top-left cell is outside the currently loaded viewport.
type MergeMetadata struct {
	StartRow    int `json:"start_row"`
	StartColumn int `json:"start_column"`
	EndRow      int `json:"end_row"`
	EndColumn   int `json:"end_column"`
}

func (m MergeMetadata) Range() cellrange.Range {
	return cellrange.Range{
		Start: cellrange.Position{Row: m.StartRow, Column: m.StartColumn},
		End:   cellrange.Position{Row: m.EndRow, Column: m.EndColumn},
	}
}

func mergeMetadataFor(selected cellrange.Range) MergeMetadata {
	return MergeMetadata{
		StartRow: selected.Start.Row, StartColumn: selected.Start.Column,
		EndRow: selected.End.Row, EndColumn: selected.End.Column,
	}
}

func sameMerge(metadata MergeMetadata, selected cellrange.Range) bool {
	return metadata == mergeMetadataFor(selected)
}

// CellMerge returns validated merge metadata from a cell style.
func CellMerge(cell Cell) (MergeMetadata, bool, error) {
	if len(bytes.TrimSpace(cell.Style)) == 0 {
		return MergeMetadata{}, false, nil
	}
	var style map[string]json.RawMessage
	if err := json.Unmarshal(cell.Style, &style); err != nil || style == nil {
		return MergeMetadata{}, false, fmt.Errorf("%w: stored cell style is invalid", ErrInvalid)
	}
	raw, exists := style[mergeStyleKey]
	if !exists {
		return MergeMetadata{}, false, nil
	}
	var metadata MergeMetadata
	if json.Unmarshal(raw, &metadata) != nil || metadata.StartRow < 1 || metadata.StartColumn < 1 || metadata.EndRow < metadata.StartRow || metadata.EndColumn < metadata.StartColumn || !metadata.Range().Contains(cell.Row, cell.Column) {
		return MergeMetadata{}, false, fmt.Errorf("%w: stored merge metadata is invalid", ErrInvalid)
	}
	return metadata, true, nil
}

// BuildMergeCells materializes a non-destructive merge or unmerge operation.
// Values, formulas, and ordinary formatting are preserved for every cell.
func BuildMergeCells(existing []Cell, selected cellrange.Range, merge bool) ([]CellInput, error) {
	rows := selected.End.Row - selected.Start.Row + 1
	columns := selected.End.Column - selected.Start.Column + 1
	if rows < 1 || columns < 1 || rows > MaxPasteCells || columns > MaxPasteCells || rows > MaxPasteCells/columns {
		return nil, fmt.Errorf("%w: merged range must contain 1 to %d cells", ErrInvalid, MaxPasteCells)
	}
	if merge && rows == 1 && columns == 1 {
		return nil, fmt.Errorf("%w: merged range must contain at least two cells", ErrInvalid)
	}
	byCoordinate := make(map[string]Cell, len(existing))
	for _, cell := range existing {
		byCoordinate[coordinateKey(cell.Row, cell.Column)] = cell
	}
	metadata := mergeMetadataFor(selected)
	inputs := make([]CellInput, 0, rows*columns)
	for row := selected.Start.Row; row <= selected.End.Row; row++ {
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			current := byCoordinate[coordinateKey(row, column)]
			stored, exists, err := CellMerge(current)
			if err != nil {
				return nil, err
			}
			if exists && !sameMerge(stored, selected) {
				return nil, fmt.Errorf("%w: selected range overlaps another merged range", ErrInvalid)
			}
			style, err := setMergeMetadata(current.Style, metadata, merge)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, CellInput{Row: row, Column: column, Value: cloneJSON(current.Value), Formula: current.Formula, Style: style})
		}
	}
	return inputs, nil
}

func setMergeMetadata(current json.RawMessage, metadata MergeMetadata, merge bool) (json.RawMessage, error) {
	style := make(map[string]json.RawMessage)
	if len(bytes.TrimSpace(current)) > 0 {
		if err := json.Unmarshal(current, &style); err != nil || style == nil {
			return nil, fmt.Errorf("%w: stored cell style is invalid", ErrInvalid)
		}
	}
	if merge {
		encoded, _ := json.Marshal(metadata)
		style[mergeStyleKey] = encoded
	} else {
		delete(style, mergeStyleKey)
	}
	if len(style) == 0 {
		return nil, nil
	}
	return json.Marshal(style)
}
