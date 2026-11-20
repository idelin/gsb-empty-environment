package biscuit

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenKind int

const (
	tEOF tokenKind = iota
	tIdent
	tVariable
	tString
	tInteger
	tColon
	tLParen
	tRParen
	tComma
	tSemicolon
	tOr
)

type token struct {
	kind tokenKind
	text string
	line int
	col  int
}

func (t token) String() string {
	switch t.kind {
	case tEOF:
		return "end of input"
	case tIdent:
		return "identifier " + t.text
	case tVariable:
		return "variable " + t.text
	case tString:
		return "string " + t.text
	case tInteger:
		return "integer " + t.text
	default:
		return strconv.Quote(t.text)
	}
}

type lexer struct {
	src  string
	pos  int
	line int
	col  int
	toks []token
}

func lex(src string) (*lexer, error) {
	l := &lexer{src: src, line: 1, col: 1}
	for l.pos < len(l.src) {
		r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
		switch {
		case r == '\n':
			l.advance(1)
			l.line++
			l.col = 1
		case unicode.IsSpace(r):
			l.advance(utf8.RuneLen(r))
		case r == '#':
			l.skipLineComment()
		case isIdentStart(r):
			l.readIdent()
		case r == '$':
			if err := l.readVariable(); err != nil {
				return nil, err
			}
		case r == '"':
			if err := l.readString(); err != nil {
				return nil, err
			}
		case r >= '0' && r <= '9':
			l.readInteger()
		default:
			if err := l.readSymbol(r); err != nil {
				return nil, err
			}
		}
	}
	l.toks = append(l.toks, token{kind: tEOF, line: l.line, col: l.col})
	return l, nil
}

func (l *lexer) advance(n int) {
	l.pos += n
	l.col += n
}

func (l *lexer) skipLineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.advance(1)
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

func (l *lexer) readIdent() {
	start := l.pos
	line, col := l.line, l.col
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isIdentPart(r) {
			break
		}
		l.advance(size)
	}
	l.toks = append(l.toks, token{kind: tIdent, text: l.src[start:l.pos], line: line, col: col})
}

func (l *lexer) readVariable() error {
	line, col := l.line, l.col
	l.advance(1) // 跳过 $
	start := l.pos
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isIdentPart(r) {
			break
		}
		l.advance(size)
	}
	name := l.src[start:l.pos]
	if name == "" {
		return lexErrorf(line, col, "expected variable name after $")
	}
	l.toks = append(l.toks, token{kind: tVariable, text: name, line: line, col: col})
	return nil
}

func (l *lexer) readString() error {
	line, col := l.line, l.col
	l.advance(1) // 跳过开头引号
	var builder strings.Builder
	for {
		if l.pos >= len(l.src) {
			return lexErrorf(line, col, "unterminated string literal")
		}
		ch := l.src[l.pos]
		if ch == '"' {
			l.advance(1)
			l.toks = append(l.toks, token{kind: tString, text: builder.String(), line: line, col: col})
			return nil
		}
		if ch == '\n' {
			return lexErrorf(line, col, "unterminated string literal")
		}
		if ch == '\\' {
			if l.pos+1 >= len(l.src) {
				return lexErrorf(line, col, "unterminated escape sequence")
			}
			esc := l.src[l.pos+1]
			switch esc {
			case 'n':
				builder.WriteByte('\n')
			case 't':
				builder.WriteByte('\t')
			case 'r':
				builder.WriteByte('\r')
			case '\\':
				builder.WriteByte('\\')
			case '"':
				builder.WriteByte('"')
			default:
				return lexErrorf(l.line, l.col, "unsupported escape sequence \\%c", esc)
			}
			l.advance(2)
			continue
		}
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		builder.WriteRune(r)
		l.advance(size)
	}
}

func (l *lexer) readInteger() {
	start := l.pos
	line, col := l.line, l.col
	for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
		l.advance(1)
	}
	l.toks = append(l.toks, token{kind: tInteger, text: l.src[start:l.pos], line: line, col: col})
}

func (l *lexer) readSymbol(first rune) error {
	line, col := l.line, l.col
	next := byte(0)
	if l.pos+1 < len(l.src) {
		next = l.src[l.pos+1]
	}
	switch first {
	case ':':
		if next == '-' {
			l.advance(2)
			l.toks = append(l.toks, token{kind: tColon, text: ":-", line: line, col: col})
			return nil
		}
		return lexErrorf(line, col, "unexpected character %q", first)
	case '|':
		if next == '|' {
			l.advance(2)
			l.toks = append(l.toks, token{kind: tOr, text: "||", line: line, col: col})
			return nil
		}
		return lexErrorf(line, col, "unexpected character %q", first)
	case '(':
		l.advance(1)
		l.toks = append(l.toks, token{kind: tLParen, text: "(", line: line, col: col})
	case ')':
		l.advance(1)
		l.toks = append(l.toks, token{kind: tRParen, text: ")", line: line, col: col})
	case ',':
		l.advance(1)
		l.toks = append(l.toks, token{kind: tComma, text: ",", line: line, col: col})
	case ';':
		l.advance(1)
		l.toks = append(l.toks, token{kind: tSemicolon, text: ";", line: line, col: col})
	default:
		return lexErrorf(line, col, "unexpected character %q", first)
	}
	return nil
}

// ParseError 携带解析失败的位置信息。
type ParseError struct {
	Line int
	Col  int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d col %d: %s", e.Line, e.Col, e.Msg)
}

func lexErrorf(line, col int, format string, args ...any) error {
	return &ParseError{Line: line, Col: col, Msg: fmt.Sprintf(format, args...)}
}
