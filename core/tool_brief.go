package core

import (
	"strings"
)

// briefToolPreview flattens a tool input to a single line and keeps its
// first maxRunes runes. A leading "# description" line (how several
// adapters prefix shell commands) is dropped so the preview shows the
// command itself.
func briefToolPreview(input string, maxRunes int) string {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "# ") {
		if _, rest, ok := strings.Cut(input, "\n"); ok && strings.TrimSpace(rest) != "" {
			input = rest
		}
	}
	flat := strings.Join(strings.Fields(input), " ")
	// Backticks would break the inline-code span the preview is shown in.
	flat = strings.ReplaceAll(flat, "`", "'")
	if maxRunes <= 0 {
		return flat
	}
	r := []rune(flat)
	if len(r) <= maxRunes {
		return flat
	}
	return string(r[:maxRunes]) + "…"
}

// briefToolLine renders "<name> `<preview>`" for the one-line tool format.
// When the agent reports the input itself as the tool name (some ACP agents
// use the shell command as the title), only the preview is kept.
func briefToolLine(name, input string, maxRunes int) string {
	name = strings.TrimSpace(name)
	preview := briefToolPreview(input, maxRunes)
	if name != "" && strings.Join(strings.Fields(name), " ") == strings.Join(strings.Fields(strings.TrimSpace(input)), " ") {
		name = ""
	}
	if name != "" {
		name = briefToolPreview(name, maxRunes)
	}
	switch {
	case name == "" && preview == "":
		return "tool"
	case preview == "":
		return name
	case name == "":
		return "`" + preview + "`"
	default:
		return name + " `" + preview + "`"
	}
}
