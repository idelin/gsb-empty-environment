package configcomplete

import "fmt"

// TokenCategory classifies tokens of the configuration language. The
// categories CatStringChar, CatEscape, CatEscapeHex and CatStringEnd are
// not produced by the lexer itself; they describe positions inside a
// string literal and appear in completion results when the cursor sits
// inside a string.
type TokenCategory int

const (
	CatLBrace TokenCategory = iota
	CatRBrace
	CatLBracket
	CatRBracket
	CatColon
	CatComma
	CatString
	CatNumber
	CatTrue
	CatFalse
	CatNull
	CatStringChar
	CatEscape
	CatEscapeHex
	CatStringEnd
)

func (c TokenCategory) String() string {
	switch c {
	case CatLBrace:
		return "'{'"
	case CatRBrace:
		return "'}'"
	case CatLBracket:
		return "'['"
	case CatRBracket:
		return "']'"
	case CatColon:
		return "':'"
	case CatComma:
		return "','"
	case CatString:
		return "string"
	case CatNumber:
		return "number"
	case CatTrue:
		return "true"
	case CatFalse:
		return "false"
	case CatNull:
		return "null"
	case CatStringChar:
		return "string-char"
	case CatEscape:
		return "escape"
	case CatEscapeHex:
		return "escape-hex"
	case CatStringEnd:
		return "string-end"
	}
	return "unknown"
}

type token struct {
	cat   TokenCategory
	start int
	end   int
}

type strState int

const (
	strNormal strState = iota
	strEscape
	strUnicode
)

type numState int

const (
	numAfterMinus numState = iota
	numAfterDot
	numAfterExp
	numAfterExpSign
)

// partialToken describes an incomplete token at the end of the input: the
// bytes so far are a strict prefix of some valid token and can be completed
// by appending more bytes.
type partialToken struct {
	cat     TokenCategory
	start   int
	end     int
	str     strState
	hexHave int
	num     numState
}

type lexResult struct {
	tokens   []token
	partial  *partialToken
	conflict int
	msg      string
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// lex scans text and returns the complete tokens, at most one partial
// token at the end of input, or the position of the earliest byte where
// the text stops being a prefix of any valid document.
func lex(text string) lexResult {
	res := lexResult{conflict: -1}
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '{':
			res.tokens = append(res.tokens, token{CatLBrace, i, i + 1})
			i++
		case c == '}':
			res.tokens = append(res.tokens, token{CatRBrace, i, i + 1})
			i++
		case c == '[':
			res.tokens = append(res.tokens, token{CatLBracket, i, i + 1})
			i++
		case c == ']':
			res.tokens = append(res.tokens, token{CatRBracket, i, i + 1})
			i++
		case c == ':':
			res.tokens = append(res.tokens, token{CatColon, i, i + 1})
			i++
		case c == ',':
			res.tokens = append(res.tokens, token{CatComma, i, i + 1})
			i++
		case c == '"':
			i = lexString(text, i, &res)
		case c == '-' || isDigit(c):
			i = lexNumber(text, i, &res)
		case c == 't':
			i = lexKeyword(text, i, "true", CatTrue, &res)
		case c == 'f':
			i = lexKeyword(text, i, "false", CatFalse, &res)
		case c == 'n':
			i = lexKeyword(text, i, "null", CatNull, &res)
		default:
			res.conflict = i
			res.msg = fmt.Sprintf("unexpected character %q", c)
		}
		if res.conflict >= 0 || res.partial != nil {
			break
		}
	}
	return res
}

func lexString(text string, start int, res *lexResult) int {
	i := start + 1
	for i < len(text) {
		c := text[i]
		switch {
		case c == '"':
			res.tokens = append(res.tokens, token{CatString, start, i + 1})
			return i + 1
		case c == '\\':
			if i+1 >= len(text) {
				res.partial = &partialToken{cat: CatString, start: start, end: len(text), str: strEscape}
				return len(text)
			}
			e := text[i+1]
			switch e {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
				continue
			case 'u':
				j := i + 2
				have := 0
				for have < 4 && j < len(text) && isHex(text[j]) {
					j++
					have++
				}
				if have == 4 {
					i = j
					continue
				}
				if j >= len(text) {
					res.partial = &partialToken{cat: CatString, start: start, end: len(text), str: strUnicode, hexHave: have}
					return len(text)
				}
				res.conflict = j
				res.msg = "invalid hex digit in \\u escape"
				return j
			}
			res.conflict = i + 1
			res.msg = fmt.Sprintf("invalid escape sequence \\%c", e)
			return i + 1
		case c < 0x20:
			res.conflict = i
			res.msg = "unescaped control character in string"
			return i
		default:
			i++
		}
	}
	res.partial = &partialToken{cat: CatString, start: start, end: len(text), str: strNormal}
	return len(text)
}

func lexNumber(text string, start int, res *lexResult) int {
	i := start
	if text[i] == '-' {
		i++
		if i >= len(text) {
			res.partial = &partialToken{cat: CatNumber, start: start, end: i, num: numAfterMinus}
			return i
		}
		if !isDigit(text[i]) {
			res.conflict = i
			res.msg = "expected digit after '-'"
			return i
		}
	}
	if text[i] == '0' {
		i++
	} else {
		for i < len(text) && isDigit(text[i]) {
			i++
		}
	}
	if i < len(text) && text[i] == '.' {
		i++
		if i >= len(text) {
			res.partial = &partialToken{cat: CatNumber, start: start, end: i, num: numAfterDot}
			return i
		}
		if !isDigit(text[i]) {
			res.conflict = i
			res.msg = "expected digit after '.'"
			return i
		}
		for i < len(text) && isDigit(text[i]) {
			i++
		}
	}
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i >= len(text) {
			res.partial = &partialToken{cat: CatNumber, start: start, end: i, num: numAfterExp}
			return i
		}
		if text[i] == '+' || text[i] == '-' {
			i++
			if i >= len(text) {
				res.partial = &partialToken{cat: CatNumber, start: start, end: i, num: numAfterExpSign}
				return i
			}
		}
		if !isDigit(text[i]) {
			res.conflict = i
			res.msg = "expected digit in exponent"
			return i
		}
		for i < len(text) && isDigit(text[i]) {
			i++
		}
	}
	res.tokens = append(res.tokens, token{CatNumber, start, i})
	return i
}

func lexKeyword(text string, start int, word string, cat TokenCategory, res *lexResult) int {
	i := start
	for i < len(text) && i-start < len(word) && text[i] == word[i-start] {
		i++
	}
	if i-start == len(word) {
		res.tokens = append(res.tokens, token{cat, start, i})
		return i
	}
	if i >= len(text) {
		res.partial = &partialToken{cat: cat, start: start, end: i}
		return i
	}
	res.conflict = i
	res.msg = fmt.Sprintf("invalid keyword, expected %q", word)
	return i
}
