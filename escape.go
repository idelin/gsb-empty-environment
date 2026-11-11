package ctxescape

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// escape renders untrusted text for the current parse state. Every supported
// state maps to a context-specific escaping rule; every unsupported state is
// rejected rather than approximated.
func (w *Writer) escape(s string) (string, error) {
	st := w.st
	if st == stateBeforeValue {
		// Interpolation immediately after "=" with no buffered value byte:
		// the value cannot be quoted, so it is unquoted.
		st = w.unquotedValueState()
	}
	switch st {
	case stateText:
		return escapeHTMLText(s), nil
	case stateAttrValueQ:
		return escapeAttrQuoted(s), nil
	case stateAttrValueU:
		return escapeAttrUnquoted(s), nil
	case stateURLValueQ:
		if err := checkSafeURL(s); err != nil {
			return "", err
		}
		return escapeAttrQuoted(s), nil
	case stateURLValueU:
		if err := checkSafeURL(s); err != nil {
			return "", err
		}
		return escapeAttrUnquoted(s), nil
	case stateJSAttrQ:
		return escapeJSString(s), nil
	case stateJSAttrU:
		// An unquoted event handler needs both JS escaping and HTML
		// no-space escaping, in that order.
		return escapeAttrUnquoted(escapeJSString(s)), nil
	case stateJSDqStr, stateJSSqStr:
		return escapeJSString(s), nil
	case stateJSTmpl:
		return escapeJSTmplLiteral(s), nil
	case stateRawText:
		switch w.elem {
		case elemRCDATA:
			return escapeHTMLText(s), nil
		case elemStyle:
			return "", fmt.Errorf("%w: cannot interpolate into <style> element body",
				ErrAmbiguousContext)
		}
		return "", fmt.Errorf("%w: cannot interpolate into %s element body",
			ErrAmbiguousContext, w.st)
	case stateTagName, stateTag, stateAttrName, stateAfterName,
		stateBeforeValue, stateStyleQ, stateStyleU, stateHTMLComment,
		stateCDATA, stateDecl, stateJSCode, stateJSRegexp,
		stateJSBlockCmt, stateJSLineCmt:
		return "", fmt.Errorf("%w: interpolation into %s is not supported",
			ErrAmbiguousContext, w.st)
	default:
		return "", fmt.Errorf("%w: unknown context %s",
			ErrAmbiguousContext, w.st)
	}
}

// htmlReplacementTable mirrors html/template's table for HTML text and
// quoted attribute values.
var htmlReplacementTable = [256]string{
	0:    "\uFFFD",
	'"':  "&#34;",
	'&':  "&amp;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'>':  "&gt;",
}

// htmlNospaceReplacementTable covers every byte that can terminate or alter
// an unquoted attribute value, plus the HTML specials.
var htmlNospaceReplacementTable = [256]string{
	0:    "&#xfffd;",
	'\t': "&#9;",
	'\n': "&#10;",
	'\v': "&#11;",
	'\f': "&#12;",
	'\r': "&#13;",
	' ':  "&#32;",
	'"':  "&#34;",
	'&':  "&amp;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'=':  "&#61;",
	'>':  "&gt;",
	'`':  "&#96;",
}

// escapeHTMLText escapes data interpolated between markup tags.
func escapeHTMLText(s string) string {
	var b strings.Builder
	writeEscaped(s, &b, htmlReplacementTable[:], true)
	return b.String()
}

// escapeAttrQuoted escapes data inside a single- or double-quoted attribute.
// Both quote characters are entity-encoded, so the result is safe in either.
func escapeAttrQuoted(s string) string {
	var b strings.Builder
	writeEscaped(s, &b, htmlReplacementTable[:], true)
	return b.String()
}

// escapeAttrUnquoted escapes data in an unquoted attribute value. Bytes that
// are not part of the conservative safe set are encoded as hex entities so
// no whitespace, sign or quote can break out of the value.
func escapeAttrUnquoted(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(htmlNospaceReplacementTable[0])
			i++
			continue
		}
		if r < 256 && htmlNospaceReplacementTable[r] != "" {
			b.WriteString(htmlNospaceReplacementTable[r])
			i += size
			continue
		}
		if r < 256 && isNospaceSafeByte(byte(r)) {
			b.WriteByte(byte(r))
			i += size
			continue
		}
		if r >= 256 && isNonCharacterRune(r) {
			fmt.Fprintf(&b, "&#x%x;", r)
			i += size
			continue
		}
		if r < 256 {
			fmt.Fprintf(&b, "&#x%x;", r)
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func isNospaceSafeByte(c byte) bool {
	if c >= 0x80 {
		return false
	}
	switch {
	case 'a' <= c && c <= 'z':
		return true
	case 'A' <= c && c <= 'Z':
		return true
	case '0' <= c && c <= '9':
		return true
	}
	switch c {
	case '-', '.', '_', '~', ':', '/', '?', '#', '%', ';', ',', '@':
		return true
	}
	return false
}

func isNonCharacterRune(r rune) bool {
	return 0xfdd0 <= r && r <= 0xfdef || 0xfff0 <= r && r <= 0xffff
}

// writeEscaped applies an ASCII replacement table rune by rune, mirroring
// the invalid-UTF-8 and non-character handling of html/template.
func writeEscaped(s string, b *strings.Builder, table []string, badRunes bool) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(table[0])
		} else if int(r) < len(table) && table[r] != "" {
			b.WriteString(table[r])
		} else if !badRunes && isNonCharacterRune(r) {
			fmt.Fprintf(b, "&#x%x;", r)
		} else {
			b.WriteRune(r)
		}
		i += size
	}
}

// checkSafeURL rejects URL attribute values whose scheme is outside the
// navigation allow list. Relative and protocol-relative URLs are accepted;
// leading ASCII whitespace and control characters are ignored, matching the
// HTML5 "valid URL potentially surrounded by spaces" rule.
func checkSafeURL(s string) error {
	trimmed := strings.TrimLeft(s, " \t\n\r\f\v\x00")
	protocol, _, ok := strings.Cut(trimmed, ":")
	if !ok || strings.Contains(protocol, "/") {
		// Relative URL (no scheme component).
		return nil
	}
	// Reject anything with whitespace inside the scheme portion and any
	// non-scheme character that smuggling attempts rely on.
	for i := 0; i < len(protocol); i++ {
		c := protocol[i]
		if isHTMLSpace(c) {
			return fmt.Errorf("%w: %q", ErrUnsafeURL, s)
		}
	}
	switch strings.ToLower(protocol) {
	case "http", "https", "mailto":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsafeURL, s)
	}
}
