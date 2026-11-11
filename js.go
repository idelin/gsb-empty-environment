package ctxescape

import "bytes"

// scriptEndPrefix finds the byte offset of a partial "</script" end marker
// that must be retained until its closing '>' arrives. A complete end tag is
// reported by indexTagEnd before this is consulted.
func scriptEndPrefix(s []byte) int {
	return rawTextEndPrefix(s, "script")
}

// stepJSCode scans JavaScript program text. It recognizes string, template
// literal, regexp and comment entry points so interpolation contexts can be
// tracked; the raw-text "</script>" end tag is only honored at statement
// level, never from inside a string or comment.
func (w *Writer) stepJSCode() (int, error) {
	if i := indexTagEnd(w.pending, "script"); i >= 0 {
		w.leaveScript()
		return i, nil
	}
	if at := scriptEndPrefix(w.pending); at > 0 {
		// Emit everything before a partial "</script" marker, retaining it.
		return at, nil
	} else if at == 0 {
		return 0, nil
	}
	for i := 0; i < len(w.pending); i++ {
		c := w.pending[i]
		switch c {
		case '"':
			w.st = stateJSDqStr
			w.jsDivExpected = true
			return i + 1, nil
		case '\'':
			w.st = stateJSSqStr
			w.jsDivExpected = true
			return i + 1, nil
		case '`':
			w.st = stateJSTmpl
			w.jsDivExpected = true
			return i + 1, nil
		case '/':
			if i+1 >= len(w.pending) {
				// Lone trailing '/': decide regexp vs division from the
				// preceding token. A regexp context consumes the slash now;
				// otherwise retain it for the next chunk to disambiguate.
				if jsTokenAllowsRegexp(w.pending[:i]) {
					w.st = stateJSRegexp
					return i + 1, nil
				}
				return i, nil
			}
			switch w.pending[i+1] {
			case '/':
				w.st = stateJSLineCmt
				return i + 2, nil
			case '*':
				w.st = stateJSBlockCmt
				return i + 2, nil
			default:
				if jsTokenAllowsRegexp(w.pending[:i]) {
					w.st = stateJSRegexp
					return i + 1, nil
				}
				// Division operator; keep scanning.
			}
		case '\\':
			if i == len(w.pending)-1 {
				return i, nil
			}
			i++ // skip escaped byte
		case '<':
			// Only "</script" ends the raw-text element. A complete marker
			// is handled above. Any other '<' is ordinary JavaScript source
			// (e.g. a comparison); partial markers are held back below.
			continue
		case '{':
			if len(w.jsBraceDepth) > 0 {
				w.jsBraceDepth[len(w.jsBraceDepth)-1]++
			}
		case '}':
			if n := len(w.jsBraceDepth); n > 0 {
				w.jsBraceDepth[n-1]--
				if w.jsBraceDepth[n-1] < 0 {
					w.jsBraceDepth = w.jsBraceDepth[:n-1]
					w.st = stateJSTmpl
					return i + 1, nil
				}
			}
		}
	}
	if at := scriptEndPrefix(w.pending); at >= 0 {
		return at, nil
	}
	// Update the regexp-vs-division heuristic from the trailing token.
	w.jsDivExpected = jsTokenAllowsRegexp(w.pending)
	return len(w.pending), nil
}

var regexpPrecederKeywords = map[string]bool{
	"break": true, "case": true, "continue": true, "delete": true,
	"do": true, "else": true, "finally": true, "in": true,
	"instanceof": true, "return": true, "throw": true, "try": true,
	"typeof": true, "void": true,
}

// jsTokenAllowsRegexp reports whether a '/' after the given JS code bytes
// begins a regexp literal rather than a division operator. It is a streaming
// approximation of html/template's nextJSCtx, evaluated per chunk.
func jsTokenAllowsRegexp(s []byte) bool {
	trimmed := bytes.TrimRightFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' ||
			r == '\f' || r == '\v' || r == 0xa0 || r == 0xfeff
	})
	if len(trimmed) == 0 {
		return true
	}
	c := trimmed[len(trimmed)-1]
	switch c {
	case '.', ',', '<', '>', '=', '*', '%', '&', '|', '^', '?',
		'!', '~', '(', '[', ':', ';', '{', '}':
		return true
	case '+', '-':
		// A single +/- is a prefix operator; an even run is binary.
		n := 0
		for i := len(trimmed) - 1; i >= 0 && trimmed[i] == c; i-- {
			n++
		}
		return n%2 == 1
	}
	// Identifier or keyword ending.
	end := len(trimmed)
	for end > 0 && isJSIdentPart(rune(trimmed[end-1])) {
		end--
	}
	if end < len(trimmed) {
		return regexpPrecederKeywords[string(trimmed[end:])]
	}
	return false
}

func isJSIdentPart(r rune) bool {
	return r == '$' || r == '_' ||
		r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9'
}

// stepJSString handles both '...' and "..." strings.
func (w *Writer) stepJSString() (int, error) {
	quote := byte('\'')
	if w.st == stateJSDqStr {
		quote = '"'
	}
	// The HTML tokenizer recognizes a raw-text end tag even inside a JS
	// string, because markup tokenization precedes script execution.
	if i := indexTagEnd(w.pending, "script"); i >= 0 {
		w.leaveScript()
		return i, nil
	}
	if at := scriptEndPrefix(w.pending); at > 0 {
		return at, nil
	} else if at == 0 {
		return 0, nil
	}
	for i := 0; i < len(w.pending); i++ {
		switch w.pending[i] {
		case '\\':
			if i == len(w.pending)-1 {
				return i, nil
			}
			i++ // escaped byte is never a delimiter
		case quote:
			w.st = stateJSCode
			w.jsDivExpected = false
			return i + 1, nil
		case '\n', '\r':
			// Unterminated string; the JS is malformed. Return to code
			// context instead of swallowing the rest of the script body.
			w.st = stateJSCode
			w.jsDivExpected = true
			return i + 1, nil
		}
	}
	if at := scriptEndPrefix(w.pending); at >= 0 {
		return at, nil
	}
	return len(w.pending), nil
}

func (w *Writer) leaveScript() {
	w.st = stateText
	w.elem = elemGeneric
	w.elemName = ""
	w.jsBraceDepth = w.jsBraceDepth[:0]
}

// stepJSTmpl handles backtick template literals including ${ ... } nesting.
func (w *Writer) stepJSTmpl() (int, error) {
	if i := indexTagEnd(w.pending, "script"); i >= 0 {
		w.leaveScript()
		return i, nil
	}
	if at := scriptEndPrefix(w.pending); at > 0 {
		return at, nil
	} else if at == 0 {
		return 0, nil
	}
	for i := 0; i < len(w.pending); i++ {
		c := w.pending[i]
		switch c {
		case '\\':
			if i == len(w.pending)-1 {
				return i, nil
			}
			i++
		case '`':
			w.st = stateJSCode
			w.jsDivExpected = false
			return i + 1, nil
		case '$':
			if i+1 < len(w.pending) && w.pending[i+1] == '{' {
				w.jsBraceDepth = append(w.jsBraceDepth, 0)
				w.st = stateJSCode
				return i + 2, nil
			}
		}
	}
	if at := scriptEndPrefix(w.pending); at >= 0 {
		return at, nil
	}
	return len(w.pending), nil
}

// stepJSRegexp handles /.../ regexp literals; escaped slashes and character
// classes (in which '/' is literal) are accounted for.
func (w *Writer) stepJSRegexp() (int, error) {
	if i := indexTagEnd(w.pending, "script"); i >= 0 {
		w.leaveScript()
		return i, nil
	}
	if at := scriptEndPrefix(w.pending); at > 0 {
		return at, nil
	} else if at == 0 {
		return 0, nil
	}
	inClass := false
	for i := 0; i < len(w.pending); i++ {
		c := w.pending[i]
		switch c {
		case '\\':
			if i == len(w.pending)-1 {
				return i, nil
			}
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				w.st = stateJSCode
				w.jsDivExpected = false
				return i + 1, nil
			}
		case '\n', '\r':
			inClass = false
		}
	}
	if at := scriptEndPrefix(w.pending); at >= 0 {
		return at, nil
	}
	return len(w.pending), nil
}

func (w *Writer) stepJSBlockCmt() (int, error) {
	if i := indexTagEnd(w.pending, "script"); i >= 0 {
		w.leaveScript()
		return i, nil
	}
	if at := scriptEndPrefix(w.pending); at > 0 {
		return at, nil
	} else if at == 0 {
		return 0, nil
	}
	if i := bytes.Index(w.pending, []byte("*/")); i >= 0 {
		w.st = stateJSCode
		return i + 2, nil
	}
	atBlock := longestSuffixPrefixPos(w.pending, "*/")
	atScript := scriptEndPrefix(w.pending)
	at := atBlock
	if atScript >= 0 && (at < 0 || atScript < at) {
		at = atScript
	}
	if at >= 0 {
		return at, nil
	}
	return len(w.pending), nil
}

func (w *Writer) stepJSLineCmt() (int, error) {
	if i := indexTagEnd(w.pending, "script"); i >= 0 {
		w.leaveScript()
		return i, nil
	}
	if at := scriptEndPrefix(w.pending); at > 0 {
		return at, nil
	} else if at == 0 {
		return 0, nil
	}
	for i, c := range w.pending {
		if c == '\n' {
			w.st = stateJSCode
			return i + 1, nil
		}
	}
	if at := scriptEndPrefix(w.pending); at >= 0 {
		return at, nil
	}
	return len(w.pending), nil
}
