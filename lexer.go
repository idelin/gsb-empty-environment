package relalg

import (
	"fmt"
	"strings"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokNumber
	tokString
	tokSymbol
)

type token struct {
	kind tokenKind
	val  string
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// lex 将 SQL 文本切分为 token 序列，末尾追加 tokEOF。
func lex(input string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(input) {
		c := input[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isIdentStart(c):
			j := i + 1
			for j < len(input) && isIdentPart(input[j]) {
				j++
			}
			toks = append(toks, token{tokIdent, input[i:j]})
			i = j
		case isDigit(c):
			j := i + 1
			for j < len(input) && (isDigit(input[j]) || input[j] == '.') {
				j++
			}
			toks = append(toks, token{tokNumber, input[i:j]})
			i = j
		case c == '\'':
			j := i + 1
			var sb strings.Builder
			for {
				if j >= len(input) {
					return nil, fmt.Errorf("unterminated string literal")
				}
				if input[j] == '\'' {
					if j+1 < len(input) && input[j+1] == '\'' {
						sb.WriteByte('\'')
						j += 2
						continue
					}
					j++
					break
				}
				sb.WriteByte(input[j])
				j++
			}
			toks = append(toks, token{tokString, sb.String()})
			i = j
		case c == '>' || c == '<' || c == '=':
			j := i + 1
			if j < len(input) && (input[j] == '=' || (c == '<' && input[j] == '>')) {
				j++
			}
			toks = append(toks, token{tokSymbol, input[i:j]})
			i = j
		case c == '!':
			if i+1 < len(input) && input[i+1] == '=' {
				toks = append(toks, token{tokSymbol, "!="})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected character '!'")
			}
		case strings.ContainsRune(",.*()", rune(c)):
			toks = append(toks, token{tokSymbol, string(c)})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	toks = append(toks, token{tokEOF, ""})
	return toks, nil
}
