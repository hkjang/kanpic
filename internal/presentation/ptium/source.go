// Package ptium writes kanpic decks into Ptium.
//
// It is the only place in kanpic that knows Ptium exists. Everything above it
// works in the neutral deck model, so another presentation service is another
// package beside this one rather than a change spread through the editor.
package ptium

import (
	"strings"

	"kanpic/internal/presentation"
)

// Ptium's own component names. The deck model already uses these names for the
// shapes both sides share, so this map only has to cover the rest.
var componentNames = map[string]string{
	"kpi": "kpi", "bars": "bars", "line": "line", "share": "share",
	"comparison": "comparison", "table": "table", "steps": "steps",
	"timeline": "timeline", "callout": "callout", "quote": "quote",
}

var slideKinds = map[string]string{
	presentation.SlideCover:   "@cover",
	presentation.SlideContent: "@content",
	presentation.SlideClosing: "@closing",
}

// WriteSource renders a deck in Ptium's slide language.
//
// The language is line-based and every line carries its own marker, so the only
// thing that has to be handled carefully is text that would be read as a marker
// where it sits: a title starting with '#', a field containing '|'. Ptium
// escapes both with a leading backslash.
func WriteSource(deck presentation.Deck) string {
	lines := []string{}
	for index, slide := range deck.Slides {
		if index > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "# "+escapeTitle(slide.Title))
		if kind, known := slideKinds[slide.Kind]; known && kind != "" {
			lines = append(lines, kind)
		}
		if lead := clean(slide.Lead); lead != "" {
			lines = append(lines, "> "+lead)
		}
		for _, bullet := range slide.Bullets {
			if text := clean(bullet); text != "" {
				lines = append(lines, "- "+text)
			}
		}
		if slide.Component != nil {
			lines = append(lines, componentLines(*slide.Component)...)
		}
		if notes := clean(slide.Notes); notes != "" {
			lines = append(lines, "!notes "+notes)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func componentLines(component presentation.Component) []string {
	kind, known := componentNames[component.Kind]
	if !known {
		// 모르는 종류를 지어내지 않는다. 표로 두면 값은 전부 남는다.
		kind = "table"
	}
	opening := "::" + kind
	if caption := clean(component.Caption); caption != "" {
		opening += " " + caption
	}
	lines := []string{opening}
	for _, row := range component.Rows {
		fields := append([]string{escapeField(row.Label)}, escapeFields(row.Fields)...)
		// 뒤쪽 빈 칸은 지운다. "영업1 | 120 | " 는 값이 없는 열을 하나 더
		// 만든다.
		for len(fields) > 1 && strings.TrimSpace(fields[len(fields)-1]) == "" {
			fields = fields[:len(fields)-1]
		}
		lines = append(lines, "- "+strings.Join(fields, " | "))
	}
	return append(lines, "::")
}

func escapeFields(fields []string) []string {
	escaped := make([]string, 0, len(fields))
	for _, field := range fields {
		escaped = append(escaped, escapeField(field))
	}
	return escaped
}

// escapeField protects a value that carries the field separator. A department
// called "영업1|2" would otherwise silently become two columns.
func escapeField(text string) string {
	cleaned := clean(text)
	if strings.Contains(cleaned, "|") {
		return "\\" + cleaned
	}
	return cleaned
}

// escapeTitle protects a title that starts with a marker of its own.
func escapeTitle(text string) string {
	cleaned := clean(text)
	if cleaned == "" {
		return "제목 없음"
	}
	switch cleaned[0] {
	case '#', '>', '-', '@', '!', ':', '/', '\\':
		return "\\" + cleaned
	}
	return cleaned
}

// clean flattens a value onto one line. The language is line-based, so a cell
// with a newline in it would otherwise become a second bullet with no marker.
func clean(text string) string {
	replaced := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(text)
	return strings.TrimSpace(strings.Join(strings.Fields(replaced), " "))
}
