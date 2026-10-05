// Package errmerge provides diagnostic merging for parallel / speculative
// parsers: when many branches fail at different input positions, the
// diagnostics are merged into a single farthest-failure report.
package errmerge

import (
	"fmt"
	"sort"
	"strings"
)

// Source describes one calling context that contributed an expectation.
type Source struct {
	// Rule is the innermost rule that reported the expectation; it is the
	// identity of the calling context.
	Rule string
	// Chain is the rule call chain from the entry rule down to Rule.
	Chain []string
}

// Diag is a (possibly merged) parse diagnostic.
//
// Positions always come from failure events (a token was expected but not
// found), never from consumption bookkeeping: a branch that merely consumed
// far before being abandoned leaves no trace here.
type Diag struct {
	Pos        int      // token index of the farthest failure; -1 when empty
	Expected   []string // sorted set of expected tokens at Pos
	Sources    []Source // shortest source chain per distinct calling context
	DepthLimit bool     // a recursion depth limit was hit at Pos
}

// Empty returns the absence of a diagnostic.
func Empty() Diag { return Diag{Pos: -1} }

// Valid reports whether d carries any diagnostic information.
func (d Diag) Valid() bool { return d.Pos >= 0 }

// expectAt builds a single-expectation diagnostic at pos. chain is the rule
// call stack at the failure point (may be nil).
func expectAt(pos int, expected string, chain []string) Diag {
	d := Diag{Pos: pos, Expected: []string{expected}}
	if len(chain) > 0 {
		d.Sources = []Source{{
			Rule:  chain[len(chain)-1],
			Chain: append([]string(nil), chain...),
		}}
	}
	return d
}

// Merge combines any number of diagnostics into one:
//  1. the farthest failure position wins outright;
//  2. at a tied position the expected-token sets are unioned;
//  3. for each distinct calling context the shortest source chain is kept.
func Merge(diags ...Diag) Diag {
	best := Empty()
	for _, d := range diags {
		best = merge2(best, d)
	}
	return best
}

func merge2(a, b Diag) Diag {
	if !a.Valid() {
		return normalize(b)
	}
	if !b.Valid() {
		return normalize(a)
	}
	switch {
	case a.Pos > b.Pos:
		return normalize(a)
	case b.Pos > a.Pos:
		return normalize(b)
	}
	return Diag{
		Pos:        a.Pos,
		Expected:   unionStrings(a.Expected, b.Expected),
		Sources:    mergeSources(a.Sources, b.Sources),
		DepthLimit: a.DepthLimit || b.DepthLimit,
	}
}

func normalize(d Diag) Diag {
	if !d.Valid() {
		return d
	}
	d.Expected = unionStrings(d.Expected)
	d.Sources = mergeSources(d.Sources)
	return d
}

func unionStrings(sets ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range sets {
		for _, v := range s {
			if _, ok := seen[v]; !ok {
				seen[v] = struct{}{}
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}

// mergeSources keeps, for each distinct calling context (identified by the
// innermost rule), the shortest source chain. The result is sorted for
// deterministic output.
func mergeSources(lists ...[]Source) []Source {
	byRule := map[string]Source{}
	for _, list := range lists {
		for _, s := range list {
			cur, ok := byRule[s.Rule]
			switch {
			case !ok:
				byRule[s.Rule] = s
			case len(s.Chain) < len(cur.Chain):
				byRule[s.Rule] = s
			case len(s.Chain) == len(cur.Chain) &&
				strings.Join(s.Chain, "\x00") < strings.Join(cur.Chain, "\x00"):
				byRule[s.Rule] = s
			}
		}
	}
	out := make([]Source, 0, len(byRule))
	for _, s := range byRule {
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rule != out[j].Rule {
			return out[i].Rule < out[j].Rule
		}
		return strings.Join(out[i].Chain, "\x00") < strings.Join(out[j].Chain, "\x00")
	})
	return out
}

// String renders the diagnostic in a human-readable form.
func (d Diag) String() string {
	if !d.Valid() {
		return "no error"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "parse error at token %d: expected %s", d.Pos, strings.Join(d.Expected, ", "))
	if d.DepthLimit {
		b.WriteString(" (recursion depth limit)")
	}
	for _, s := range d.Sources {
		fmt.Fprintf(&b, "\n  via %s", strings.Join(s.Chain, " -> "))
	}
	return b.String()
}
