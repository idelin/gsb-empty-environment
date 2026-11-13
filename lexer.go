package netsec

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokenKind 是词法单元的种类。
type tokenKind int

const (
	tkEOF tokenKind = iota
	tkIdent
	tkVariable
	tkString
	tkInteger
	tkLParen
	tkRParen
	tkComma
	tkColonDash // :-
	tkSemicolon
	tkNewline
)

type token struct {
	kind tokenKind
	text string
	line int
}

// lexer 把 Datalog 源文本切成词法单元。括号内的换行仅作为空白，
// 括号外的换行与分号一样充当语句终止符。
type lexer struct {
	src   string
	pos   int
	line  int
	depth int
}

func lex(src string) ([]token, error) {
	l := &lexer{src: src, line: 1}
	tokens := make([]token, 0)
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		switch {
		case r == '\n':
			if l.depth == 0 {
				tokens = append(tokens, token{kind: tkNewline, line: l.line})
			}
			l.pos += size
			l.line++
		case unicode.IsSpace(r):
			l.pos += size
		case r == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		case r == '(':
			tokens = append(tokens, token{kind: tkLParen, line: l.line})
			l.depth++
			l.pos += size
		case r == ')':
			if l.depth == 0 {
				return nil, fmt.Errorf("%w: line %d: unmatched ')'", ErrParse, l.line)
			}
			l.depth--
			tokens = append(tokens, token{kind: tkRParen, line: l.line})
			l.pos += size
		case r == ',':
			tokens = append(tokens, token{kind: tkComma, line: l.line})
			l.pos += size
		case r == ';':
			tokens = append(tokens, token{kind: tkSemicolon, line: l.line})
			l.pos += size
		case r == ':' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '-':
			tokens = append(tokens, token{kind: tkColonDash, line: l.line})
			l.pos += 2
		case r == '$':
			startLine := l.line
			l.pos += size
			ident, err := l.scanIdent()
			if err != nil {
				return nil, err
			}
			if ident == "" {
				return nil, fmt.Errorf("%w: line %d: variable name cannot be empty", ErrParse, startLine)
			}
			tokens = append(tokens, token{kind: tkVariable, text: ident, line: startLine})
		case r == '"':
			startLine := l.line
			value, err := l.scanString()
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tkString, text: value, line: startLine})
		case r >= '0' && r <= '9':
			start := l.pos
			startLine := l.line
			for l.pos < len(l.src) {
				c := l.src[l.pos]
				if c < '0' || c > '9' {
					break
				}
				l.pos++
			}
			tokens = append(tokens, token{kind: tkInteger, text: l.src[start:l.pos], line: startLine})
		case isIdentStart(r):
			startLine := l.line
			ident, err := l.scanIdent()
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tkIdent, text: ident, line: startLine})
		default:
			return nil, fmt.Errorf("%w: line %d: unexpected character %q", ErrParse, l.line, r)
		}
	}
	if l.depth != 0 {
		return nil, fmt.Errorf("%w: line %d: unclosed '('", ErrParse, l.line)
	}
	tokens = append(tokens, token{kind: tkEOF, line: l.line})
	return tokens, nil
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (l *lexer) scanIdent() (string, error) {
	start := l.pos
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isIdentPart(r) {
			break
		}
		l.pos += size
	}
	if start == l.pos {
		return "", fmt.Errorf("%w: line %d: expected identifier", ErrParse, l.line)
	}
	return l.src[start:l.pos], nil
}

func (l *lexer) scanString() (string, error) {
	startLine := l.line
	l.pos++ // 跳过开引号
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '"' {
			l.pos++
			return b.String(), nil
		}
		if c == '\n' {
			return "", fmt.Errorf("%w: line %d: unterminated string literal", ErrParse, startLine)
		}
		if c == '\\' {
			l.pos++
			if l.pos >= len(l.src) {
				break
			}
			esc := l.src[l.pos]
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				return "", fmt.Errorf("%w: line %d: invalid string escape \\%c", ErrParse, l.line, esc)
			}
			l.pos++
			continue
		}
		b.WriteByte(c)
		l.pos++
	}
	return "", fmt.Errorf("%w: line %d: unterminated string literal", ErrParse, startLine)
}
