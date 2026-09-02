package importexport

import (
	"encoding/json"
	"strings"

	"github.com/xuri/excelize/v2"

	"kanpic/internal/workbook"
)

var builtInNumberFormats = map[int]string{
	1: "0", 2: "0.00", 3: "#,##0", 4: "#,##0.00", 9: "0%", 10: "0.00%", 11: "0.00E+00",
	12: "# ?/?", 13: "# ??/??", 14: "mm-dd-yy", 15: "d-mmm-yy", 16: "d-mmm", 17: "mmm-yy",
	18: "h:mm AM/PM", 19: "h:mm:ss AM/PM", 20: "hh:mm", 21: "hh:mm:ss", 22: "m/d/yy hh:mm",
	37: "#,##0 ;(#,##0)", 38: "#,##0 ;[red](#,##0)", 39: "#,##0.00 ;(#,##0.00)", 40: "#,##0.00 ;[red](#,##0.00)",
	45: "mm:ss", 46: "[h]:mm:ss", 47: "mm:ss.0", 48: "##0.0E+0", 49: "@",
}

type canonicalBorderSide struct {
	Style string `json:"style"`
	Color string `json:"color"`
}

func canonicalStyleFromXLSX(source *excelize.Style) json.RawMessage {
	if source == nil {
		return nil
	}
	style := make(map[string]any)
	if font := source.Font; font != nil {
		if font.Bold {
			style["bold"] = true
		}
		if font.Italic {
			style["italic"] = true
		}
		if font.Underline != "" && font.Underline != "none" {
			style["underline"] = true
		}
		if font.Strike {
			style["strike"] = true
		}
		if strings.TrimSpace(font.Family) != "" {
			style["font_family"] = font.Family
		}
		if font.Size >= 6 && font.Size <= 72 {
			style["font_size"] = font.Size
		}
		if color := canonicalColor(font.Color); color != "" {
			style["color"] = color
		}
	}
	if source.Fill.Pattern != 0 && len(source.Fill.Color) > 0 {
		if color := canonicalColor(source.Fill.Color[0]); color != "" {
			style["background"] = color
		}
	}
	if alignment := source.Alignment; alignment != nil {
		switch alignment.Horizontal {
		case "left", "center", "right":
			style["horizontal_align"] = alignment.Horizontal
		}
		switch alignment.Vertical {
		case "top", "bottom":
			style["vertical_align"] = alignment.Vertical
		case "center":
			style["vertical_align"] = "middle"
		}
		if alignment.WrapText {
			style["text_mode"] = "wrap"
		}
		if alignment.TextRotation >= -90 && alignment.TextRotation <= 90 && alignment.TextRotation != 0 {
			style["text_rotation"] = alignment.TextRotation
		}
	}
	borders := make(map[string]canonicalBorderSide)
	for _, border := range source.Border {
		if border.Type != "top" && border.Type != "right" && border.Type != "bottom" && border.Type != "left" {
			continue
		}
		borderStyle := canonicalBorderStyle(border.Style)
		if borderStyle == "" {
			continue
		}
		color := canonicalColor(border.Color)
		if color == "" {
			color = "#000000"
		}
		borders[border.Type] = canonicalBorderSide{Style: borderStyle, Color: color}
	}
	if len(borders) > 0 {
		style["borders"] = borders
	}
	if source.CustomNumFmt != nil && strings.TrimSpace(*source.CustomNumFmt) != "" && !strings.EqualFold(*source.CustomNumFmt, "general") {
		style["number_format"] = *source.CustomNumFmt
	} else if format := builtInNumberFormats[source.NumFmt]; format != "" {
		style["number_format"] = format
	}
	style = acceptedStyle(style)
	if len(style) == 0 {
		return nil
	}
	data, _ := json.Marshal(style)
	return data
}

// acceptedStyle drops the properties this service would refuse from any other
// caller. Importing more than the validator accepts does not fail the import —
// it leaves cells whose style comes straight back on the next edit and turns a
// single keystroke into a 400. The rule is not restated here; the server's own
// validator is asked, one property at a time, so the two cannot drift apart.
func acceptedStyle(style map[string]any) map[string]any {
	for key, value := range style {
		encoded, err := json.Marshal(map[string]any{key: value})
		if err != nil || workbook.ValidateStylePatch(encoded) != nil {
			delete(style, key)
		}
	}
	return style
}

func xlsxStyle(raw json.RawMessage) *excelize.Style {
	if len(raw) == 0 {
		return nil
	}
	var canonical map[string]json.RawMessage
	if json.Unmarshal(raw, &canonical) != nil || canonical == nil {
		return nil
	}
	style := &excelize.Style{}
	hasStyle := false
	font := &excelize.Font{}
	fontSet := false
	decodeBool(canonical["bold"], &font.Bold, &fontSet)
	decodeBool(canonical["italic"], &font.Italic, &fontSet)
	decodeBool(canonical["strike"], &font.Strike, &fontSet)
	var underline bool
	if json.Unmarshal(canonical["underline"], &underline) == nil && underline {
		font.Underline = "single"
		fontSet = true
	}
	if family := decodeString(canonical["font_family"]); family != "" {
		font.Family = family
		fontSet = true
	}
	if color := decodeString(canonical["color"]); color != "" {
		font.Color = xlsxColor(color)
		fontSet = true
	}
	if rawSize := canonical["font_size"]; len(rawSize) > 0 && json.Unmarshal(rawSize, &font.Size) == nil {
		fontSet = true
	}
	if fontSet {
		style.Font = font
		hasStyle = true
	}
	if color := decodeString(canonical["background"]); color != "" {
		style.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{xlsxColor(color)}}
		hasStyle = true
	}
	alignment := &excelize.Alignment{}
	alignmentSet := false
	if horizontal := decodeString(canonical["horizontal_align"]); horizontal != "" {
		alignment.Horizontal = horizontal
		alignmentSet = true
	}
	if vertical := decodeString(canonical["vertical_align"]); vertical != "" {
		if vertical == "middle" {
			vertical = "center"
		}
		alignment.Vertical = vertical
		alignmentSet = true
	}
	textMode := decodeString(canonical["text_mode"])
	var legacyWrap bool
	_ = json.Unmarshal(canonical["wrap"], &legacyWrap)
	if textMode == "wrap" || legacyWrap {
		alignment.WrapText = true
		alignmentSet = true
	}
	if rawRotation := canonical["text_rotation"]; len(rawRotation) > 0 && json.Unmarshal(rawRotation, &alignment.TextRotation) == nil {
		alignmentSet = true
	}
	if alignmentSet {
		style.Alignment = alignment
		hasStyle = true
	}
	var borders map[string]canonicalBorderSide
	if json.Unmarshal(canonical["borders"], &borders) == nil {
		for _, side := range []string{"left", "right", "top", "bottom"} {
			border, ok := borders[side]
			if !ok || border.Style == "none" {
				continue
			}
			style.Border = append(style.Border, excelize.Border{Type: side, Color: xlsxColor(border.Color), Style: xlsxBorderStyle(border.Style)})
			hasStyle = true
		}
	}
	if numberFormat := decodeString(canonical["number_format"]); numberFormat != "" {
		style.CustomNumFmt = &numberFormat
		hasStyle = true
	}
	if !hasStyle {
		return nil
	}
	return style
}

func decodeBool(raw json.RawMessage, target *bool, set *bool) {
	if len(raw) > 0 && json.Unmarshal(raw, target) == nil {
		*set = true
	}
}

func decodeString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func canonicalColor(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) == 8 {
		value = value[2:]
	}
	if len(value) != 6 {
		return ""
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return ""
		}
	}
	return "#" + strings.ToUpper(value)
}

func xlsxColor(value string) string { return strings.TrimPrefix(strings.ToUpper(value), "#") }

func canonicalBorderStyle(style int) string {
	switch style {
	case 1:
		return "thin"
	case 2, 8, 10, 12, 13:
		return "medium"
	case 3, 9, 11:
		return "dashed"
	case 4, 7:
		return "dotted"
	case 5:
		return "thick"
	case 6:
		return "double"
	default:
		return ""
	}
}

func xlsxBorderStyle(style string) int {
	switch style {
	case "thin":
		return 1
	case "medium":
		return 2
	case "dashed":
		return 3
	case "dotted":
		return 4
	case "thick":
		return 5
	case "double":
		return 6
	default:
		return 0
	}
}
