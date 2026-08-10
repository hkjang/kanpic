package formula

import (
	"regexp"
	"strings"
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s.]+\.[^@\s]+$`)

// evaluateInformation answers questions about a value's kind. The IS family
// receives values that have already been evaluated; the error-aware members
// are handled earlier, where an argument's failure can still be caught.
func evaluateInformation(name string, values []any) (any, bool, error) {
	switch name {
	case "ISBLANK":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return values[0] == nil, true, nil
	case "ISNUMBER":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		if values[0] == nil {
			return false, true, nil
		}
		if _, isBoolean := values[0].(bool); isBoolean {
			return false, true, nil
		}
		if _, isText := values[0].(string); isText {
			return false, true, nil
		}
		_, ok := toNumber(values[0])
		return ok, true, nil
	case "ISTEXT", "ISNONTEXT":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		_, isText := values[0].(string)
		return isText == (name == "ISTEXT"), true, nil
	case "ISLOGICAL":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		_, isBoolean := values[0].(bool)
		return isBoolean, true, nil
	case "ISEVEN", "ISODD":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		even := int64(number)%2 == 0
		return even == (name == "ISEVEN"), true, nil
	case "ISEMAIL":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return emailPattern.MatchString(strings.TrimSpace(display(values[0]))), true, nil
	case "ISURL":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		text := strings.TrimSpace(display(values[0]))
		return strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://"), true, nil
	case "ISDATE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		_, ok := parseDate(values[0])
		return ok, true, nil
	case "NA":
		if len(values) != 0 {
			return nil, true, argError(name)
		}
		return nil, true, formulaError("#N/A", "NA()")
	case "N":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		if boolean, isBoolean := values[0].(bool); isBoolean {
			if boolean {
				return float64(1), true, nil
			}
			return float64(0), true, nil
		}
		if _, isText := values[0].(string); isText {
			return float64(0), true, nil
		}
		number, ok := toNumber(values[0])
		if !ok {
			return float64(0), true, nil
		}
		return number, true, nil
	case "TYPE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		switch values[0].(type) {
		case string:
			return float64(2), true, nil
		case bool:
			return float64(4), true, nil
		}
		return float64(1), true, nil
	case "XOR":
		if len(values) == 0 {
			return nil, true, argError(name)
		}
		result := false
		for _, value := range values {
			if truthy(value) {
				result = !result
			}
		}
		return result, true, nil
	}
	return nil, false, nil
}
