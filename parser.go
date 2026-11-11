package ctxescape

import "bytes"

func (w *Writer) stepTag() (int, error) {
	for i, c := range w.pending {
		if isHTMLSpace(c) {
			continue
		}
		if c == '>' {
			w.enterElementBody()
			return i + 1, nil
		}
		if c == '/' && i == len(w.pending)-1 {
			return i, nil
		}
		// Attribute name starts here.
		w.nameBuf = w.nameBuf[:0]
		w.nameBuf = append(w.nameBuf, c)
		w.st = stateAttrName
		return i + 1, nil
	}
	// Only whitespace buffered: emit it; more attribute bytes may follow.
	return len(w.pending), nil
}

func (w *Writer) stepAttrName() (int, error) {
	i := 0
	for i < len(w.pending) {
		c := w.pending[i]
		switch {
		case isHTMLSpace(c):
			w.commitAttrName()
			w.st = stateAfterName
			return i, nil
		case c == '=':
			w.commitAttrName()
			w.st = stateBeforeValue
			return i + 1, nil
		case c == '>':
			w.commitAttrName()
			w.enterElementBody()
			return i + 1, nil
		case c == '/':
			if i == len(w.pending)-1 {
				return 0, nil
			}
			if w.pending[i+1] == '>' {
				w.st = stateText
				w.elem = elemGeneric
				return i + 2, nil
			}
		default:
			w.nameBuf = append(w.nameBuf, c)
		}
		i++
	}
	return i, nil
}

func (w *Writer) stepAfterName() (int, error) {
	for i, c := range w.pending {
		if isHTMLSpace(c) {
			continue
		}
		if c == '=' {
			w.st = stateBeforeValue
			return i + 1, nil
		}
		if c == '>' {
			w.enterElementBody()
			return i + 1, nil
		}
		if c == '/' && i == len(w.pending)-1 {
			return i, nil
		}
		// A new attribute begins.
		w.nameBuf = w.nameBuf[:0]
		w.nameBuf = append(w.nameBuf, c)
		w.attr = attrPlain
		w.st = stateAttrName
		return i + 1, nil
	}
	return len(w.pending), nil
}

func (w *Writer) stepBeforeValue() (int, error) {
	for i := 0; i < len(w.pending); i++ {
		c := w.pending[i]
		if isHTMLSpace(c) {
			continue
		}
		switch c {
		case '"', '\'':
			w.quote = c
			w.st = w.quotedValueState()
			return i + 1, nil
		case '>':
			// Valueless attribute; '>' ends the tag.
			w.enterElementBody()
			return i + 1, nil
		case '/':
			if i == len(w.pending)-1 {
				return i, nil
			}
			if w.pending[i+1] == '>' {
				w.st = stateText
				w.elem = elemGeneric
				w.elemName = ""
				return i + 2, nil
			}
			w.quote = 0
			w.st = w.unquotedValueState()
			return i, nil
		default:
			w.quote = 0
			w.st = w.unquotedValueState()
			return i + 1, nil
		}
	}
	// Only leading whitespace is buffered; keep it pending so the value
	// context is entered atomically when the first value byte arrives.
	return 0, nil
}

func (w *Writer) quotedValueState() state {
	switch w.attr {
	case attrURL:
		return stateURLValueQ
	case attrJS:
		return stateJSAttrQ
	case attrStyle:
		return stateStyleQ
	default:
		return stateAttrValueQ
	}
}

func (w *Writer) unquotedValueState() state {
	switch w.attr {
	case attrURL:
		return stateURLValueU
	case attrJS:
		return stateJSAttrU
	case attrStyle:
		return stateStyleU
	default:
		return stateAttrValueU
	}
}

func (w *Writer) stepQuotedValue() (int, error) {
	quote := w.quote
	for i := 0; i < len(w.pending); i++ {
		c := w.pending[i]
		if c == quote {
			// Closing quote: stay inside the tag, ready for another
			// attribute or the terminating '>'.
			w.st = stateAfterName
			w.attr = attrPlain
			w.quote = 0
			return i + 1, nil
		}
	}
	// A quote byte anywhere in the buffer is matched above, so once we get
	// here the remaining bytes are all ordinary value content.
	return len(w.pending), nil
}

func (w *Writer) stepUnquotedValue() (int, error) {
	for i, c := range w.pending {
		if isHTMLSpace(c) {
			w.st = stateTag
			w.attr = attrPlain
			w.quote = 0
			return i + 1, nil
		}
		if c == '>' {
			// '>' ends the opening tag; no raw-text body is entered here.
			w.st = stateText
			w.elem = elemGeneric
			w.elemName = ""
			w.attr = attrPlain
			w.quote = 0
			return i + 1, nil
		}
	}
	// Retain a trailing '/' or '>' prefix so a chunk boundary cannot hide a
	// tag terminator; the rest is ordinary unquoted value content.
	last := w.pending[len(w.pending)-1]
	if last == '/' || last == '>' {
		return len(w.pending) - 1, nil
	}
	return len(w.pending), nil
}

// stepUntil consumes bytes up to and including a fixed terminator such as
// "-->" (comment) or "]]>" (CDATA). A buffer that is itself a prefix of the
// terminator is held back until more bytes arrive.
func (w *Writer) stepUntil(term string) (int, error) {
	if i := bytes.Index(w.pending, []byte(term)); i >= 0 {
		w.st = stateText
		return i + len(term), nil
	}
	keep := longestSuffixPrefix(w.pending, term)
	n := len(w.pending) - keep
	if n == 0 {
		return 0, nil
	}
	return n, nil
}

// stepDecl consumes a "<!..." declaration terminated by '>'.
func (w *Writer) stepDecl() (int, error) {
	if i := bytes.IndexByte(w.pending, '>'); i >= 0 {
		w.st = stateText
		return i + 1, nil
	}
	return 0, nil
}

// stepRawText handles the bodies of <script>, <style>, <textarea> and
// <title>. The HTML tokenizer leaves raw-text mode only on the matching
// "</tagname" end marker, which is located case-insensitively and must be
// followed by whitespace, '/' or '>'.
func (w *Writer) stepRawText() (int, error) {
	marker := w.rawEndMarker()
	if i := indexTagEnd(w.pending, marker); i >= 0 {
		w.st = stateText
		w.elem = elemGeneric
		w.elemName = ""
		return i, nil
	}
	if at := rawTextEndPrefix(w.pending, marker); at >= 0 {
		if at == 0 {
			return 0, nil
		}
		return at, nil
	}
	return len(w.pending), nil
}

// rawTextEndPrefix returns the offset of a partial or complete
// case-insensitive "</name" end marker in s that is still waiting for its
// closing '>'. It returns -1 when no marker needs to be retained.
func rawTextEndPrefix(s []byte, name string) int {
	marker := "</" + name
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != '<' {
			continue
		}
		n := len(s) - i
		if n <= len(marker) {
			if bytes.EqualFold(s[i:], []byte(marker[:n])) {
				return i
			}
			return -1
		}
		if !bytes.EqualFold(s[i:i+len(marker)], []byte(marker)) {
			return -1
		}
		for j := i + len(marker); j < len(s); j++ {
			if s[j] == '>' {
				return -1 // complete end tag; indexTagEnd consumes it
			}
			if !isTagEndSeparator(s[j]) {
				return -1
			}
		}
		return i
	}
	return -1
}

func (w *Writer) rawEndMarker() string {
	switch w.elem {
	case elemScript:
		return "script"
	case elemStyle:
		return "style"
	case elemRCDATA:
		return w.elemName
	default:
		return ""
	}
}

// indexTagEnd finds the earliest "</name" followed by an HTML tag-end
// separator, case-insensitively, or returns -1.
func indexTagEnd(s []byte, name string) int {
	marker := "</" + name
	for i := 0; i+len(marker) <= len(s); i++ {
		if !bytes.EqualFold(s[i:i+len(marker)], []byte(marker)) {
			continue
		}
		j := i + len(marker)
		if j == len(s) {
			// Marker name complete but no separator byte is buffered yet.
			return -1
		}
		if !isTagEndSeparator(s[j]) {
			continue
		}
		for ; j < len(s); j++ {
			if s[j] == '>' {
				return j + 1
			}
			if !isTagEndSeparator(s[j]) {
				break
			}
		}
	}
	return -1
}

func isTagEndSeparator(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r', '/', '>':
		return true
	}
	return false
}

// longestSuffixPrefix returns how many trailing bytes of s equal a prefix of
// term, so that an end marker split across chunks is not flushed early.
func longestSuffixPrefix(s []byte, term string) int {
	max := len(term)
	if max > len(s) {
		max = len(s)
	}
	for k := max; k > 0; k-- {
		if bytes.EqualFold(s[len(s)-k:], []byte(term[:k])) {
			return k
		}
	}
	return 0
}

// longestSuffixPrefixPos returns the byte offset at which the longest trailing
// prefix of term begins, or -1 when there is no such prefix.
func longestSuffixPrefixPos(s []byte, term string) int {
	k := longestSuffixPrefix(s, term)
	if k == 0 {
		return -1
	}
	return len(s) - k
}
