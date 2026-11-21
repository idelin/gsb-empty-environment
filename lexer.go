package tenantfilter

import (
	"fmt"
	"strconv"
	"strings"
)

type tokenKind uint8

const (
	tokSpace tokenKind = iota
	tokComment
	tokString
	tokQuotedIdent
	tokNumber
	tokWord
	tokSymbol
	tokPlaceholder
)

type token struct {
	kind  tokenKind
	text  string
	lower string
	start int
	end   int
	depth int
	match int
}

func lexSQL(src string) ([]token, error) {
	if strings.IndexByte(src, 0) >= 0 {
		return nil, fmt.Errorf("%w: NUL byte in query", ErrMalformedQuery)
	}

	tokens := make([]token, 0, len(src)/3)
	for i := 0; i < len(src); {
		start := i
		c := src[i]
		switch {
		case isSpace(c):
			for i < len(src) && isSpace(src[i]) {
				i++
			}
			tokens = append(tokens, makeToken(tokSpace, src[start:i], start, i))
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			i += 2
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				i++
			}
			tokens = append(tokens, makeToken(tokComment, src[start:i], start, i))
		case c == '#':
			return nil, fmt.Errorf("%w: '#' comments and operators are dialect-ambiguous", ErrUnsupportedQuery)
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			if i+2 < len(src) && (src[i+2] == '!' || (src[i+2] == 'M' && i+3 < len(src) && src[i+3] == '!')) {
				return nil, fmt.Errorf("%w: executable block comments are not allowed", ErrUnsupportedQuery)
			}
			i += 2
			closed := false
			for i+1 < len(src) {
				if src[i] == '*' && src[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unterminated block comment", ErrMalformedQuery)
			}
			tokens = append(tokens, makeToken(tokComment, src[start:i], start, i))
		case c == '\'':
			var ok bool
			i, ok = scanQuoted(src, i, '\'', true)
			if !ok {
				return nil, fmt.Errorf("%w: unterminated string literal", ErrMalformedQuery)
			}
			tokens = append(tokens, makeToken(tokString, src[start:i], start, i))
		case c == '"':
			var ok bool
			i, ok = scanQuoted(src, i, '"', false)
			if !ok {
				return nil, fmt.Errorf("%w: unterminated quoted identifier", ErrMalformedQuery)
			}
			tokens = append(tokens, makeToken(tokQuotedIdent, src[start:i], start, i))
		case c == '`':
			var ok bool
			i, ok = scanQuoted(src, i, '`', false)
			if !ok {
				return nil, fmt.Errorf("%w: unterminated quoted identifier", ErrMalformedQuery)
			}
			tokens = append(tokens, makeToken(tokQuotedIdent, src[start:i], start, i))
		case c == '[':
			i++
			closed := false
			for i < len(src) {
				if src[i] == ']' {
					if i+1 < len(src) && src[i+1] == ']' {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unterminated bracketed identifier", ErrMalformedQuery)
			}
			tokens = append(tokens, makeToken(tokQuotedIdent, src[start:i], start, i))
		case c == '?':
			i++
			for i < len(src) && isDigit(src[i]) {
				i++
			}
			tokens = append(tokens, makeToken(tokPlaceholder, src[start:i], start, i))
		case c == '$':
			if i+1 < len(src) && isDigit(src[i+1]) {
				i += 2
				for i < len(src) && isDigit(src[i]) {
					i++
				}
				tokens = append(tokens, makeToken(tokPlaceholder, src[start:i], start, i))
				break
			}
			if end, ok := scanDollarQuoted(src, i); ok {
				i = end
				tokens = append(tokens, makeToken(tokString, src[start:i], start, i))
				break
			}
			i++
			tokens = append(tokens, makeToken(tokSymbol, src[start:i], start, i))
		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			i = scanNumber(src, i)
			tokens = append(tokens, makeToken(tokNumber, src[start:i], start, i))
		case isIdentStart(c):
			i++
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			text := src[start:i]
			token := makeToken(tokWord, text, start, i)
			token.lower = strings.ToLower(text)
			tokens = append(tokens, token)
		default:
			i++
			tokens = append(tokens, makeToken(tokSymbol, src[start:i], start, i))
		}
	}

	if err := annotateParens(tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

func makeToken(kind tokenKind, text string, start, end int) token {
	return token{kind: kind, text: text, start: start, end: end, match: -1}
}

func scanQuoted(src string, start int, quote byte, backslash bool) (int, bool) {
	i := start + 1
	for i < len(src) {
		if src[i] == quote {
			if i+1 < len(src) && src[i+1] == quote {
				i += 2
				continue
			}
			return i + 1, true
		}
		if backslash && src[i] == '\\' && i+1 < len(src) {
			i += 2
			continue
		}
		i++
	}
	return i, false
}

func scanDollarQuoted(src string, start int) (int, bool) {
	i := start + 1
	for i < len(src) && (isIdentPart(src[i]) && src[i] != '$') {
		i++
	}
	if i >= len(src) || src[i] != '$' {
		return 0, false
	}
	delimiter := src[start : i+1]
	if end := strings.Index(src[i+1:], delimiter); end >= 0 {
		return i + 1 + end + len(delimiter), true
	}
	return 0, false
}

func scanNumber(src string, start int) int {
	i := start
	if src[i] == '.' {
		i++
	}
	for i < len(src) && isDigit(src[i]) {
		i++
	}
	if i < len(src) && src[i] == '.' {
		i++
		for i < len(src) && isDigit(src[i]) {
			i++
		}
	}
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		j := i + 1
		if j < len(src) && (src[j] == '+' || src[j] == '-') {
			j++
		}
		if j < len(src) && isDigit(src[j]) {
			i = j + 1
			for i < len(src) && isDigit(src[i]) {
				i++
			}
		}
	}
	return i
}

func annotateParens(tokens []token) error {
	depth := 0
	stack := make([]int, 0, 8)
	for i := range tokens {
		switch tokens[i].text {
		case "(":
			tokens[i].depth = depth
			depth++
			stack = append(stack, i)
		case ")":
			if len(stack) == 0 {
				return fmt.Errorf("%w: unmatched closing parenthesis", ErrMalformedQuery)
			}
			depth--
			tokens[i].depth = depth
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			tokens[open].match = i
			tokens[i].match = open
		default:
			tokens[i].depth = depth
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("%w: unmatched opening parenthesis", ErrMalformedQuery)
	}
	return nil
}

func enclosingOpens(tokens []token) []int {
	enclosing := make([]int, len(tokens))
	stack := make([]int, 0, 8)
	for i := range tokens {
		switch tokens[i].text {
		case "(":
			if len(stack) == 0 {
				enclosing[i] = -1
			} else {
				enclosing[i] = stack[len(stack)-1]
			}
			stack = append(stack, i)
		case ")":
			if len(stack) != 0 {
				enclosing[i] = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			} else {
				enclosing[i] = -1
			}
		default:
			if len(stack) == 0 {
				enclosing[i] = -1
			} else {
				enclosing[i] = stack[len(stack)-1]
			}
		}
	}
	return enclosing
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c) || c == '$'
}

func isSignificant(tok token) bool {
	return tok.kind != tokSpace && tok.kind != tokComment
}

func nextSignificant(tokens []token, i, end int) int {
	for i < end && !isSignificant(tokens[i]) {
		i++
	}
	return i
}

func prevSignificant(tokens []token, i, start int) int {
	for i >= start && !isSignificant(tokens[i]) {
		i--
	}
	return i
}

func isWord(tok token, word string) bool {
	return tok.kind == tokWord && tok.lower == word
}

func isIdent(tok token) bool {
	return tok.kind == tokWord || tok.kind == tokQuotedIdent
}

func identifierValue(tok token) string {
	switch tok.kind {
	case tokQuotedIdent:
		return unquoteIdentifier(tok.text)
	default:
		return tok.text
	}
}

func identifierKey(tok token) string {
	if tok.kind == tokQuotedIdent {
		return unquoteIdentifier(tok.text)
	}
	return strings.ToLower(tok.text)
}

func unquoteIdentifier(text string) string {
	if len(text) < 2 {
		return text
	}
	switch text[0] {
	case '"':
		if text[len(text)-1] == '"' {
			return strings.ReplaceAll(text[1:len(text)-1], `""`, `"`)
		}
	case '`':
		if text[len(text)-1] == '`' {
			return strings.ReplaceAll(text[1:len(text)-1], "``", "`")
		}
	case '[':
		if text[len(text)-1] == ']' {
			return strings.ReplaceAll(text[1:len(text)-1], "]]", "]")
		}
	}
	return text
}

func placeholderNumber(tok token) (int, bool) {
	if !strings.HasPrefix(tok.text, "$") {
		return 0, false
	}
	number, err := strconv.Atoi(tok.text[1:])
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}
