package configcomplete

// Byte-oriented lexer for the configuration language (JSON grammar).
//
// The lexer works on raw bytes so that a cursor position always refers to
// original source bytes, even inside string literals (an escape sequence
// such as \n occupies two bytes, a multi-byte UTF-8 rune occupies several).
//
// Lexing a prefix can end in three ways:
//   - an "open" token: the text ends in the middle of a token that could
//     still be extended (unterminated string, partial keyword, number).
//   - a lexical error at the exact byte offset of the conflict.
//   - cleanly, at a token boundary.

type tokKind int

const (
	kLBrace tokKind = iota
	kRBrace
	kLBracket
	kRBracket
	kColon
	kComma
	kString
	kNumber
	kTrue
	kFalse
	kNull
)

type token struct {
	kind       tokKind
	start, end int // byte offsets, end exclusive
}

type openKind int

const (
	openString        openKind = iota // unterminated string, ready for content
	openStringEscape                  // string ends right after a backslash
	openStringUnicode                 // string inside \uXXXX, hexNeed digits left
	openNumber                        // number at end of input
	openKeyword                       // proper prefix of true/false/null
)

// openTok describes an incomplete token at the end of the input.
type openTok struct {
	kind      openKind
	start     int     // byte offset where the token begins
	needDigit bool    // openNumber: next byte must be a digit (after -, ., e, e+)
	hexNeed   int     // openStringUnicode: hex digits still required
	kw        tokKind // openKeyword: kTrue, kFalse or kNull
	remain    string  // openKeyword: bytes still needed to finish the keyword
}

type lexError struct {
	off int
	msg string
}

func isWS(c byte) bool    { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// lex tokenizes src completely. Exactly one of open or lerr may be non-nil;
// when lerr is non-nil, toks holds every complete token before the error.
func lex(src string) (toks []token, open *openTok, lerr *lexError) {
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case isWS(c):
			i++
		case c == '{':
			toks = append(toks, token{kLBrace, i, i + 1})
			i++
		case c == '}':
			toks = append(toks, token{kRBrace, i, i + 1})
			i++
		case c == '[':
			toks = append(toks, token{kLBracket, i, i + 1})
			i++
		case c == ']':
			toks = append(toks, token{kRBracket, i, i + 1})
			i++
		case c == ':':
			toks = append(toks, token{kColon, i, i + 1})
			i++
		case c == ',':
			toks = append(toks, token{kComma, i, i + 1})
			i++
		case c == '"':
			tok, o, e, next := lexString(src, i)
			if e != nil {
				return toks, nil, e
			}
			if o != nil {
				return toks, o, nil
			}
			toks = append(toks, tok)
			i = next
		case c == '-' || isDigit(c):
			tok, o, e, next := lexNumber(src, i)
			if e != nil {
				return toks, nil, e
			}
			if o != nil {
				return toks, o, nil
			}
			toks = append(toks, tok)
			i = next
		case c == 't':
			tok, o, e, next := lexKeyword(src, i, "true", kTrue)
			if e != nil {
				return toks, nil, e
			}
			if o != nil {
				return toks, o, nil
			}
			toks = append(toks, tok)
			i = next
		case c == 'f':
			tok, o, e, next := lexKeyword(src, i, "false", kFalse)
			if e != nil {
				return toks, nil, e
			}
			if o != nil {
				return toks, o, nil
			}
			toks = append(toks, tok)
			i = next
		case c == 'n':
			tok, o, e, next := lexKeyword(src, i, "null", kNull)
			if e != nil {
				return toks, nil, e
			}
			if o != nil {
				return toks, o, nil
			}
			toks = append(toks, tok)
			i = next
		default:
			return toks, nil, &lexError{i, "unexpected character"}
		}
	}
	return toks, nil, nil
}

func lexKeyword(src string, i int, kw string, kind tokKind) (token, *openTok, *lexError, int) {
	for j := 0; j < len(kw); j++ {
		if i+j >= len(src) {
			return token{}, &openTok{kind: openKeyword, start: i, kw: kind, remain: kw[j:]}, nil, 0
		}
		if src[i+j] != kw[j] {
			return token{}, nil, &lexError{i + j, "invalid keyword"}, 0
		}
	}
	return token{kind, i, i + len(kw)}, nil, nil, i + len(kw)
}

func lexString(src string, i int) (token, *openTok, *lexError, int) {
	j := i + 1
	for {
		if j >= len(src) {
			return token{}, &openTok{kind: openString, start: i}, nil, 0
		}
		c := src[j]
		switch {
		case c == '"':
			return token{kString, i, j + 1}, nil, nil, j + 1
		case c == '\\':
			j++
			if j >= len(src) {
				return token{}, &openTok{kind: openStringEscape, start: i}, nil, 0
			}
			switch e := src[j]; e {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				j++
			case 'u':
				j++
				need := 4
				for need > 0 {
					if j >= len(src) {
						return token{}, &openTok{kind: openStringUnicode, start: i, hexNeed: need}, nil, 0
					}
					if !isHex(src[j]) {
						return token{}, nil, &lexError{j, "invalid unicode escape"}, 0
					}
					j++
					need--
				}
			default:
				return token{}, nil, &lexError{j, "invalid escape sequence"}, 0
			}
		case c < 0x20:
			return token{}, nil, &lexError{j, "control character in string"}, 0
		default:
			// Ordinary content byte, including bytes >= 0x80: strings are
			// scanned byte-wise so a cursor may split a multi-byte rune.
			j++
		}
	}
}

func lexNumber(src string, i int) (token, *openTok, *lexError, int) {
	j := i
	if src[j] == '-' {
		j++
	}
	// Integer part: 0 | [1-9][0-9]*
	if j >= len(src) {
		return token{}, &openTok{kind: openNumber, start: i, needDigit: true}, nil, 0
	}
	if src[j] == '0' {
		j++
	} else if src[j] >= '1' && src[j] <= '9' {
		for j < len(src) && isDigit(src[j]) {
			j++
		}
	} else {
		return token{}, nil, &lexError{j, "digit expected"}, 0
	}
	// Fraction: . [0-9]+
	if j < len(src) && src[j] == '.' {
		j++
		if j >= len(src) {
			return token{}, &openTok{kind: openNumber, start: i, needDigit: true}, nil, 0
		}
		if !isDigit(src[j]) {
			return token{}, nil, &lexError{j, "digit expected after decimal point"}, 0
		}
		for j < len(src) && isDigit(src[j]) {
			j++
		}
	}
	// Exponent: [eE] [+-]? [0-9]+
	if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
		j++
		if j < len(src) && (src[j] == '+' || src[j] == '-') {
			j++
		}
		if j >= len(src) {
			return token{}, &openTok{kind: openNumber, start: i, needDigit: true}, nil, 0
		}
		if !isDigit(src[j]) {
			return token{}, nil, &lexError{j, "digit expected in exponent"}, 0
		}
		for j < len(src) && isDigit(src[j]) {
			j++
		}
	}
	if j >= len(src) {
		// A complete number at end of input may still be extended.
		return token{}, &openTok{kind: openNumber, start: i}, nil, 0
	}
	return token{kNumber, i, j}, nil, nil, j
}
