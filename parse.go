package netsec

type segment struct {
	literal bool
	text    string
	refName string
}

// isValidName enforces conventional environment variable names: an ASCII
// letter or underscore first, followed by letters, digits or underscores.
func isValidName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			// always allowed
		case i > 0 && r >= '0' && r <= '9':
			// allowed after the first rune
		default:
			return false
		}
	}
	return true
}

// parseTemplate parses a trusted template. Literal bytes are preserved
// verbatim. "$$" yields one literal '$'; "${NAME}" yields a reference. Any
// other '$' occurrence is a syntax error.
func parseTemplate(name, template string) ([]segment, error) {
	var segments []segment
	literalStart := 0
	flushLiteral := func(end int) {
		if end > literalStart {
			segments = append(segments, segment{
				literal: true,
				text:    template[literalStart:end],
			})
		}
	}

	for i := 0; i < len(template); {
		if template[i] != '$' {
			i++
			continue
		}
		if i+1 >= len(template) {
			return nil, &SyntaxError{Name: name, Position: i, Template: template}
		}
		switch template[i+1] {
		case '$':
			flushLiteral(i)
			segments = append(segments, segment{literal: true, text: "$"})
			i += 2
			literalStart = i
		case '{':
			close := indexByte(template, '}', i+2)
			if close < 0 {
				return nil, &SyntaxError{Name: name, Position: i, Template: template}
			}
			refName := template[i+2 : close]
			if !isValidName(refName) {
				return nil, &SyntaxError{Name: name, Position: i, Template: template}
			}
			flushLiteral(i)
			segments = append(segments, segment{literal: false, refName: refName})
			i = close + 1
			literalStart = i
		default:
			return nil, &SyntaxError{Name: name, Position: i, Template: template}
		}
	}
	flushLiteral(len(template))
	return segments, nil
}

func indexByte(s string, b byte, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// splitEnvEntry splits a conventional "KEY=VALUE" environment entry. Values
// may contain further '=' characters.
func splitEnvEntry(entry string) (string, string, bool) {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i], entry[i+1:], true
		}
	}
	return "", "", false
}
