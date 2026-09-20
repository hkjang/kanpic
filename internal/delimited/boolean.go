package delimited

import "strings"

// Boolean reads only true/false, ignoring case. Whitespace and apostrophe
// policies belong to the caller, just as they do for Number.
func Boolean(text string) (bool, bool) {
	switch strings.ToLower(text) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}
