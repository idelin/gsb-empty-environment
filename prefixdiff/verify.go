package main

import (
	"fmt"
	"strings"
)

// EnumeratePaths returns every path over the alphabet: all strings of length
// exactly maxDepth.
func EnumeratePaths(alphabet []rune, maxDepth int) []string {
	total := 1
	for d := 0; d < maxDepth; d++ {
		total *= len(alphabet)
	}
	paths := make([]string, 0, total)
	var walk func(node []rune)
	walk = func(node []rune) {
		if len(node) == maxDepth {
			paths = append(paths, string(node))
			return
		}
		for _, ch := range alphabet {
			child := make([]rune, len(node)+1)
			copy(child, node)
			child[len(node)] = ch
			walk(child)
		}
	}
	walk(nil)
	return paths
}

// PathCount returns len(EnumeratePaths(...)) without building the list.
func PathCount(alphabetSize, maxDepth int) int64 {
	var total int64 = 1
	for d := 0; d < maxDepth; d++ {
		total *= int64(alphabetSize)
		if total < 0 {
			return -1 // overflow guard
		}
	}
	return total
}

func coveredBy(set []string, s string) bool {
	for _, p := range set {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func displayPrefix(p string) string {
	if p == "" {
		return "<root>"
	}
	return p
}

// Verify brute-force checks the diff result against the rule semantics by
// enumerating every path. It returns a list of problems (empty means OK):
//  1. coverage: expanded/shrunk sets cover exactly the paths whose decision
//     changed in the corresponding direction;
//  2. disjointness: no prefix in a set is a prefix of another, and the two
//     sets do not overlap;
//  3. minimality: no prefix is redundant (removing it would lose a path).
func (cfg *Config) Verify(expanded, shrunk []string) []string {
	var problems []string
	paths := EnumeratePaths(cfg.Alphabet, cfg.MaxDepth)

	// 1. exact coverage on every enumerated path.
	for _, s := range paths {
		oldAllow := decide(cfg.Old, s, cfg.DefAllow)
		newAllow := decide(cfg.New, s, cfg.DefAllow)
		if got, want := coveredBy(expanded, s), !oldAllow && newAllow; got != want {
			problems = append(problems, fmt.Sprintf("coverage: path %q expanded=%v, want %v (old=%v new=%v)", s, got, want, oldAllow, newAllow))
		}
		if got, want := coveredBy(shrunk, s), oldAllow && !newAllow; got != want {
			problems = append(problems, fmt.Sprintf("coverage: path %q shrunk=%v, want %v (old=%v new=%v)", s, got, want, oldAllow, newAllow))
		}
	}

	// 2. disjointness within each set and across both sets.
	checkDisjoint := func(name string, set []string) {
		for i := 0; i < len(set); i++ {
			for j := i + 1; j < len(set); j++ {
				a, b := set[i], set[j]
				if a == b {
					problems = append(problems, fmt.Sprintf("disjoint: %s has duplicate prefix %q", name, displayPrefix(a)))
				} else if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
					problems = append(problems, fmt.Sprintf("disjoint: %s prefixes %q and %q overlap", name, displayPrefix(a), displayPrefix(b)))
				}
			}
		}
	}
	checkDisjoint("expanded", expanded)
	checkDisjoint("shrunk", shrunk)
	for _, e := range expanded {
		for _, s := range shrunk {
			if strings.HasPrefix(e, s) || strings.HasPrefix(s, e) {
				problems = append(problems, fmt.Sprintf("disjoint: expanded prefix %q overlaps shrunk prefix %q", displayPrefix(e), displayPrefix(s)))
			}
		}
	}

	// 3. no redundant prefix: each prefix must solely cover at least one path.
	checkRedundant := func(name string, set []string) {
		for i, p := range set {
			rest := make([]string, 0, len(set)-1)
			rest = append(rest, set[:i]...)
			rest = append(rest, set[i+1:]...)
			unique := false
			for _, path := range paths {
				if strings.HasPrefix(path, p) && !coveredBy(rest, path) {
					unique = true
					break
				}
			}
			if !unique {
				problems = append(problems, fmt.Sprintf("redundant: %s prefix %q covers no path on its own", name, displayPrefix(p)))
			}
		}
	}
	checkRedundant("expanded", expanded)
	checkRedundant("shrunk", shrunk)

	return problems
}
