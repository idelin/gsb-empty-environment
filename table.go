// Package foldindex builds a search index over text that has been folded
// with a caller-supplied character folding table. Folding rules may expand
// one character into many (e.g. "ß" -> "ss") or replace a combination of
// characters (e.g. "..." -> "…"). Search hits produced on the folded text
// are always mapped back to intervals of the original document.
package foldindex

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Rule maps a sequence of one or more characters (From) to its folded
// replacement (To). To may be empty, which deletes the matched sequence
// from the folded text.
type Rule struct {
	From string
	To   string
}

// Table is an immutable, versioned folding table. The version identifies
// the rule set: indexes and cursors built from one version must never be
// mixed with another.
type Table struct {
	version string
	rules   []compiledRule // sorted by descending From length (longest match wins)
	byFirst map[rune][]int // first rune of From -> indexes into rules
}

type compiledRule struct {
	from []rune
	to   []rune
}

// NewTable compiles a folding table. version must be non-empty and must
// not contain '|'. Duplicate From entries are rejected.
func NewTable(version string, rules []Rule) (*Table, error) {
	if version == "" {
		return nil, errors.New("foldindex: table version must not be empty")
	}
	if strings.ContainsRune(version, '|') {
		return nil, errors.New("foldindex: table version must not contain '|'")
	}
	seen := make(map[string]bool, len(rules))
	compiled := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		if r.From == "" {
			return nil, errors.New("foldindex: rule with empty From")
		}
		if seen[r.From] {
			return nil, fmt.Errorf("foldindex: duplicate rule for %q", r.From)
		}
		seen[r.From] = true
		compiled = append(compiled, compiledRule{
			from: []rune(r.From),
			to:   []rune(r.To),
		})
	}
	sort.SliceStable(compiled, func(i, j int) bool {
		return len(compiled[i].from) > len(compiled[j].from)
	})
	byFirst := make(map[rune][]int)
	for i, r := range compiled {
		first := r.from[0]
		byFirst[first] = append(byFirst[first], i)
	}
	return &Table{version: version, rules: compiled, byFirst: byFirst}, nil
}

// Version returns the table version supplied to NewTable.
func (t *Table) Version() string { return t.version }

// matchAt returns the longest rule matching in[pos:], if any.
func (t *Table) matchAt(in []rune, pos int) (compiledRule, bool) {
	for _, idx := range t.byFirst[in[pos]] {
		r := t.rules[idx]
		if pos+len(r.from) <= len(in) && runesEqual(in[pos:pos+len(r.from)], r.from) {
			return r, true
		}
	}
	return compiledRule{}, false
}

// fold applies the table to in, returning the folded runes and one unit
// per consumed input segment describing the folded/original correspondence.
func (t *Table) fold(in []rune) ([]rune, []unit) {
	out := make([]rune, 0, len(in))
	units := make([]unit, 0, len(in))
	i := 0
	for i < len(in) {
		if r, ok := t.matchAt(in, i); ok {
			fs := len(out)
			out = append(out, r.to...)
			units = append(units, unit{fs, len(out), i, i + len(r.from)})
			i += len(r.from)
		} else {
			fs := len(out)
			out = append(out, in[i])
			units = append(units, unit{fs, fs + 1, i, i + 1})
			i++
		}
	}
	return out, units
}

// foldString folds s without keeping the unit mapping (used for queries).
func (t *Table) foldString(s string) []rune {
	folded, _ := t.fold([]rune(s))
	return folded
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
