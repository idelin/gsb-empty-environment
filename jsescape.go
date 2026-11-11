package ctxescape

import (
	"strings"
	"unicode/utf8"
)

// jsStrReplacementTable mirrors html/template's jsStrReplacementTable:
// quotes, HTML specials and control bytes are encoded so the result cannot
// leave the enclosing string literal or close the <script> raw-text element.
var jsStrReplacementTable = [256]string{
	0:    `\u0000`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\u000b`,
	'\f': `\f`,
	'\r': `\r`,
	'"':  `\u0022`,
	'`':  `\u0060`,
	'&':  `\u0026`,
	'\'': `\u0027`,
	'+':  `\u002b`,
	'/':  `\/`,
	'<':  `\u003c`,
	'>':  `\u003e`,
	'\\': `\\`,
}

// jsTmplReplacementTable additionally neutralizes the template-literal
// interpolation tokens "$", "{" and "}".
var jsTmplReplacementTable = jsStrReplacementTable

func init() {
	jsTmplReplacementTable['$'] = `\u0024`
	jsTmplReplacementTable['{'] = `\u007b`
	jsTmplReplacementTable['}'] = `\u007d`
}

// escapeJSString escapes data placed between quotes in JavaScript source,
// including inside <script> and in event-handler attributes.
func escapeJSString(s string) string {
	return replaceJS(s, jsStrReplacementTable[:])
}

// escapeJSTmplLiteral escapes data placed inside a backtick template literal.
func escapeJSTmplLiteral(s string) string {
	return replaceJS(s, jsTmplReplacementTable[:])
}

func replaceJS(s string, table []string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteString(`\ufffd`)
			i++
		case r < 256 && table[r] != "":
			b.WriteString(table[r])
			i += size
		case r == 0x2028:
			b.WriteString(`\u2028`)
			i += size
		case r == 0x2029:
			b.WriteString(`\u2029`)
			i += size
		default:
			b.WriteRune(r)
			i += size
		}
	}
	return b.String()
}
