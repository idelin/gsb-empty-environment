package errmerge

import "strings"

// Parser holds the input and runtime limits. Pos is only advanced by
// successful terminals and is rolled back on backtracking, so consumption
// from abandoned paths never leaks into diagnostics.
type Parser struct {
	Src      string
	Pos      int
	Depth    int
	MaxDepth int // 0 means unlimited
}

// NewParser builds a Parser over src with the given recursion depth limit.
func NewParser(src string, maxDepth int) *Parser {
	return &Parser{Src: src, MaxDepth: maxDepth}
}

// Rule parses at p.Pos. On success it returns a value and a nil *Diag.
// On failure it returns a non-nil *Diag and must leave p.Pos unchanged
// from rule entry (combinators below guarantee this).
type Rule func(p *Parser) (interface{}, *Diag)

// Named wraps r as a grammar rule: it enforces the recursion depth limit
// and records the rule in the source chain of any failure unwinding
// through it.
func Named(name string, r Rule) Rule {
	return func(p *Parser) (interface{}, *Diag) {
		if p.MaxDepth > 0 && p.Depth >= p.MaxDepth {
			d := New(p.Pos, "depth-limit:"+name)
			d.PushFrame(name)
			return nil, d
		}
		p.Depth++
		v, d := r(p)
		p.Depth--
		if d != nil {
			d.PushFrame(name)
		}
		return v, d
	}
}

// Tok matches a literal terminal.
func Tok(lit string) Rule {
	return func(p *Parser) (interface{}, *Diag) {
		if strings.HasPrefix(p.Src[p.Pos:], lit) {
			p.Pos += len(lit)
			return lit, nil
		}
		return nil, New(p.Pos, quote(lit))
	}
}

// EOF succeeds only at end of input.
func EOF() Rule {
	return func(p *Parser) (interface{}, *Diag) {
		if p.Pos == len(p.Src) {
			return nil, nil
		}
		return nil, New(p.Pos, "<EOF>")
	}
}

// Seq runs rules in order. On the first failure it restores the entry
// position and propagates that failure's diagnostic.
func Seq(rules ...Rule) Rule {
	return func(p *Parser) (interface{}, *Diag) {
		start := p.Pos
		vals := make([]interface{}, 0, len(rules))
		for _, r := range rules {
			v, d := r(p)
			if d != nil {
				p.Pos = start
				return nil, d
			}
			vals = append(vals, v)
		}
		return vals, nil
	}
}

// Alt tries each alternative from the same position. A branch's diagnostic
// survives only if every alternative fails: as soon as one succeeds, the
// failures (and the consumption that preceded them) of abandoned branches
// are discarded. On total failure the branch diagnostics are merged.
func Alt(rules ...Rule) Rule {
	return func(p *Parser) (interface{}, *Diag) {
		start := p.Pos
		var merged *Diag
		for _, r := range rules {
			p.Pos = start
			v, d := r(p)
			if d == nil {
				return v, nil
			}
			merged = Merge(merged, d)
		}
		p.Pos = start
		return nil, merged
	}
}

func quote(lit string) string {
	return "'" + lit + "'"
}
