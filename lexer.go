package netsec

import (
	"fmt"
	"strings"
	"unicode"
)

type tokenKind int

const (
	tEOF tokenKind = iota
	tIdent
	tString
	tNumber
	tOp
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

func lex(src string) ([]token, error) {
	var tokens []token
	for i := 0; i < len(src); {
		ch := src[i]

		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			i++
		case ch == '/' && i+1 < len(src) && src[i+1] == '/':
			i += 2
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case ch == '"':
			lit, next, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tString, text: lit, pos: i})
			i = next
		case ch >= '0' && ch <= '9':
			start := i
			for i < len(src) && (src[i] >= '0' && src[i] <= '9' || src[i] == '.') {
				i++
			}
			tokens = append(tokens, token{kind: tNumber, text: src[start:i], pos: start})
		case isIdentStart(ch):
			start := i
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			tokens = append(tokens, token{kind: tIdent, text: src[start:i], pos: start})
		case strings.HasPrefix(src[i:], "==") || strings.HasPrefix(src[i:], "!=") ||
			strings.HasPrefix(src[i:], "&&") || strings.HasPrefix(src[i:], "||") ||
			strings.HasPrefix(src[i:], "<=") || strings.HasPrefix(src[i:], ">="):
			tokens = append(tokens, token{kind: tOp, text: src[i : i+2], pos: i})
			i += 2
		case ch == '<' || ch == '>' || ch == '(' || ch == ')' || ch == ';' ||
			ch == '.' || ch == '[' || ch == ']' || ch == ',':
			tokens = append(tokens, token{kind: tOp, text: string(ch), pos: i})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q at offset %d", ch, i)
		}
	}
	tokens = append(tokens, token{kind: tEOF, pos: len(src)})
	return tokens, nil
}

func lexString(src string, start int) (string, int, error) {
	var builder strings.Builder
	i := start + 1
	for i < len(src) {
		ch := src[i]
		if ch == '"' {
			return builder.String(), i + 1, nil
		}
		if ch == '\\' {
			i++
			if i >= len(src) {
				break
			}
			switch src[i] {
			case '"':
				builder.WriteByte('"')
			case '\\':
				builder.WriteByte('\\')
			case 'n':
				builder.WriteByte('\n')
			case 't':
				builder.WriteByte('\t')
			default:
				return "", 0, fmt.Errorf("invalid escape \\%c at offset %d", src[i], i)
			}
			i++
			continue
		}
		if ch == '\n' {
			return "", 0, fmt.Errorf("unterminated string starting at offset %d", start)
		}
		builder.WriteByte(ch)
		i++
	}
	return "", 0, fmt.Errorf("unterminated string starting at offset %d", start)
}

func isIdentStart(ch byte) bool {
	return ch == '_' || (ch < 128 && unicode.IsLetter(rune(ch)))
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}
