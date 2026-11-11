package imapsync

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// List is a parenthesized (or bracketed) IMAP list.
type List []interface{}

// Response is a single parsed IMAP response line. Numeric atoms are
// represented as uint64, everything else as string, nested lists as List.
type Response struct {
	Tag    string // "*", "+", or a command tag
	Seq    int    // sequence number for untagged FETCH/EXISTS/RECENT/EXPUNGE
	Type   string // upper-cased response type: OK, NO, BAD, FETCH, SEARCH, ...
	Fields []interface{}
}

// token kinds
const (
	tokEOL     = 0
	tokAtom    = 'a'
	tokQuoted  = 'q'
	tokLiteral = 'l'
	tokLParen  = '('
	tokRParen  = ')'
)

type token struct {
	kind byte
	val  string
}

type parser struct {
	r *bufio.Reader
}

func newParser(rd io.Reader) *parser {
	return &parser{r: bufio.NewReaderSize(rd, 4096)}
}

// isAtomBreak reports whether c terminates an atom. Square brackets are
// ordinary atom characters here so section specifications like BODY[HEADER]
// stay intact; [...] response codes are reassembled at a higher level.
func isAtomBreak(c byte) bool {
	switch c {
	case ' ', '(', ')', '{', '"', '\r', '\n':
		return true
	}
	return false
}

func (p *parser) expectCRLF() error {
	c, err := p.r.ReadByte()
	if err != nil {
		return err
	}
	if c == '\n' {
		return nil // tolerate bare LF
	}
	if c != '\r' {
		return fmt.Errorf("imapsync: expected CR, got %q", c)
	}
	c, err = p.r.ReadByte()
	if err != nil {
		return err
	}
	if c != '\n' {
		return fmt.Errorf("imapsync: expected LF after CR, got %q", c)
	}
	return nil
}

// readToken reads one token; a token with kind tokEOL signals end of line.
func (p *parser) readToken() (token, error) {
	for {
		c, err := p.r.ReadByte()
		if err != nil {
			return token{}, err
		}
		switch {
		case c == ' ':
			continue
		case c == '\r' || c == '\n':
			if c == '\r' {
				if err := p.expectCRLFtail(); err != nil {
					return token{}, err
				}
			}
			return token{kind: tokEOL}, nil
		case c == '(':
			return token{kind: tokLParen}, nil
		case c == ')':
			return token{kind: tokRParen}, nil
		case c == '"':
			return p.readQuoted()
		case c == '{':
			return p.readLiteral()
		default:
			return p.readAtom(c)
		}
	}
}

func (p *parser) expectCRLFtail() error {
	c, err := p.r.ReadByte()
	if err != nil {
		return err
	}
	if c != '\n' {
		return fmt.Errorf("imapsync: expected LF after CR, got %q", c)
	}
	return nil
}

func (p *parser) readQuoted() (token, error) {
	var b strings.Builder
	for {
		c, err := p.r.ReadByte()
		if err != nil {
			return token{}, err
		}
		switch c {
		case '"':
			return token{kind: tokQuoted, val: b.String()}, nil
		case '\\':
			n, err := p.r.ReadByte()
			if err != nil {
				return token{}, err
			}
			b.WriteByte(n)
		default:
			b.WriteByte(c)
		}
	}
}

// readLiteral reads "{n}\r\n" followed by exactly n bytes. io.ReadFull over
// the buffered reader reassembles literals that are split across multiple
// underlying Read calls.
func (p *parser) readLiteral() (token, error) {
	var num strings.Builder
	for {
		c, err := p.r.ReadByte()
		if err != nil {
			return token{}, err
		}
		if c == '}' {
			break
		}
		if c != '+' { // non-sync literal marker {n+}
			num.WriteByte(c)
		}
	}
	if err := p.expectCRLF(); err != nil {
		return token{}, err
	}
	n, err := strconv.Atoi(num.String())
	if err != nil || n < 0 {
		return token{}, fmt.Errorf("imapsync: bad literal size %q", num.String())
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(p.r, buf); err != nil {
		return token{}, fmt.Errorf("imapsync: reading %d-byte literal: %w", n, err)
	}
	return token{kind: tokLiteral, val: string(buf)}, nil
}

func (p *parser) readAtom(first byte) (token, error) {
	var b strings.Builder
	b.WriteByte(first)
	for {
		c, err := p.r.ReadByte()
		if err != nil {
			if err == io.EOF && b.Len() > 0 {
				return token{kind: tokAtom, val: b.String()}, nil
			}
			return token{}, err
		}
		if isAtomBreak(c) {
			p.r.UnreadByte()
			return token{kind: tokAtom, val: b.String()}, nil
		}
		b.WriteByte(c)
	}
}

// assemble turns a flat token stream into fields, building nested Lists.
func assemble(toks []token) ([]interface{}, error) {
	var stack [][]interface{}
	cur := []interface{}{}
	for _, t := range toks {
		switch t.kind {
		case tokLParen:
			stack = append(stack, cur)
			cur = []interface{}{}
		case tokRParen:
			if len(stack) == 0 {
				return nil, fmt.Errorf("imapsync: unbalanced ')' in response")
			}
			parent := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			cur = append(parent, List(cur))
		default:
			if t.kind == tokAtom {
				if u, err := strconv.ParseUint(t.val, 10, 64); err == nil {
					cur = append(cur, u)
					continue
				}
			}
			cur = append(cur, t.val)
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("imapsync: unbalanced '(' in response")
	}
	return cur, nil
}

// readResponse reads one complete response line (literals included).
func (p *parser) readResponse() (*Response, error) {
	var toks []token
	for {
		t, err := p.readToken()
		if err != nil {
			return nil, err
		}
		if t.kind == tokEOL {
			break
		}
		toks = append(toks, t)
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("imapsync: empty response line")
	}
	resp := &Response{Tag: toks[0].val}
	rest := toks[1:]
	if resp.Tag == "*" && len(rest) > 0 && rest[0].kind == tokAtom {
		if n, err := strconv.ParseUint(rest[0].val, 10, 32); err == nil {
			resp.Seq = int(n)
			rest = rest[1:]
		}
	}
	if len(rest) > 0 && rest[0].kind == tokAtom {
		resp.Type = strings.ToUpper(rest[0].val)
		rest = rest[1:]
	}
	fields, err := assemble(rest)
	if err != nil {
		return nil, err
	}
	resp.Fields = fields
	return resp, nil
}

// toUint extracts a uint64 from a parsed field (uint64 or numeric string).
func toUint(v interface{}) (uint64, bool) {
	switch t := v.(type) {
	case uint64:
		return t, true
	case string:
		n, err := strconv.ParseUint(t, 10, 64)
		return n, err == nil
	}
	return 0, false
}
