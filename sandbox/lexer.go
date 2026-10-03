package sandbox

import "fmt"

type tokenKind int

const (
	tEOF tokenKind = iota
	tNumber
	tString
	tIdent
	tOp
	tLParen
	tRParen
	tComma
)

type token struct {
	kind tokenKind
	lit  string
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			j := i
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tNumber, src[i:j]})
			i = j
		case isAlpha(c):
			j := i
			for j < len(src) && (isAlpha(src[j]) || isDigit(src[j])) {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j]})
			i = j
		case c == '"' || c == '\'':
			quote := c
			j := i + 1
			var buf []byte
			closed := false
			for j < len(src) {
				if src[j] == '\\' && j+1 < len(src) {
					switch src[j+1] {
					case 'n':
						buf = append(buf, '\n')
					case 't':
						buf = append(buf, '\t')
					case 'r':
						buf = append(buf, '\r')
					default:
						buf = append(buf, src[j+1])
					}
					j += 2
					continue
				}
				if src[j] == quote {
					closed = true
					break
				}
				buf = append(buf, src[j])
				j++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unterminated string literal", ErrValidation)
			}
			toks = append(toks, token{tString, string(buf)})
			i = j + 1
		case c == '(':
			toks = append(toks, token{tLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")"})
			i++
		case c == ',':
			toks = append(toks, token{tComma, ","})
			i++
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "&&", "||", "==", "!=", "<=", ">=":
				toks = append(toks, token{tOp, two})
				i += 2
				continue
			}
			switch c {
			case '+', '-', '*', '/', '%', '<', '>', '!':
				toks = append(toks, token{tOp, string(c)})
				i++
			default:
				return nil, fmt.Errorf("%w: unexpected character %q", ErrValidation, string(c))
			}
		}
	}
	toks = append(toks, token{tEOF, ""})
	return toks, nil
}
