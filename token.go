package macroexp

import "fmt"

// Pos is a source position.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

func (p Pos) IsZero() bool { return p.File == "" && p.Line == 0 && p.Col == 0 }

// Kind classifies lexed tokens.
type Kind int

const (
	KindIdent Kind = iota
	KindNumber
	KindString
	KindPunct
	KindSpace
	KindNewline
	KindDirective // #def, #local, #end, ...
)

// Token is a lexed input token with its source position.
type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// Lex tokenizes src into a token stream, preserving whitespace and newlines
// so the expanded output can reproduce the input layout.
func Lex(file, src string) []Token {
	var toks []Token
	line, col := 1, 1
	i := 0
	emit := func(k Kind, text string, l, c int) {
		toks = append(toks, Token{Kind: k, Text: text, Pos: Pos{File: file, Line: l, Col: c}})
	}
	for i < len(src) {
		ch := src[i]
		switch {
		case ch == '\n':
			emit(KindNewline, "\n", line, col)
			i++
			line++
			col = 1
		case ch == ' ' || ch == '\t' || ch == '\r':
			start, c0 := i, col
			for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r') {
				i++
				col++
			}
			emit(KindSpace, src[start:i], line, c0)
		case isIdentStart(ch):
			start, c0 := i, col
			for i < len(src) && isIdentPart(src[i]) {
				i++
				col++
			}
			emit(KindIdent, src[start:i], line, c0)
		case ch >= '0' && ch <= '9':
			start, c0 := i, col
			for i < len(src) && (isIdentPart(src[i]) || src[i] == '.') {
				i++
				col++
			}
			emit(KindNumber, src[start:i], line, c0)
		case ch == '"':
			start, c0 := i, col
			i++
			col++
			for i < len(src) && src[i] != '"' && src[i] != '\n' {
				if src[i] == '\\' && i+1 < len(src) {
					i++
					col++
				}
				i++
				col++
			}
			if i < len(src) && src[i] == '"' {
				i++
				col++
			}
			emit(KindString, src[start:i], line, c0)
		case ch == '#':
			c0 := col
			if i+1 < len(src) && isIdentStart(src[i+1]) {
				j := i + 1
				for j < len(src) && isIdentPart(src[j]) {
					j++
				}
				emit(KindDirective, src[i:j], line, c0)
				col += j - i
				i = j
			} else {
				emit(KindPunct, "#", line, c0)
				i++
				col++
			}
		default:
			emit(KindPunct, string(ch), line, col)
			i++
			col++
		}
	}
	return toks
}
