package promqlengine

import (
	"fmt"
	"strconv"
)

// ParseExpr parses the supported PromQL subset into an evaluable AST.
//
// Supported grammar:
//
//	expr        := call | selector
//	call        := IDENT '(' expr ')' bracket?
//	selector    := (IDENT | IDENT? '{' matchers '}') modifiers bracket?
//	bracket     := '[' DURATION (':' DURATION?)? ']' modifiers
//	modifiers   := (('offset' DURATION) | ('@' NUMBER))*
//
// A bracket without ':' builds a range-vector selector (only valid on a
// vector selector); a bracket with ':' builds a subquery over any
// instant-vector expression. offset may be negative and @ may be combined
// with offset on both selectors and subqueries, independently at each level.
func ParseExpr(input string) (Expr, error) {
	tokens, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, p.unexpected("end of expression")
	}
	return expr, nil
}

// MustParseExpr is the panicking counterpart of ParseExpr for static setup.
func MustParseExpr(input string) Expr {
	expr, err := ParseExpr(input)
	if err != nil {
		panic(err)
	}
	return expr
}

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token { return p.tokens[p.pos] }
func (p *parser) next() token { t := p.tokens[p.pos]; p.pos++; return t }

func (p *parser) unexpected(want string) error {
	t := p.peek()
	return fmt.Errorf("promqlengine: expected %s but got %s at position %d", want, t, t.pos)
}

func (p *parser) isWord(word string) bool {
	t := p.peek()
	return t.kind == tokIdent && t.val == word
}

func (p *parser) parseExpr() (Expr, error) {
	// Function call when an identifier is immediately followed by '('.
	if p.peek().kind == tokIdent && p.tokens[p.pos+1].kind == tokLParen {
		return p.parseCallOrSelector()
	}
	return p.parseSelectorChain()
}

func (p *parser) parseCallOrSelector() (Expr, error) {
	name := p.next().val
	if p.peek().kind != tokLParen {
		// Bare identifier: a metric-name selector.
		return p.finishVectorSelector(name)
	}
	p.next() // '('
	fn, ok := GetFunction(name)
	if !ok {
		return nil, fmt.Errorf("promqlengine: unknown function %q", name)
	}
	arg, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokRParen {
		return nil, p.unexpected("')'")
	}
	p.next()

	expr := Expr(&Call{Func: fn, Args: []Expr{arg}})
	// A call may itself be the inner of a subquery.
	return p.attachBracket(expr)
}

// parseSelectorChain parses a vector selector (possibly brace-only), then any
// trailing range/subquery bracket.
func (p *parser) parseSelectorChain() (Expr, error) {
	var name string
	switch p.peek().kind {
	case tokIdent:
		name = p.next().val
	case tokLBrace:
		// Brace-only selector, no metric name.
	default:
		return nil, p.unexpected("metric name or '{'")
	}
	return p.finishVectorSelector(name)
}

func (p *parser) finishVectorSelector(name string) (Expr, error) {
	sel := &VectorSelector{Name: name}
	if p.peek().kind == tokLBrace {
		matchers, err := p.parseMatchers()
		if err != nil {
			return nil, err
		}
		sel.Matchers = matchers
	}
	if err := p.parseModifiers(&sel.timeModifier); err != nil {
		return nil, err
	}
	return p.attachBracket(sel)
}

func (p *parser) parseMatchers() ([]*Matcher, error) {
	p.next() // '{'
	var matchers []*Matcher
	for {
		labelTok := p.next()
		if labelTok.kind != tokIdent {
			return nil, fmt.Errorf("promqlengine: expected label name at position %d", labelTok.pos)
		}
		opTok := p.next()
		if opTok.kind != tokOp {
			return nil, fmt.Errorf("promqlengine: expected matcher operator at position %d", opTok.pos)
		}
		valTok := p.next()
		if valTok.kind != tokString {
			return nil, fmt.Errorf("promqlengine: expected quoted matcher value at position %d", valTok.pos)
		}
		op, err := parseMatchOp(opTok.val)
		if err != nil {
			return nil, err
		}
		m, err := NewMatcher(labelTok.val, op, valTok.val)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, m)
		if p.peek().kind == tokComma {
			p.next()
			continue
		}
		break
	}
	if p.peek().kind != tokRBrace {
		return nil, p.unexpected("'}'")
	}
	p.next()
	return matchers, nil
}

func parseMatchOp(s string) (MatchOp, error) {
	switch s {
	case "=":
		return MatchEqual, nil
	case "!=":
		return MatchNotEqual, nil
	case "=~":
		return MatchRegexp, nil
	case "!~":
		return MatchNotRegexp, nil
	default:
		return 0, fmt.Errorf("promqlengine: unknown matcher operator %q", s)
	}
}

// parseModifiers consumes any sequence of `offset D` and `@ T` modifiers in
// either order, rejecting duplicates.
func (p *parser) parseModifiers(m *timeModifier) error {
	var sawOffset, sawAt bool
	for {
		switch {
		case p.isWord("offset"):
			if sawOffset {
				return fmt.Errorf("promqlengine: duplicate offset modifier at position %d", p.peek().pos)
			}
			p.next()
			durTok := p.next()
			if durTok.kind != tokDuration && durTok.kind != tokNumber {
				return fmt.Errorf("promqlengine: offset expects a duration at position %d", durTok.pos)
			}
			d, err := ParseDuration(durTok.val)
			if err != nil {
				return err
			}
			m.Offset = d
			sawOffset = true
		case p.isWord("@"):
			if sawAt {
				return fmt.Errorf("promqlengine: duplicate @ modifier at position %d", p.peek().pos)
			}
			p.next()
			tsTok := p.next()
			if tsTok.kind != tokNumber {
				return fmt.Errorf("promqlengine: @ expects a unix timestamp at position %d", tsTok.pos)
			}
			atMs, err := parseUnixSeconds(tsTok.val)
			if err != nil {
				return err
			}
			m.HasAt = true
			m.AtMs = atMs
			sawAt = true
		default:
			return nil
		}
	}
}

// attachBracket consumes an optional [range] or [range:step] bracket and the
// modifiers following it.
func (p *parser) attachBracket(inner Expr) (Expr, error) {
	if p.peek().kind != tokLBracket {
		return inner, nil
	}
	p.next() // '['

	rangeTok := p.next()
	if rangeTok.kind != tokDuration {
		return nil, fmt.Errorf("promqlengine: expected range duration at position %d", rangeTok.pos)
	}
	rng, err := ParseDuration(rangeTok.val)
	if err != nil {
		return nil, err
	}

	if p.peek().kind != tokColon {
		// Plain range selector: inner must be a vector selector.
		if p.peek().kind != tokRBracket {
			return nil, p.unexpected("']' or ':'")
		}
		p.next()
		sel, ok := inner.(*VectorSelector)
		if !ok {
			return nil, fmt.Errorf("promqlengine: range bracket requires a vector selector")
		}
		rs := &RangeSelector{Vector: sel, Range: rng}
		// Range selectors carry no modifiers of their own in PromQL (the
		// modifiers live on the inner selector).
		return rs, nil
	}

	// Subquery: [range:step].
	p.next() // ':'
	if p.peek().kind == tokRBracket {
		return nil, fmt.Errorf("promqlengine: subquery requires an explicit step at position %d", p.peek().pos)
	}
	stepTok := p.next()
	if stepTok.kind != tokDuration {
		return nil, fmt.Errorf("promqlengine: expected subquery step duration at position %d", stepTok.pos)
	}
	step, err := ParseDuration(stepTok.val)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokRBracket {
		return nil, p.unexpected("']'")
	}
	p.next()

	sq := &SubqueryExpr{Inner: inner, Range: rng, Step: step}
	if err := p.parseModifiers(&sq.timeModifier); err != nil {
		return nil, err
	}
	return sq, nil
}

// parseUnixSeconds accepts an integer or float unix timestamp (seconds, as in
// PromQL text) and converts to milliseconds.
func parseUnixSeconds(s string) (int64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("promqlengine: invalid @ timestamp %q", s)
	}
	return int64(f * 1000), nil
}
