package decorrelator

import (
	"fmt"
	"strings"
)

type tokenKind int

const (
	tEOF tokenKind = iota
	tIdent
	tNumber
	tString
	tOp // = < > <= >= != <>
	tComma
	tLParen
	tRParen
	tDot
	tStar
	tSemi
)

type token struct {
	kind tokenKind
	val  string
}

func lex(sql string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(sql) {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isIdentStart(c):
			j := i + 1
			for j < len(sql) && isIdentPart(sql[j]) {
				j++
			}
			toks = append(toks, token{tIdent, sql[i:j]})
			i = j
		case c >= '0' && c <= '9':
			j := i + 1
			for j < len(sql) && ((sql[j] >= '0' && sql[j] <= '9') || sql[j] == '.') {
				j++
			}
			toks = append(toks, token{tNumber, sql[i:j]})
			i = j
		case c == '\'':
			j := i + 1
			var sb strings.Builder
			for {
				if j >= len(sql) {
					return nil, fmt.Errorf("unterminated string literal")
				}
				if sql[j] == '\'' {
					if j+1 < len(sql) && sql[j+1] == '\'' {
						sb.WriteByte('\'')
						j += 2
						continue
					}
					break
				}
				sb.WriteByte(sql[j])
				j++
			}
			toks = append(toks, token{tString, sb.String()})
			i = j + 1
		case c == ',':
			toks = append(toks, token{tComma, ","})
			i++
		case c == '(':
			toks = append(toks, token{tLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")"})
			i++
		case c == '.':
			toks = append(toks, token{tDot, "."})
			i++
		case c == '*':
			toks = append(toks, token{tStar, "*"})
			i++
		case c == ';':
			toks = append(toks, token{tSemi, ";"})
			i++
		case c == '=':
			toks = append(toks, token{tOp, "="})
			i++
		case c == '<':
			if i+1 < len(sql) && sql[i+1] == '=' {
				toks = append(toks, token{tOp, "<="})
				i += 2
			} else if i+1 < len(sql) && sql[i+1] == '>' {
				toks = append(toks, token{tOp, "<>"})
				i += 2
			} else {
				toks = append(toks, token{tOp, "<"})
				i++
			}
		case c == '>':
			if i+1 < len(sql) && sql[i+1] == '=' {
				toks = append(toks, token{tOp, ">="})
				i += 2
			} else {
				toks = append(toks, token{tOp, ">"})
				i++
			}
		case c == '!':
			if i+1 < len(sql) && sql[i+1] == '=' {
				toks = append(toks, token{tOp, "!="})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected character '!'")
			}
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	toks = append(toks, token{tEOF, ""})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
