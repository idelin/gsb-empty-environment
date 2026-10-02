package exprfmt

import "fmt"

type tokKind int

const (
	tEOF tokKind = iota
	tNum
	tStr
	tIdent
	tOp
	tLParen
	tRParen
	tComma
)

type token struct {
	kind tokKind
	text string
	pos  int
}

var twoCharOps = map[string]bool{
	"||": true, "&&": true, "==": true, "!=": true, "<=": true, ">=": true,
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

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
			toks = append(toks, token{tNum, src[i:j], i})
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
			closed := false
			for j < len(src) {
				if src[j] == '\\' {
					j += 2
					continue
				}
				if src[j] == '"' {
					closed = true
					j++
					break
				}
				j++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string at offset %d", i)
			}
			toks = append(toks, token{tStr, src[i:j], i})
			i = j
		case c == '(':
			toks = append(toks, token{tLParen, "(", i})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")", i})
			i++
		case c == ',':
			toks = append(toks, token{tComma, ",", i})
			i++
		default:
			if i+1 < len(src) && twoCharOps[src[i:i+2]] {
				toks = append(toks, token{tOp, src[i : i+2], i})
				i += 2
				continue
			}
			switch c {
			case '+', '-', '*', '/', '%', '<', '>', '!':
				toks = append(toks, token{tOp, string(c), i})
				i++
			default:
				return nil, fmt.Errorf("unexpected character %q at offset %d", c, i)
			}
		}
	}
	toks = append(toks, token{tEOF, "", len(src)})
	return toks, nil
}
