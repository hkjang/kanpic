package formula

import (
	"math"
	"strings"
)

// SparklineMarker tags the value a SPARKLINE formula produces. The chart is
// drawn by the client inside the cell, so the formula result carries the
// numbers and the appearance rather than a rendered picture.
const SparklineMarker = "sparkline"

// evaluateSparkline builds the description of a chart that lives in one cell.
// Options are written the way Google Sheets writes them — a two column array
// of names and values — which the array literal syntax makes easy to type:
//
//	=SPARKLINE(B2:G2,{"charttype","column";"color","#0f766e"})
func evaluateSparkline(arguments []any) (any, bool, error) {
	if len(arguments) < 1 || len(arguments) > 2 {
		return nil, true, argError("SPARKLINE")
	}
	data, err := toArray(arguments[0])
	if err != nil {
		return nil, true, err
	}
	numbers := make([]any, 0, len(data.values))
	for _, value := range data.values {
		if value == nil || display(value) == "" {
			continue
		}
		if formulaErr, isError := value.(*Error); isError {
			return nil, true, formulaErr
		}
		number, ok := toNumber(value)
		if !ok {
			continue
		}
		numbers = append(numbers, number)
	}
	if len(numbers) == 0 {
		return nil, true, formulaError("#N/A", "SPARKLINE needs numbers to chart")
	}
	if len(numbers) > 1000 {
		numbers = numbers[:1000]
	}
	chart := map[string]any{"kanpic": SparklineMarker, "chart": "line", "values": numbers, "color": "#0f766e"}
	if len(arguments) == 2 && !omitted(arguments[1]) {
		if err := applySparklineOptions(chart, arguments[1]); err != nil {
			return nil, true, err
		}
	}
	return chart, true, nil
}

// applySparklineOptions reads the name and value pairs, ignoring the ones this
// engine does not draw so a formula copied from elsewhere still works.
func applySparklineOptions(chart map[string]any, argument any) error {
	options, err := toArray(argument)
	if err != nil {
		return err
	}
	if options.columns != 2 && options.rows != 2 {
		return formulaError("#VALUE!", "SPARKLINE options need a name and a value in each row")
	}
	read := func(index int) (string, any) {
		if options.columns == 2 {
			return strings.ToLower(strings.TrimSpace(display(options.at(index, 0)))), options.at(index, 1)
		}
		return strings.ToLower(strings.TrimSpace(display(options.at(0, index)))), options.at(1, index)
	}
	count := options.rows
	if options.columns != 2 {
		count = options.columns
	}
	for index := 0; index < count; index++ {
		name, value := read(index)
		switch name {
		case "charttype":
			kind := strings.ToLower(strings.TrimSpace(display(value)))
			switch kind {
			case "line", "column", "bar", "winloss":
				chart["chart"] = kind
			default:
				return formulaError("#VALUE!", "SPARKLINE charttype must be line, column, bar or winloss")
			}
		case "color", "color1":
			chart["color"] = display(value)
		case "negcolor", "lowcolor":
			chart["negativeColor"] = display(value)
		case "highcolor":
			chart["highColor"] = display(value)
		case "axis":
			chart["axis"] = truthy(value)
		case "max", "ymax":
			if number, ok := toNumber(value); ok {
				chart["max"] = number
			}
		case "min", "ymin":
			if number, ok := toNumber(value); ok {
				chart["min"] = number
			}
		case "linewidth":
			if number, ok := toNumber(value); ok {
				chart["lineWidth"] = math.Max(1, math.Min(6, number))
			}
		case "empty", "nan", "rtl", "firstcolor", "lastcolor", "axiscolor":
			// Recognised by Sheets but not drawn here; ignored on purpose so a
			// pasted formula keeps working.
		}
	}
	return nil
}
