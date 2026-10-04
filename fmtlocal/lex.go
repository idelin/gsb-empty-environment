package main

import "fmt"

type TokKind int

const (
	kNum TokKind = iota
	kIdent
	kOp
	kLParen
	kRParen
	kComma
	kLineComment
	kBlockComment
)

func (k TokKind) String() string {
	switch k {
	case kNum:
		return "Number"
	case kIdent:
		return "Ident"
	case kOp:
		return "Op"
	case kLParen:
		return "LParen"
	case kRParen:
		return "RParen"
	case kComma:
		return "Comma"
	case kLineComment:
		return "LineComment"
	case kBlockComment:
		return "BlockComment"
	}
	return "?"
}

// Token is a lexical unit. Whitespace is never tokenized; comments are
// tokens so they keep their position relative to real tokens.
type Token struct {
	Kind     TokKind
	Text     string
	Start    int  // byte offset, inclusive
	End      int  // byte offset, exclusive
	CallOpen bool // '(' that opens a call argument list
	Unary    bool // '-' used as a prefix operator
}

func (t Token) isComment() bool {
	return t.Kind == kLineComment || t.Kind == kBlockComment
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func lex(src string) ([]Token, error) {
	var toks []Token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case isSpace(c):
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			j := i
			for j < len(src) && src[j] != '\n' {
				j++
			}
			toks = append(toks, Token{Kind: kLineComment, Text: src[i:j], Start: i, End: j})
			i = j
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			j := i + 2
			for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
				j++
			}
			if j+1 >= len(src) {
				return nil, fmt.Errorf("unterminated block comment at byte %d", i)
			}
			j += 2
			toks = append(toks, Token{Kind: kBlockComment, Text: src[i:j], Start: i, End: j})
			i = j
		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			j := i
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, Token{Kind: kNum, Text: src[i:j], Start: i, End: j})
			i = j
		case isAlpha(c):
			j := i
			for j < len(src) && (isAlpha(src[j]) || isDigit(src[j])) {
				j++
			}
			toks = append(toks, Token{Kind: kIdent, Text: src[i:j], Start: i, End: j})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/':
			toks = append(toks, Token{Kind: kOp, Text: src[i : i+1], Start: i, End: i + 1})
			i++
		case c == '(':
			toks = append(toks, Token{Kind: kLParen, Text: "(", Start: i, End: i + 1})
			i++
		case c == ')':
			toks = append(toks, Token{Kind: kRParen, Text: ")", Start: i, End: i + 1})
			i++
		case c == ',':
			toks = append(toks, Token{Kind: kComma, Text: ",", Start: i, End: i + 1})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q at byte %d", c, i)
		}
	}
	return toks, nil
}
