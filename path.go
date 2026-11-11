package jsonpathstream

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Path is a compiled JSONPath query.
type Path struct {
	Source   string
	segments []segment
}

type segKind int

const (
	segMember segKind = iota
	segWildcard
	segIndex
	segSlice
	segFilter
)

type segment struct {
	kind    segKind
	recurse bool

	// member
	name string

	// index
	index       int
	needsLength bool // negative index or negative slice bounds need array length first

	// slice; step is always >= 1; nil bounds mean unbounded
	start *int
	end   *int
	step  int

	// filter
	filter *filterExpr
}

type refStep struct {
	isIndex bool
	name    string
	index   int
}

// refPath is an @-relative reference inside a filter, e.g. @.addr.city or
// @.a[0]. An empty path refers to the current node itself.
type refPath struct {
	steps []refStep
}

type filterExpr struct {
	lhs      refPath
	op       string
	rhsIsRef bool
	rhs      refPath
	literal  any // float64, string, bool or nil
}

type parser struct {
	s   string
	pos int
}

// ASCII punctuation used while scanning. Byte constants avoid rune literals.
const (
	bDollar    byte = 0x24 // $
	bDot       byte = 0x2E // .
	bLBrack    byte = 0x5B // [
	bRBrack    byte = 0x5D // ]
	bStar      byte = 0x2A // *
	bQuestion  byte = 0x3F // ?
	bLParen    byte = 0x28 // (
	bRParen    byte = 0x29 // )
	bDQuote    byte = 0x22 // "
	bSQuote    byte = 0x27 // single quote
	bColon     byte = 0x3A // :
	bMinus     byte = 0x2D // -
	bPlus      byte = 0x2B // +
	bBackSlash byte = 0x5C // backslash
)

func isDigit(r rune) bool { return r >= 0x30 && r <= 0x39 }

// Parse compiles a JSONPath expression.
//
// Supported syntax:
//
//	$                          root
//	$.name or $["name"]        child member
//	$.* or $[*]                wildcard
//	$[i]                       array index (a negative index buffers the
//	                           enclosing array)
//	$[start:end] or [a:b:k]    array slice, Python-style; step must be
//	                           positive; negative bounds buffer the array
//	$..name, $..*, $..[...]    recursive descent
//	$[?(@.x op v)]             comparison filter; op is one of ==, !=, <,
//	                           <=, >, >=; v is a literal or an @-reference
func Parse(query string) (*Path, error) {
	p := &parser{s: query}
	p.skipSpaces()
	if p.pos >= len(p.s) || p.s[p.pos] != bDollar {
		return nil, fmt.Errorf("jsonpath: query must start with $: %q", query)
	}
	p.pos++

	var segs []segment
	for p.pos < len(p.s) {
		p.skipSpaces()
		if p.pos >= len(p.s) {
			break
		}
		switch p.s[p.pos] {
		case bDot:
			p.pos++
			recurse := false
			if p.pos < len(p.s) && p.s[p.pos] == bDot {
				recurse = true
				p.pos++
			}
			p.skipSpaces()
			if p.pos >= len(p.s) {
				return nil, p.err("expected member name, wildcard or [ after .")
			}
			if p.s[p.pos] == bLBrack {
				seg, err := p.parseBracket()
				if err != nil {
					return nil, err
				}
				seg.recurse = recurse
				segs = append(segs, seg)
				continue
			}
			if p.s[p.pos] == bStar {
				p.pos++
				segs = append(segs, segment{kind: segWildcard, recurse: recurse})
				continue
			}
			name, err := p.parseIdent()
			if err != nil {
				return nil, err
			}
			segs = append(segs, segment{kind: segMember, recurse: recurse, name: name})
		case bLBrack:
			seg, err := p.parseBracket()
			if err != nil {
				return nil, err
			}
			segs = append(segs, seg)
		default:
			return nil, p.errf("unexpected character %q", p.s[p.pos])
		}
	}
	return &Path{Source: query, segments: segs}, nil
}

func (p *parser) parseBracket() (segment, error) {
	if p.s[p.pos] != bLBrack {
		return segment{}, p.err("expected [")
	}
	p.pos++
	p.skipSpaces()
	if p.pos >= len(p.s) {
		return segment{}, p.err("unterminated [")
	}
	var seg segment
	switch {
	case p.s[p.pos] == bQuestion:
		f, err := p.parseFilter()
		if err != nil {
			return segment{}, err
		}
		seg.kind = segFilter
		seg.filter = f
	case p.s[p.pos] == bStar:
		p.pos++
		seg.kind = segWildcard
	case p.s[p.pos] == bDQuote || p.s[p.pos] == bSQuote:
		name, err := p.parseQuoted()
		if err != nil {
			return segment{}, err
		}
		seg.kind = segMember
		seg.name = name
	default:
		start := p.pos
		for p.pos < len(p.s) && p.s[p.pos] != bRBrack {
			p.pos++
		}
		if p.pos >= len(p.s) {
			return segment{}, p.err("unterminated [")
		}
		raw := strings.TrimSpace(p.s[start:p.pos])
		if raw == "" {
			return segment{}, p.err("empty bracket expression")
		}
		var err error
		seg, err = parseIndexOrSlice(raw)
		if err != nil {
			return segment{}, err
		}
	}
	p.skipSpaces()
	if p.pos >= len(p.s) || p.s[p.pos] != bRBrack {
		return segment{}, p.err("expected ]")
	}
	p.pos++
	return seg, nil
}

func parseIndexOrSlice(raw string) (segment, error) {
	if !strings.ContainsRune(raw, rune(bColon)) {
		i, err := strconv.Atoi(raw)
		if err != nil {
			return segment{}, fmt.Errorf("jsonpath: invalid bracket expression %q", raw)
		}
		return segment{kind: segIndex, index: i, needsLength: i < 0}, nil
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return segment{}, fmt.Errorf("jsonpath: invalid slice %q", raw)
	}
	seg := segment{kind: segSlice, step: 1}
	parseBound := func(part string) (*int, bool, error) {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, nil
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, false, fmt.Errorf("jsonpath: invalid slice bound %q", part)
		}
		return &v, v < 0, nil
	}
	var negative bool
	var err error
	if seg.start, negative, err = parseBound(parts[0]); err != nil {
		return segment{}, err
	}
	seg.needsLength = seg.needsLength || negative
	if seg.end, negative, err = parseBound(parts[1]); err != nil {
		return segment{}, err
	}
	seg.needsLength = seg.needsLength || negative
	if len(parts) == 3 {
		stepPart := strings.TrimSpace(parts[2])
		if stepPart == "" {
			return segment{}, fmt.Errorf("jsonpath: invalid slice %q", raw)
		}
		step, err := strconv.Atoi(stepPart)
		if err != nil {
			return segment{}, fmt.Errorf("jsonpath: invalid slice step %q", stepPart)
		}
		if step <= 0 {
			return segment{}, fmt.Errorf("jsonpath: only positive slice steps are supported: %q", raw)
		}
		seg.step = step
	}
	return seg, nil
}

func (p *parser) parseFilter() (*filterExpr, error) {
	p.pos++ // consume ?
	p.skipSpaces()
	if p.pos >= len(p.s) || p.s[p.pos] != bLParen {
		return nil, p.err("expected ( after ?")
	}
	depth := 1 // remaining close parens to consume
	p.pos++    // consume the opening (
	start := p.pos
	inString := byte(0)
	for p.pos < len(p.s) && depth > 0 {
		c := p.s[p.pos]
		if inString != 0 {
			if c == bBackSlash {
				p.pos += 2
				continue
			}
			if c == inString {
				inString = 0
			}
			p.pos++
			continue
		}
		switch c {
		case bDQuote, bSQuote:
			inString = c
		case bLParen:
			depth++
		case bRParen:
			depth--
		}
		if depth > 0 {
			p.pos++
		}
	}
	if depth != 0 {
		return nil, p.err("unterminated filter expression")
	}
	body := strings.TrimSpace(p.s[start:p.pos])
	p.pos++ // consume )
	p.skipSpaces()

	fp := &parser{s: body}
	fp.skipSpaces()
	lhs, err := fp.parseRef()
	if err != nil {
		return nil, err
	}
	fp.skipSpaces()
	op, err := fp.parseOp()
	if err != nil {
		return nil, err
	}
	fp.skipSpaces()
	if fp.pos >= len(fp.s) {
		return nil, fmt.Errorf("jsonpath: filter %q is missing a right-hand side", body)
	}
	f := &filterExpr{lhs: lhs, op: op}
	switch fp.s[fp.pos] {
	case bAt:
		ref, err := fp.parseRef()
		if err != nil {
			return nil, err
		}
		f.rhsIsRef = true
		f.rhs = ref
	case bDollar:
		return nil, fp.err("root-relative $ references are not supported inside filters; use @")
	case bSQuote, bDQuote:
		v, err := fp.parseQuoted()
		if err != nil {
			return nil, err
		}
		f.literal = v
	case 0x74, 0x66:
		if fp.hasPrefix("true") {
			f.literal = true
			fp.pos += 4
		} else if fp.hasPrefix("false") {
			f.literal = false
			fp.pos += 5
		} else {
			return nil, fp.err("invalid literal in filter")
		}
	case 0x6E:
		if fp.hasPrefix("null") {
			f.literal = nil
			fp.pos += 4
		} else {
			return nil, fp.err("invalid literal in filter")
		}
	default:
		numStart := fp.pos
		if fp.s[fp.pos] == bMinus || fp.s[fp.pos] == bPlus {
			fp.pos++
		}
		for fp.pos < len(fp.s) && (isDigit(rune(fp.s[fp.pos])) || fp.s[fp.pos] == bDot) {
			fp.pos++
		}
		token := fp.s[numStart:fp.pos]
		v, err := strconv.ParseFloat(token, 64)
		if err != nil {
			return nil, fmt.Errorf("jsonpath: invalid filter literal %q", token)
		}
		f.literal = v
	}
	fp.skipSpaces()
	if fp.pos != len(fp.s) {
		return nil, fmt.Errorf("jsonpath: trailing data in filter %q", body)
	}
	return f, nil
}

func (p *parser) parseRef() (refPath, error) {
	if p.pos >= len(p.s) || p.s[p.pos] != bDollar && p.s[p.pos] != bAt {
		return refPath{}, p.err("filter references must start with @")
	}
	if p.s[p.pos] == bDollar {
		return refPath{}, p.err("root-relative $ references are not supported inside filters; use @")
	}
	p.pos++
	var steps []refStep
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case bDot:
			p.pos++
			name, err := p.parseIdent()
			if err != nil {
				return refPath{}, err
			}
			steps = append(steps, refStep{name: name})
		case bLBrack:
			p.pos++
			p.skipSpaces()
			if p.pos >= len(p.s) {
				return refPath{}, p.err("unterminated [ in filter reference")
			}
			if p.s[p.pos] == bDQuote || p.s[p.pos] == bSQuote {
				name, err := p.parseQuoted()
				if err != nil {
					return refPath{}, err
				}
				steps = append(steps, refStep{name: name})
			} else {
				numStart := p.pos
				for p.pos < len(p.s) && p.s[p.pos] != bRBrack {
					p.pos++
				}
				if p.pos >= len(p.s) {
					return refPath{}, p.err("unterminated [ in filter reference")
				}
				token := strings.TrimSpace(p.s[numStart:p.pos])
				idx, err := strconv.Atoi(token)
				if err != nil {
					return refPath{}, fmt.Errorf("jsonpath: invalid array index in filter: %q", token)
				}
				steps = append(steps, refStep{isIndex: true, index: idx})
			}
			p.skipSpaces()
			if p.pos >= len(p.s) || p.s[p.pos] != bRBrack {
				return refPath{}, p.err("expected ] in filter reference")
			}
			p.pos++
		default:
			return refPath{steps: steps}, nil
		}
	}
	return refPath{steps: steps}, nil
}

func (p *parser) parseOp() (string, error) {
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.hasPrefix(op) {
			p.pos += len(op)
			return op, nil
		}
	}
	return "", p.err("expected one of ==, !=, <, <=, >, >= in filter")
}

func (p *parser) parseIdent() (string, error) {
	start := p.pos
	for p.pos < len(p.s) {
		r, size := utf8.DecodeRuneInString(p.s[p.pos:])
		if r == utf8.RuneError || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == 0x5F) {
			break
		}
		p.pos += size
	}
	if p.pos == start {
		return "", p.err("expected member name")
	}
	return p.s[start:p.pos], nil
}

func (p *parser) parseQuoted() (string, error) {
	quote := p.s[p.pos]
	start := p.pos
	p.pos++
	for p.pos < len(p.s) && p.s[p.pos] != quote {
		if p.s[p.pos] == bBackSlash {
			p.pos += 2
			continue
		}
		p.pos++
	}
	if p.pos >= len(p.s) {
		return "", p.err("unterminated string literal")
	}
	raw := p.s[start : p.pos+1]
	p.pos++
	if quote == bDQuote {
		v, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("jsonpath: invalid string literal %q", raw)
		}
		return v, nil
	}
	// Single-quoted: translate to double quotes, escaping interior double quotes.
	inner := strings.NewReplacer("\\\x27", "\x27", "\"", "\\\"").Replace(raw[1 : len(raw)-1])
	v, err := strconv.Unquote("\"" + inner + "\"")
	if err != nil {
		return "", fmt.Errorf("jsonpath: invalid string literal %q", raw)
	}
	return v, nil
}

func (p *parser) hasPrefix(s string) bool {
	return strings.HasPrefix(p.s[p.pos:], s)
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.s) && unicode.IsSpace(rune(p.s[p.pos])) {
		p.pos++
	}
}

func (p *parser) err(msg string) error {
	return fmt.Errorf("jsonpath: %s (at offset %d in %q)", msg, p.pos, p.s)
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("jsonpath: "+format+" (at offset %d)", append(args, p.pos)...)
}

const bAt byte = 0x40 // @
