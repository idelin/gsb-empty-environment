package pretty

import (
	"fmt"
	"strconv"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tNumber
	tString
	tIdent
	tOp
	tLParen
	tRParen
	tComma
)

type token struct {
	kind tokKind
	text string // literal text for Number/Ident/Op; unquoted value for String
	pos  int
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// lex tokenizes the input source.
func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isDigit(c):
			j := i
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tNumber, src[i:j], i})
			i = j
		case isAlpha(c):
			j := i
			for j < len(src) && (isAlpha(src[j]) || isDigit(src[j])) {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j], i})
			i = j
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string at %d", i)
			}
			v, err := strconv.Unquote(src[i : j+1])
			if err != nil {
				return nil, fmt.Errorf("bad string at %d: %v", i, err)
			}
			toks = append(toks, token{tString, v, i})
			i = j + 1
		case c == '(':
			toks = append(toks, token{tLParen, "(", i})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")", i})
			i++
		case c == ',':
			toks = append(toks, token{tComma, ",", i})
			i++
		case strings.IndexByte("+-*/%<>=!&|", c) >= 0:
			j := i + 1
			if j < len(src) {
				two := src[i : j+1]
				switch two {
				case "&&", "||", "==", "!=", "<=", ">=":
					j++
				}
			}
			toks = append(toks, token{tOp, src[i:j], i})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at %d", c, i)
		}
	}
	toks = append(toks, token{tEOF, "", len(src)})
	return toks, nil
}
