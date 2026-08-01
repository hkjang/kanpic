package workbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
)

const MaxStylePatchBytes = 4 * 1024

var styleBooleanKeys = map[string]bool{"bold": true, "italic": true, "underline": true, "strike": true, "wrap": true}

var borderStyles = map[string]bool{"none": true, "thin": true, "medium": true, "thick": true, "dashed": true, "dotted": true, "double": true}
var borderPresets = map[string]bool{"all": true, "outer": true, "inner": true, "horizontal": true, "vertical": true, "top": true, "right": true, "bottom": true, "left": true, "none": true}

type BorderSide struct {
	Style string `json:"style"`
	Color string `json:"color"`
}

// BorderCommand materializes a range border atomically. Range coordinates are
// populated by the API layer and are deliberately not accepted from clients.
type BorderCommand struct {
	Preset      string `json:"preset"`
	Style       string `json:"style"`
	Color       string `json:"color"`
	StartRow    int    `json:"-"`
	StartColumn int    `json:"-"`
	EndRow      int    `json:"-"`
	EndColumn   int    `json:"-"`
}

func ValidateStylePatch(patch json.RawMessage) error {
	if len(bytes.TrimSpace(patch)) == 0 || len(patch) > MaxStylePatchBytes {
		return fmt.Errorf("%w: style patch must contain 1 to %d bytes", ErrInvalid, MaxStylePatchBytes)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(patch, &values); err != nil || values == nil || len(values) == 0 {
		return fmt.Errorf("%w: style patch must be a non-empty JSON object", ErrInvalid)
	}
	for key, raw := range values {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		if styleBooleanKeys[key] {
			var value bool
			if json.Unmarshal(raw, &value) != nil {
				return invalidStyleValue(key)
			}
			continue
		}
		switch key {
		case "color", "background":
			var value string
			if json.Unmarshal(raw, &value) != nil || !validHexColor(value) {
				return invalidStyleValue(key)
			}
		case "font_family", "number_format":
			var value string
			if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" || len(value) > 100 {
				return invalidStyleValue(key)
			}
		case "horizontal_align":
			var value string
			if json.Unmarshal(raw, &value) != nil || (value != "left" && value != "center" && value != "right") {
				return invalidStyleValue(key)
			}
		case "vertical_align":
			var value string
			if json.Unmarshal(raw, &value) != nil || (value != "top" && value != "middle" && value != "bottom") {
				return invalidStyleValue(key)
			}
		case "text_mode":
			var value string
			if json.Unmarshal(raw, &value) != nil || (value != "overflow" && value != "clip" && value != "wrap") {
				return invalidStyleValue(key)
			}
		case "borders":
			if err := validateBorders(raw); err != nil {
				return err
			}
		case "font_size":
			var value float64
			if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || value < 6 || value > 72 {
				return invalidStyleValue(key)
			}
		case "text_rotation":
			var value float64
			if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || value < -90 || value > 90 {
				return invalidStyleValue(key)
			}
		default:
			return fmt.Errorf("%w: unsupported style property %q", ErrInvalid, key)
		}
	}
	return nil
}

// ValidateCellStyle validates complete styles submitted by batch, paste, fill,
// REST, WebSocket and MCP callers. Merge metadata is an internal style field,
// but is still checked against the cell coordinate before persistence.
func ValidateCellStyle(input CellInput) error {
	if len(bytes.TrimSpace(input.Style)) == 0 {
		return nil
	}
	if len(input.Style) > MaxStylePatchBytes {
		return fmt.Errorf("%w: cell style exceeds %d bytes", ErrInvalid, MaxStylePatchBytes)
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input.Style, &values) != nil || values == nil {
		return fmt.Errorf("%w: cell style must be a JSON object", ErrInvalid)
	}
	if _, exists := values[mergeStyleKey]; exists {
		if _, _, err := CellMerge(Cell{Row: input.Row, Column: input.Column, Style: input.Style}); err != nil {
			return err
		}
		delete(values, mergeStyleKey)
	}
	if len(values) == 0 {
		return nil
	}
	ordinary, _ := json.Marshal(values)
	return ValidateStylePatch(ordinary)
}

func mergeStylePatch(current, patch json.RawMessage) (json.RawMessage, error) {
	if err := ValidateStylePatch(patch); err != nil {
		return nil, err
	}
	values := make(map[string]json.RawMessage)
	if len(bytes.TrimSpace(current)) > 0 {
		if err := json.Unmarshal(current, &values); err != nil || values == nil {
			return nil, fmt.Errorf("%w: stored cell style is invalid", ErrInvalid)
		}
	}
	var changes map[string]json.RawMessage
	_ = json.Unmarshal(patch, &changes)
	for key, value := range changes {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(values, key)
		} else if key == "borders" {
			merged, err := mergeBorders(values[key], value)
			if err != nil {
				return nil, err
			}
			if len(merged) == 0 {
				delete(values, key)
			} else {
				values[key] = merged
			}
		} else {
			values[key] = cloneJSON(value)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return json.Marshal(values)
}

func ValidateBorderCommand(command BorderCommand) error {
	if !borderPresets[command.Preset] {
		return invalidStyleValue("border.preset")
	}
	if command.Preset != "none" && !borderStyles[command.Style] {
		return invalidStyleValue("border.style")
	}
	if command.Preset != "none" && command.Style != "none" && !validHexColor(command.Color) {
		return invalidStyleValue("border.color")
	}
	if command.StartRow < 1 || command.StartColumn < 1 || command.EndRow < command.StartRow || command.EndColumn < command.StartColumn {
		return invalidStyleValue("border.range")
	}
	return nil
}

func applyCellFormatting(current Cell, input CellInput, patch json.RawMessage, border *BorderCommand) (CellInput, error) {
	style := cloneJSON(current.Style)
	var err error
	if len(patch) > 0 {
		style, err = mergeStylePatch(style, patch)
		if err != nil {
			return CellInput{}, err
		}
	}
	if border != nil {
		style, err = applyBorderCommand(style, *border, input.Row, input.Column)
		if err != nil {
			return CellInput{}, err
		}
	}
	input.Value = cloneJSON(current.Value)
	input.Formula = current.Formula
	input.Style = style
	input.SpillSource = current.SpillSource
	return input, nil
}

func validateBorders(raw json.RawMessage) error {
	var sides map[string]json.RawMessage
	if json.Unmarshal(raw, &sides) != nil || sides == nil || len(sides) == 0 {
		return invalidStyleValue("borders")
	}
	for name, value := range sides {
		if name != "top" && name != "right" && name != "bottom" && name != "left" {
			return invalidStyleValue("borders." + name)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		var side BorderSide
		if json.Unmarshal(value, &side) != nil || !borderStyles[side.Style] || side.Style == "none" && side.Color != "" || side.Style != "none" && !validHexColor(side.Color) {
			return invalidStyleValue("borders." + name)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(value, &fields)
		if len(fields) != 2 {
			return invalidStyleValue("borders." + name)
		}
		for field := range fields {
			if field != "style" && field != "color" {
				return invalidStyleValue("borders." + name)
			}
		}
	}
	return nil
}

func mergeBorders(current, patch json.RawMessage) (json.RawMessage, error) {
	var values map[string]json.RawMessage
	if len(bytes.TrimSpace(current)) > 0 && json.Unmarshal(current, &values) != nil {
		return nil, fmt.Errorf("%w: stored cell borders are invalid", ErrInvalid)
	}
	if values == nil {
		values = make(map[string]json.RawMessage)
	}
	var changes map[string]json.RawMessage
	if json.Unmarshal(patch, &changes) != nil {
		return nil, invalidStyleValue("borders")
	}
	for key, value := range changes {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(values, key)
			continue
		}
		var side BorderSide
		_ = json.Unmarshal(value, &side)
		if side.Style == "none" {
			delete(values, key)
		} else {
			values[key] = cloneJSON(value)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return json.Marshal(values)
}

func applyBorderCommand(current json.RawMessage, command BorderCommand, row, column int) (json.RawMessage, error) {
	if err := ValidateBorderCommand(command); err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	switch command.Preset {
	case "none", "all":
		selected = map[string]bool{"top": true, "right": true, "bottom": true, "left": true}
	case "outer":
		selected["top"] = row == command.StartRow
		selected["right"] = column == command.EndColumn
		selected["bottom"] = row == command.EndRow
		selected["left"] = column == command.StartColumn
	case "inner":
		selected["top"] = row > command.StartRow
		selected["left"] = column > command.StartColumn
	case "horizontal":
		selected["top"] = row > command.StartRow
	case "vertical":
		selected["left"] = column > command.StartColumn
	case "top":
		selected["top"] = true
	case "right":
		selected["right"] = true
	case "bottom":
		selected["bottom"] = true
	case "left":
		selected["left"] = true
	}
	patch := make(map[string]any)
	for _, side := range []string{"top", "right", "bottom", "left"} {
		if !selected[side] {
			continue
		}
		if command.Preset == "none" || command.Style == "none" {
			patch[side] = nil
		} else {
			patch[side] = BorderSide{Style: command.Style, Color: command.Color}
		}
	}
	if len(patch) == 0 {
		return cloneJSON(current), nil
	}
	data, _ := json.Marshal(map[string]any{"borders": patch})
	return mergeStylePatch(current, data)
}

func stylesEqual(left, right json.RawMessage) bool {
	if len(bytes.TrimSpace(left)) == 0 && len(bytes.TrimSpace(right)) == 0 {
		return true
	}
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func invalidStyleValue(key string) error {
	return fmt.Errorf("%w: invalid value for style property %q", ErrInvalid, key)
}

func validHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, character := range value[1:] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
