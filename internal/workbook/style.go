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
		} else {
			values[key] = cloneJSON(value)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return json.Marshal(values)
}

func applyStylePatch(current Cell, input CellInput, patch json.RawMessage) (CellInput, error) {
	style, err := mergeStylePatch(current.Style, patch)
	if err != nil {
		return CellInput{}, err
	}
	input.Value = cloneJSON(current.Value)
	input.Formula = current.Formula
	input.Style = style
	return input, nil
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
