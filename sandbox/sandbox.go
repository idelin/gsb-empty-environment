// Package sandbox evaluates macOS-style sandbox configurations before they
// are published. A configuration starts from an implicit "(deny default)";
// rules are evaluated in order of appearance and the last matching rule
// decides the outcome. The package reports the effective decision for an
// (operation, path) pair and flags deny rules that are fully shadowed by
// later rules and therefore can never take effect.
package sandbox

import (
	"fmt"
	"regexp"
	"strings"
)

// Action is the effect of a matching rule.
type Action int

const (
	Deny Action = iota
	Allow
)

func (a Action) String() string {
	if a == Allow {
		return "allow"
	}
	return "deny"
}

// FilterKind selects how a rule's filter value is matched against a path.
type FilterKind int

const (
	// Literal matches exactly one path.
	Literal FilterKind = iota
	// Subpath matches the path itself and everything beneath it. The
	// boundary is a path separator: subpath "/a/b" matches "/a/b" and
	// "/a/b/c" but not "/a/bc".
	Subpath
	// Regex matches when the pattern matches the entire path.
	Regex
)

// Filter is a compiled path matcher.
type Filter struct {
	Kind  FilterKind
	Value string
	re    *regexp.Regexp
}

// LiteralFilter matches exactly path.
func LiteralFilter(path string) Filter {
	return Filter{Kind: Literal, Value: path}
}

// SubpathFilter matches root and every path beneath it.
func SubpathFilter(root string) Filter {
	return Filter{Kind: Subpath, Value: normalizeRoot(root)}
}

// RegexFilter matches paths for which pattern matches the whole string.
// The pattern is anchored on both ends; an invalid pattern yields an error.
func RegexFilter(pattern string) (Filter, error) {
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return Filter{}, fmt.Errorf("invalid regex filter %q: %w", pattern, err)
	}
	return Filter{Kind: Regex, Value: pattern, re: re}, nil
}

// MustRegex is RegexFilter for patterns known to be valid; it panics on
// invalid patterns.
func MustRegex(pattern string) Filter {
	f, err := RegexFilter(pattern)
	if err != nil {
		panic(err)
	}
	return f
}

func normalizeRoot(root string) string {
	for len(root) > 1 && strings.HasSuffix(root, "/") {
		root = strings.TrimSuffix(root, "/")
	}
	return root
}

// subpathMatch reports whether path is root itself or lies beneath root.
func subpathMatch(root, path string) bool {
	if root == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == root || strings.HasPrefix(path, root+"/")
}

func (f Filter) matches(path string) bool {
	switch f.Kind {
	case Literal:
		return path == f.Value
	case Subpath:
		return subpathMatch(f.Value, path)
	case Regex:
		return f.re.MatchString(path)
	}
	return false
}

// covers reports whether every path matched by other is also matched by f.
// The check is exact for literal/subpath combinations and conservative for
// regex filters (identical patterns, or a literal tested against the regex).
func (f Filter) covers(other Filter) bool {
	switch f.Kind {
	case Literal:
		return other.Kind == Literal && f.Value == other.Value
	case Subpath:
		switch other.Kind {
		case Literal:
			return subpathMatch(f.Value, other.Value)
		case Subpath:
			return subpathMatch(f.Value, other.Value)
		}
		return false
	case Regex:
		switch other.Kind {
		case Literal:
			return f.re.MatchString(other.Value)
		case Regex:
			return f.Value == other.Value
		case Subpath:
			// Only a match-everything pattern provably covers a subpath.
			return f.Value == ".*" || f.Value == "^.*$"
		}
	}
	return false
}

// Rule is one line of a sandbox configuration. Operation is either an exact
// operation name ("file-read-data") or, when it ends in '*', a wildcard
// matching every operation with that prefix ("file-read*").
type Rule struct {
	Action    Action
	Operation string
	Filter    Filter
}

// wildcard reports whether the operation name is a prefix wildcard.
func wildcard(op string) bool {
	return strings.HasSuffix(op, "*")
}

func prefixOf(op string) string {
	return strings.TrimSuffix(op, "*")
}

// opMatches reports whether the rule's operation matcher covers op.
func (r Rule) opMatches(op string) bool {
	if wildcard(r.Operation) {
		return strings.HasPrefix(op, prefixOf(r.Operation))
	}
	return r.Operation == op
}

// opCovers reports whether every operation matched by other is also matched
// by matcher op.
func opCovers(op, other string) bool {
	switch {
	case wildcard(op) && wildcard(other):
		return strings.HasPrefix(prefixOf(other), prefixOf(op))
	case wildcard(op):
		return strings.HasPrefix(other, prefixOf(op))
	case wildcard(other):
		// An exact name can never cover an unbounded prefix set.
		return false
	default:
		return op == other
	}
}

func (r Rule) matches(op, path string) bool {
	return r.opMatches(op) && r.Filter.matches(path)
}

// covers reports whether every (operation, path) pair matched by other is
// also matched by r.
func (r Rule) covers(other Rule) bool {
	return opCovers(r.Operation, other.Operation) && r.Filter.covers(other.Filter)
}

// Policy is an ordered sandbox configuration with an implicit leading
// "(deny default)".
type Policy struct {
	Rules []Rule
}

// Decision is the outcome of evaluating a policy for one (operation, path)
// pair.
type Decision struct {
	Allowed bool
	// Rule is the index of the deciding rule, or -1 when no rule matched
	// and the implicit default deny decided.
	Rule int
}

// Decide evaluates the policy for (op, path). Rules are consulted in order
// of appearance and the last matching rule wins; when nothing matches the
// implicit "(deny default)" applies.
func (p *Policy) Decide(op, path string) Decision {
	d := Decision{Allowed: false, Rule: -1}
	for i := range p.Rules {
		if p.Rules[i].matches(op, path) {
			d.Allowed = p.Rules[i].Action == Allow
			d.Rule = i
		}
	}
	return d
}

// ShadowedDenies returns the indices of deny rules that can never decide an
// outcome because every (operation, path) pair they match is also matched
// by some later rule. A deny is reported regardless of the shadowing rule's
// action: a later broad allow silently re-opens what the deny meant to
// close, and a later equivalent deny makes it redundant.
//
// The check is sound (no false positives) and exact for this matcher
// language: a prefix-wildcard operation set or a subpath/literal path set
// is infinite, so a finite union of later rules covers such a set only when
// a single later rule already covers both dimensions.
func (p *Policy) ShadowedDenies() []int {
	var shadowed []int
	for i := range p.Rules {
		if p.Rules[i].Action != Deny {
			continue
		}
		for j := i + 1; j < len(p.Rules); j++ {
			if p.Rules[j].covers(p.Rules[i]) {
				shadowed = append(shadowed, i)
				break
			}
		}
	}
	return shadowed
}
