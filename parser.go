package errmerge

import "fmt"

// Token is a lexed input symbol. Kind is what grammar terminals match on.
type Token struct {
	Kind string
	Text string
	Off  int // byte offset in the source, for display
}

// Result is the outcome of running a node at some position.
type Result struct {
	OK   bool
	Pos  int  // position after the match (or where the failure surfaced)
	Diag Diag // best diagnostic seen inside this subtree
}

// Node is a grammar fragment. It is purely functional over positions: it
// receives the start position and returns the end position, so a failed or
// abandoned branch cannot leak its consumption into the caller.
type Node func(p *Parser, pos int) Result

// Parser holds the input and shared parse state.
type Parser struct {
	Tokens   []Token
	MaxDepth int // maximum rule nesting depth; 0 means unlimited

	stack []string
}

// Tok matches a single token of the given kind.
func Tok(kind string) Node {
	return func(p *Parser, pos int) Result {
		if pos < len(p.Tokens) && p.Tokens[pos].Kind == kind {
			return Result{OK: true, Pos: pos + 1}
		}
		return Result{Pos: pos, Diag: expectAt(pos, kind, p.stack)}
	}
}

// Rule wraps n in a named rule, tracking the call chain for diagnostics and
// enforcing MaxDepth. Hitting the depth limit is a fatal failure: it
// propagates through Seq/Alt/Many/Opt without being swallowed or retried.
func Rule(name string, n Node) Node {
	return func(p *Parser, pos int) Result {
		if p.MaxDepth > 0 && len(p.stack) >= p.MaxDepth {
			d := expectAt(pos, fmt.Sprintf("<recursion depth limit %d>", p.MaxDepth), p.stack)
			d.DepthLimit = true
			return Result{Pos: pos, Diag: d}
		}
		p.stack = append(p.stack, name)
		r := n(p, pos)
		p.stack = p.stack[:len(p.stack)-1]
		return r
	}
}

// Lazy defers dereferencing a node, enabling recursive grammars.
func Lazy(n *Node) Node {
	return func(p *Parser, pos int) Result { return (*n)(p, pos) }
}

// Seq runs nodes in order, failing at the first failing child. Diagnostics
// of all children run so far are merged.
func Seq(nodes ...Node) Node {
	return func(p *Parser, pos int) Result {
		cur := pos
		diag := Empty()
		for _, n := range nodes {
			r := n(p, cur)
			diag = Merge(diag, r.Diag)
			if !r.OK {
				return Result{Pos: cur, Diag: diag}
			}
			cur = r.Pos
		}
		return Result{OK: true, Pos: cur, Diag: diag}
	}
}

// Alt tries each branch from the same start position (ordered choice).
// Failed branches contribute their diagnostics to the merge; their
// consumption is discarded because the next branch restarts at pos. The
// first succeeding branch commits.
func Alt(nodes ...Node) Node {
	return func(p *Parser, pos int) Result {
		diag := Empty()
		for _, n := range nodes {
			r := n(p, pos)
			diag = Merge(diag, r.Diag)
			if r.Diag.DepthLimit {
				return Result{Pos: pos, Diag: diag}
			}
			if r.OK {
				return Result{OK: true, Pos: r.Pos, Diag: diag}
			}
		}
		return Result{Pos: pos, Diag: diag}
	}
}

// Many runs n greedily until it fails or makes no progress. It always
// succeeds; the final failed attempt's diagnostic is merged in.
func Many(n Node) Node {
	return func(p *Parser, pos int) Result {
		cur := pos
		diag := Empty()
		for {
			r := n(p, cur)
			diag = Merge(diag, r.Diag)
			if r.Diag.DepthLimit {
				return Result{Pos: cur, Diag: diag}
			}
			if !r.OK || r.Pos == cur {
				break
			}
			cur = r.Pos
		}
		return Result{OK: true, Pos: cur, Diag: diag}
	}
}

// Opt runs n once and succeeds either way, merging n's diagnostic.
func Opt(n Node) Node {
	return func(p *Parser, pos int) Result {
		r := n(p, pos)
		if r.Diag.DepthLimit {
			return Result{Pos: pos, Diag: r.Diag}
		}
		if !r.OK {
			return Result{OK: true, Pos: pos, Diag: r.Diag}
		}
		return r
	}
}

// Parse runs n at position 0 and requires the whole input to be consumed.
// On success the returned Result has OK set and an empty Diag; otherwise
// Diag holds the merged farthest-failure diagnostic, including an <EOF>
// expectation when a prefix parse left trailing input.
func (p *Parser) Parse(n Node) Result {
	p.stack = p.stack[:0]
	r := n(p, 0)
	switch {
	case !r.OK:
		return Result{Pos: r.Pos, Diag: r.Diag}
	case r.Pos != len(p.Tokens):
		return Result{
			Pos:  r.Pos,
			Diag: Merge(r.Diag, expectAt(r.Pos, "<EOF>", nil)),
		}
	default:
		return Result{OK: true, Pos: r.Pos}
	}
}
