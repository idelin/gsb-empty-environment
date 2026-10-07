package main

import "sort"

// pred combines the old/new decisions for one path and reports whether the
// path belongs to the target region (expanded or shrunk).
type pred func(oldAllow, newAllow bool) bool

var (
	expandedPred pred = func(oldAllow, newAllow bool) bool { return !oldAllow && newAllow }
	shrunkPred   pred = func(oldAllow, newAllow bool) bool { return oldAllow && !newAllow }
)

type cover int

const (
	coverOut cover = iota
	coverIn
	coverMixed
)

// classify reports whether every path (length exactly MaxDepth) in the
// subtree under node is inside the target region (coverIn), outside it
// (coverOut), or mixed.
func (cfg *Config) classify(p pred, node []rune) cover {
	if len(node) == cfg.MaxDepth {
		s := string(node)
		if p(decide(cfg.Old, s, cfg.DefAllow), decide(cfg.New, s, cfg.DefAllow)) {
			return coverIn
		}
		return coverOut
	}
	sawIn, sawOut := false, false
	for _, ch := range cfg.Alphabet {
		child := make([]rune, len(node)+1)
		copy(child, node)
		child[len(node)] = ch
		switch cfg.classify(p, child) {
		case coverIn:
			sawIn = true
		case coverOut:
			sawOut = true
		default:
			return coverMixed
		}
		if sawIn && sawOut {
			return coverMixed
		}
	}
	if sawIn {
		return coverIn
	}
	return coverOut
}

// emitSet returns the unique minimal set of pairwise-disjoint prefixes whose
// covered paths (length exactly MaxDepth over the alphabet) equal the region
// selected by p. Ancestors of a maximal all-in node are always mixed, and any
// descendant would leave paths uncovered, so the maximal all-in nodes form
// the only disjoint exact cover — hence it is minimal.
func (cfg *Config) emitSet(p pred) []string {
	var out []string
	var walk func(node []rune)
	walk = func(node []rune) {
		switch cfg.classify(p, node) {
		case coverIn:
			out = append(out, string(node)) // "" only when everything matches
		case coverMixed:
			for _, ch := range cfg.Alphabet {
				child := make([]rune, len(node)+1)
				copy(child, node)
				child[len(node)] = ch
				walk(child)
			}
		}
	}
	walk(nil)
	sort.Strings(out)
	return out
}

// Diff computes the minimal disjoint prefix sets for permission expansion
// (old denies, new allows) and shrinkage (old allows, new denies).
func (cfg *Config) Diff() (expanded, shrunk []string) {
	return cfg.emitSet(expandedPred), cfg.emitSet(shrunkPred)
}
