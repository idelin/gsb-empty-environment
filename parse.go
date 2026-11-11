package packetvm

import (
	"fmt"
	"unicode"
)

// ParseError describes malformed tenant source text.
type ParseError struct {
	Line int
	Col  int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error at line %d col %d: %s", e.Line, e.Col, e.Msg)
}

type tokenKind uint8

const (
	tEOF tokenKind = iota
	tNum
	tIdent
	tLParen
	tRParen
	tAnd
	tOr
	tNot
	tOp
)

type token struct {
	kind tokenKind
	text string
	num  uint32
	line int
	col  int
}

func lex(src string) ([]token, error) {
	var toks []token
	line, col := 1, 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			col = 1
			i++
		case unicode.IsSpace(rune(c)):
			col++
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen, text: "(", line: line, col: col})
			i++
			col++
		case c == ')':
			toks = append(toks, token{kind: tRParen, text: ")", line: line, col: col})
			i++
			col++
		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tOp, text: "!=", line: line, col: col})
				i += 2
				col += 2
				continue
			}
			toks = append(toks, token{kind: tNot, text: "!", line: line, col: col})
			i++
			col++
		case c >= '0' && c <= '9':
			startCol := col
			var val uint64
			for i < len(src) && src[i] >= '0' && src[i] <= '9' {
				val = val*10 + uint64(src[i]-'0')
				if val > uint64(^uint32(0)) {
					return nil, &ParseError{Line: line, Col: startCol, Msg: "integer overflows 32 bits"}
				}
				i++
				col++
			}
			toks = append(toks, token{kind: tNum, text: fmt.Sprint(val), num: uint32(val), line: line, col: startCol})
		case isIdentStart(rune(c)):
			startCol := col
			start := i
			for i < len(src) && isIdentPart(rune(src[i])) {
				i++
				col++
			}
			word := src[start:i]
			toks = append(toks, token{kind: identKind(word), text: word, line: line, col: startCol})
		case (c == '=' && i+1 < len(src) && src[i+1] == '=') ||
			(c == '<' && i+1 < len(src) && src[i+1] == '=') ||
			(c == '>' && i+1 < len(src) && src[i+1] == '='):
			toks = append(toks, token{kind: tOp, text: src[i : i+2], line: line, col: col})
			i += 2
			col += 2
		case c == '<' || c == '>':
			toks = append(toks, token{kind: tOp, text: string(c), line: line, col: col})
			i++
			col++
		case c == '&' && i+1 < len(src) && src[i+1] == '&':
			toks = append(toks, token{kind: tAnd, text: "&&", line: line, col: col})
			i += 2
			col += 2
		case c == '|' && i+1 < len(src) && src[i+1] == '|':
			toks = append(toks, token{kind: tOr, text: "||", line: line, col: col})
			i += 2
			col += 2
		default:
			return nil, &ParseError{Line: line, Col: col, Msg: fmt.Sprintf("unexpected character %q", c)}
		}
	}
	toks = append(toks, token{kind: tEOF, line: line, col: col})
	return toks, nil
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func identKind(word string) tokenKind {
	switch word {
	case "and", "AND":
		return tAnd
	case "or", "OR":
		return tOr
	case "not", "NOT":
		return tNot
	default:
		return tIdent
	}
}

type parser struct {
	toks   []token
	pos    int
	nextID int
}

// Parse compiles tenant rule text into a source-annotated boolean AST.
//
// Grammar (lowest to highest precedence):
//
//	or      := and (OR and)*
//	and     := unary (AND unary)*
//	unary   := NOT unary | primary
//	primary := '(' or ')' | comparison
//	comparison := numeric CMP numeric
//	numeric := NUMBER | len | load
//	load    := ('u8'|'u16'|'u32') '(' NUMBER ')'
//
// Connectives are and/&&, or/||, not/!. Reads are big-endian fixed-width
// loads and len is the packet length in bytes.
func Parse(name, src string) (Rule, error) {
	toks, err := lex(src)
	if err != nil {
		return Rule{}, err
	}
	p := &parser{toks: toks}
	body, err := p.parseOr()
	if err != nil {
		return Rule{}, err
	}
	if p.peek().kind != tEOF {
		return Rule{}, p.failf("unexpected token %q", p.peek().text)
	}
	return Rule{Name: name, Body: body}, nil
}

func (p *parser) parseOr() (Bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	var terms []Bool
	for p.peek().kind == tOr {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		terms = append(terms, right)
	}
	if len(terms) == 0 {
		return left, nil
	}
	all := append([]Bool{left}, terms...)
	return &Or{Terms: all, Pos: p.nodeSource("or", all[0].Src())}, nil
}

func (p *parser) parseAnd() (Bool, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	var terms []Bool
	for p.peek().kind == tAnd {
		p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		terms = append(terms, right)
	}
	if len(terms) == 0 {
		return left, nil
	}
	all := append([]Bool{left}, terms...)
	return &And{Terms: all, Pos: p.nodeSource("and", all[0].Src())}, nil
}

func (p *parser) parseUnary() (Bool, error) {
	if p.peek().kind == tNot {
		notTok := p.advance()
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Not{Inner: inner, Pos: p.sourceAt("not", notTok)}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Bool, error) {
	if p.peek().kind == tLParen {
		p.advance()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRParen {
			return nil, p.failf("missing closing parenthesis")
		}
		p.advance()
		return inner, nil
	}
	return p.parseComparison()
}

func (p *parser) parseComparison() (Bool, error) {
	start := p.peek()
	lhs, err := p.parseNumeric()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tOp {
		return nil, p.failf("expected comparison operator, got %q", p.peek().text)
	}
	opTok := p.advance()
	op, ok := parseCmpOp(opTok.text)
	if !ok {
		return nil, &ParseError{Line: opTok.line, Col: opTok.col, Msg: "unknown operator " + opTok.text}
	}
	rhs, err := p.parseNumeric()
	if err != nil {
		return nil, err
	}
	_ = start
	return &Cmp{Op: op, LHS: lhs, RHS: rhs, Pos: p.sourceAt("cmp", opTok)}, nil
}

func (p *parser) parseNumeric() (Numeric, error) {
	tok := p.peek()
	switch tok.kind {
	case tNum:
		p.advance()
		return &Imm{Value: tok.num, Pos: p.sourceAt(fmt.Sprintf("imm %d", tok.num), tok)}, nil
	case tIdent:
		switch tok.text {
		case "len":
			p.advance()
			return &Len{Pos: p.sourceAt("len", tok)}, nil
		case "u8", "u16", "u32":
			return p.parseLoad()
		default:
			return nil, p.failf("unknown identifier %q", tok.text)
		}
	default:
		return nil, p.failf("expected number, len or load, got %q", tok.text)
	}
}

func (p *parser) parseLoad() (Numeric, error) {
	widthTok := p.advance()
	size := map[string]Size{"u8": Byte, "u16": Half, "u32": Word}[widthTok.text]
	if p.peek().kind != tLParen {
		return nil, p.failf("expected '(' after %s", widthTok.text)
	}
	p.advance()
	if p.peek().kind != tNum {
		return nil, p.failf("load offset must be a non-negative integer")
	}
	offTok := p.advance()
	if p.peek().kind != tRParen {
		return nil, p.failf("missing ')' after load offset")
	}
	p.advance()
	return &Load{
		Offset: offTok.num,
		Size:   size,
		Pos:    p.sourceAt(fmt.Sprintf("%s(%d)", widthTok.text, offTok.num), widthTok),
	}, nil
}

func parseCmpOp(s string) (CmpOp, bool) {
	switch s {
	case "==":
		return Eq, true
	case "!=":
		return Ne, true
	case "<":
		return Lt, true
	case "<=":
		return Le, true
	case ">":
		return Gt, true
	case ">=":
		return Ge, true
	default:
		return 0, false
	}
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) advance() token {
	t := p.toks[p.pos]
	if t.kind != tEOF {
		p.pos++
	}
	return t
}

func (p *parser) failf(format string, args ...any) error {
	t := p.peek()
	return &ParseError{Line: t.line, Col: t.col, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) sourceAt(text string, t token) Source {
	s := Source{ID: p.nextID, Text: text, Line: t.line, Col: t.col}
	p.nextID++
	return s
}

func (p *parser) nodeSource(text string, anchor Source) Source {
	s := Source{ID: p.nextID, Text: text, Line: anchor.Line, Col: anchor.Col}
	p.nextID++
	return s
}
