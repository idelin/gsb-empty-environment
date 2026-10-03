package decor

import "fmt"

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString
	tSymbol
)

type token struct {
	kind tokKind
	text string
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isLetter(c):
			j := i
			for j < len(src) && (isLetter(src[j]) || isDigit(src[j])) {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j]})
			i = j
		case isDigit(c):
			j := i
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tNumber, src[i:j]})
			i = j
		case c == '\'':
			j := i + 1
			for j < len(src) && src[j] != '\'' {
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{tString, src[i+1 : j]})
			i = j + 1
		case c == '<' || c == '>' || c == '!' || c == '=':
			if i+1 < len(src) && (src[i+1] == '=' || (c == '<' && src[i+1] == '>')) {
				toks = append(toks, token{tSymbol, src[i : i+2]})
				i += 2
			} else if c == '!' {
				return nil, fmt.Errorf("unexpected character '!'")
			} else {
				toks = append(toks, token{tSymbol, string(c)})
				i++
			}
		case c == '(' || c == ')' || c == ',' || c == '.' || c == '*':
			toks = append(toks, token{tSymbol, string(c)})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	toks = append(toks, token{tEOF, ""})
	return toks, nil
}
