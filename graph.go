package main

import (
	"sort"
	"strings"
)

// Symbol is a node in the call graph (a function or a test).
type Symbol struct {
	Name string `json:"name"`
	Test bool   `json:"test,omitempty"`
}

// Edge is a single call site.
// Exactly one of the following shapes applies:
//   - static:     To is set
//   - dynamic:    Dynamic is true and Candidates holds the possible callees
//   - unresolved: Unresolved is true; NameHint optionally narrows expansion
type Edge struct {
	From       string   `json:"from"`
	To         string   `json:"to,omitempty"`
	Dynamic    bool     `json:"dynamic,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Unresolved bool     `json:"unresolved,omitempty"`
	NameHint   string   `json:"nameHint,omitempty"`
}

// Graph is a call graph over symbols.
type Graph struct {
	Symbols map[string]Symbol
	Edges   map[string][]Edge // caller -> outgoing call sites
}

func NewGraph() *Graph {
	return &Graph{
		Symbols: make(map[string]Symbol),
		Edges:   make(map[string][]Edge),
	}
}

func (g *Graph) AddSymbol(s Symbol) {
	g.Symbols[s.Name] = s
}

func (g *Graph) AddEdge(e Edge) {
	g.Edges[e.From] = append(g.Edges[e.From], e)
}

// shortName returns the symbol name without its package qualifier.
func shortName(sym string) string {
	if i := strings.LastIndex(sym, "."); i >= 0 {
		return sym[i+1:]
	}
	return sym
}

// pkgOf returns the package qualifier of a symbol ("" if none).
func pkgOf(sym string) string {
	if i := strings.LastIndex(sym, "."); i >= 0 {
		return sym[:i]
	}
	return ""
}

// effectiveCallees resolves an edge to the set of symbols it may reach,
// applying the explicit conservative rules for calls that could not be
// resolved statically:
//
//	R1: an unresolved/dynamic call with a name hint expands to every
//	    symbol whose short name matches the hint.
//	R2: an unresolved/dynamic call with no hint (or a hint matching
//	    nothing) expands to every symbol in the caller's package.
//
// Dynamic calls with a candidate set never expand beyond that set.
func (g *Graph) effectiveCallees(e Edge) []string {
	if e.To != "" && !e.Unresolved {
		return []string{e.To}
	}
	if len(e.Candidates) > 0 {
		return dedup(e.Candidates)
	}
	if e.NameHint != "" {
		var out []string
		for name := range g.Symbols {
			if shortName(name) == e.NameHint {
				out = append(out, name)
			}
		}
		if len(out) > 0 {
			sort.Strings(out)
			return out
		}
	}
	pkg := pkgOf(e.From)
	var out []string
	for name := range g.Symbols {
		if pkgOf(name) == pkg {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// canonicalKey normalizes an edge for old/new comparison.
func (e Edge) canonicalKey() string {
	c := append([]string(nil), e.Candidates...)
	sort.Strings(c)
	return strings.Join([]string{
		e.To,
		boolStr(e.Dynamic),
		strings.Join(c, ","),
		boolStr(e.Unresolved),
		e.NameHint,
	}, "|")
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
