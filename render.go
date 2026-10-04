package macroexp

import "strings"

// Render flattens tokens back to text with minimal spacing.
func Render(tokens []Token) string {
	var b strings.Builder
	prev := ""
	for _, t := range tokens {
		text := t.Text
		if t.Kind == Str {
			text = "\"" + text + "\""
		}
		if needSpace(prev, text) {
			b.WriteByte(' ')
		}
		b.WriteString(text)
		prev = text
	}
	return b.String()
}

func needSpace(a, b string) bool {
	if a == "" {
		return false
	}
	if strings.ContainsAny(b, ",;)]}") {
		return false
	}
	if strings.ContainsAny(a, "([{") {
		return false
	}
	return true
}
