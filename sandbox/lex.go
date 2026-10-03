package sandbox

import (
	"fmt"
	"strings"
)

type tokKind int

const (
	tokEOF tokKind = iota
	tokNumber
	tokString
	tokIdent
	tokOp
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
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
		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			j := i
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tokNumber, src[i:j], i})
			i = j
		case c == '"' || c == '\'':
			j := i + 1
			var sb strings.Builder
			for j < len(src) && src[j] != c {
				if src[j] == '\\' && j+1 < len(src) {
					j++
					switch src[j] {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					default:
						sb.WriteByte(src[j])
					}
					j++
					continue
				}
				sb.WriteByte(src[j])
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("unterminated string literal at position %d", i)
			}
			toks = append(toks, token{tokString, sb.String(), i})
			i = j + 1
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, token{tokIdent, src[i:j], i})
			i = j
		case c == '(':
			toks = append(toks, token{tokLParen, "(", i})
			i++
		case c == ')':
			toks = append(toks, token{tokRParen, ")", i})
			i++
		case c == ',':
			toks = append(toks, token{tokComma, ",", i})
			i++
		default:
			if i+1 < len(src) {
				two := src[i : i+2]
				switch two {
				case "&&", "||", "==", "!=", "<=", ">=":
					toks = append(toks, token{tokOp, two, i})
					i += 2
					continue
				}
			}
			switch c {
			case '+', '-', '*', '/', '%', '<', '>', '!':
				toks = append(toks, token{tokOp, string(c), i})
				i++
			default:
				return nil, fmt.Errorf("unexpected character %q at position %d", c, i)
			}
		}
	}
	toks = append(toks, token{tokEOF, "", len(src)})
	return toks, nil
}
