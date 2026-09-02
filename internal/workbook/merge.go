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
			if current.SpillSource != "" {
				return nil, fmt.Errorf("%w: array result cell %s from %s cannot be merged or unmerged", ErrInvalid, cellrange.Address(row, column), current.SpillSource)
			}
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
			inputs = append(inputs, CellInput{Row: row, Column: column, Value: cloneJSON(current.Value), Formula: current.Formula, Style: style, SpillSource: current.SpillSource})
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

// Address is the range in A1 notation, e.g. "A1:B2".
func (m MergeMetadata) Address() string {
	return cellrange.Address(m.StartRow, m.StartColumn) + ":" + cellrange.Address(m.EndRow, m.EndColumn)
}

// brokenMerges finds the merges a write is about to break: a cell that belonged
// to a merge is being given a style that no longer carries that merge. Every
// covered cell stores the full range, so the written cell alone names it.
func brokenMerges(effective []CellInput, current func(row, column int) (Cell, bool)) []MergeMetadata {
	seen := make(map[MergeMetadata]struct{})
	ranges := make([]MergeMetadata, 0)
	for _, input := range effective {
		cell, ok := current(input.Row, input.Column)
		if !ok {
			continue
		}
		stored, existed, err := CellMerge(cell)
		if err != nil || !existed {
			continue
		}
		next, keeps, err := CellMerge(Cell{Row: input.Row, Column: input.Column, Style: input.Style})
		if err == nil && keeps && next == stored {
			continue
		}
		if _, done := seen[stored]; done {
			continue
		}
		seen[stored] = struct{}{}
		ranges = append(ranges, stored)
	}
	return ranges
}

// dissolveMerges makes a write that touches a merge dissolve the whole merge
// in the same operation. Leaving the other cells as they were used to produce
// a merge that some cells remembered and others did not: a value pasted into a
// covered cell was drawn over and invisible, and a fill that took the anchor
// left the rest pointing at a merge that no longer existed, which the next
// edit refused as invalid metadata. Google Sheets dissolves the merge; so do
// we, and the result names what was dissolved so the grid can say so.
//
// The written inputs inside a dissolved range lose the key too, and the cells
// the write did not mention come back as extra inputs that keep their value,
// formula and note. Undo restores the merge because both are in the operation.
func dissolveMerges(sheetID string, effective []CellInput, ranges []MergeMetadata, current func(row, column int) (Cell, bool)) ([]CellInput, []CellInput, error) {
	if len(ranges) == 0 {
		return effective, nil, nil
	}
	written := make(map[cellKey]int, len(effective))
	for index, input := range effective {
		written[cellKey{input.Row, input.Column}] = index
	}
	extra := make([]CellInput, 0)
	for _, merged := range ranges {
		for row := merged.StartRow; row <= merged.EndRow; row++ {
			for column := merged.StartColumn; column <= merged.EndColumn; column++ {
				if index, ok := written[cellKey{row, column}]; ok {
					style, err := setMergeMetadata(effective[index].Style, MergeMetadata{}, false)
					if err != nil {
						return nil, nil, err
					}
					effective[index].Style = style
					continue
				}
				cell, exists := current(row, column)
				if !exists {
					continue
				}
				stored, existed, err := CellMerge(cell)
				if err != nil || !existed || stored != merged {
					continue
				}
				style, err := setMergeMetadata(cell.Style, MergeMetadata{}, false)
				if err != nil {
					return nil, nil, err
				}
				extra = append(extra, CellInput{SheetID: sheetID, Row: row, Column: column, Value: cloneJSON(cell.Value), Formula: cell.Formula, Style: style, Note: cell.Note, SpillSource: cell.SpillSource})
			}
		}
	}
	return effective, extra, nil
}
