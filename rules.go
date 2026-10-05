// Package pkgincr implements an incremental packaging tool driven by an
// ordered list of include/exclude path rules.
//
// Rule semantics:
//   - Rules are evaluated in order; for any given path the LAST matching
//     rule wins (later rules override earlier ones).
//   - A rule also "matches" the descendants of any directory it matches,
//     so excluding a directory excludes its whole subtree ...
//   - ... but a later rule that matches a path below the excluded
//     directory re-includes that path ("sub-rules can re-include").
//   - A path matched by no rule at all (neither itself nor any ancestor)
//     is included by default.
//
// Pattern syntax (gitignore-flavoured):
//   - "+ pattern" includes, "- pattern" excludes; '#' starts a comment.
//   - A pattern containing no '/' (other than a trailing one) matches the
//     base name of a file or directory at any depth.
//   - A pattern containing '/' anywhere else is anchored at the root.
//   - A trailing '/' restricts the pattern to directories.
//   - '*', '?' and '[...]' work as in path.Match within one segment.
//   - '**' as a whole segment matches zero or more path segments.
//   - Redundant '.', '//' and leading './' are normalized away, so
//     different spellings of the same path behave identically.
package pkgincr

import (
	"fmt"
	"path"
	"strings"
)

// RuleKind is the decision a rule carries.
type RuleKind int

const (
	// Include marks matching paths to be packaged.
	Include RuleKind = iota
	// Exclude marks matching paths to be left out.
	Exclude
)

func (k RuleKind) String() string {
	if k == Include {
		return "include"
	}
	return "exclude"
}

// Rule is one parsed include/exclude rule.
type Rule struct {
	Kind    RuleKind
	Pattern string // normalized, human-readable form
	Line    int    // 1-based line number in the source, 0 if synthetic

	anchored bool     // pattern is anchored at the root
	dirOnly  bool     // pattern only matches directories
	segments []string // normalized pattern segments; "**" kept verbatim
}

// String renders the rule in its canonical "+/- pattern" form.
func (r Rule) String() string {
	sigil := "+"
	if r.Kind == Exclude {
		sigil = "-"
	}
	return fmt.Sprintf("%s %s", sigil, r.Pattern)
}

// ParseRule parses a single rule line. It returns (nil, nil) for blank
// lines and comments.
func ParseRule(line string, lineNo int) (*Rule, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil, nil
	}
	r := &Rule{Line: lineNo}
	switch trimmed[0] {
	case '+':
		r.Kind = Include
	case '-':
		r.Kind = Exclude
	default:
		return nil, fmt.Errorf("line %d: rule must start with '+' or '-': %q", lineNo, line)
	}
	pat := strings.TrimSpace(trimmed[1:])
	if pat == "" {
		return nil, fmt.Errorf("line %d: empty pattern", lineNo)
	}
	if err := r.compile(pat); err != nil {
		return nil, fmt.Errorf("line %d: %v", lineNo, err)
	}
	return r, nil
}

// ParseRules parses a whole rule file.
func ParseRules(text string) ([]Rule, error) {
	var rules []Rule
	for i, line := range strings.Split(text, "\n") {
		r, err := ParseRule(line, i+1)
		if err != nil {
			return nil, err
		}
		if r != nil {
			rules = append(rules, *r)
		}
	}
	return rules, nil
}

// compile normalizes a raw pattern into segments. Different textual
// spellings of the same path ("./a/b", "a//b", "a/./b", "/a/b") compile
// to the same segments.
func (r *Rule) compile(pat string) error {
	if pat != "/" && strings.HasSuffix(pat, "/") {
		r.dirOnly = true
		pat = strings.TrimRight(pat, "/")
	}
	// gitignore convention: a slash at the start or in the middle anchors
	// the pattern at the root; otherwise it matches a base name.
	r.anchored = strings.HasPrefix(pat, "/") || strings.Contains(pat, "/")
	var segs []string
	for _, s := range strings.Split(pat, "/") {
		switch s {
		case "", ".":
			continue
		case "..":
			return fmt.Errorf("pattern %q: '..' segments are not allowed", pat)
		}
		segs = append(segs, s)
	}
	if len(segs) == 0 {
		return fmt.Errorf("pattern %q matches nothing", pat)
	}
	for _, s := range segs {
		if s == "**" {
			continue
		}
		if _, err := path.Match(s, ""); err != nil {
			return fmt.Errorf("bad pattern segment %q: %v", s, err)
		}
	}
	r.segments = segs
	var b strings.Builder
	if r.anchored {
		b.WriteByte('/')
	}
	b.WriteString(strings.Join(segs, "/"))
	if r.dirOnly {
		b.WriteByte('/')
	}
	r.Pattern = b.String()
	return nil
}

// Matches reports whether the rule matches the path described by segs
// (cleaned, root-relative path segments). isDir must reflect the type of
// the path itself; symlinks are never directories for this purpose.
func (r *Rule) Matches(segs []string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	if !r.anchored {
		// Unanchored patterns have exactly one segment and match the
		// base name at any depth.
		return segmentMatch(r.segments[0], segs[len(segs)-1])
	}
	return matchSegments(r.segments, segs)
}

// segmentMatch matches one path segment against one pattern segment.
// "**" degenerates to "match anything" in single-segment context.
func segmentMatch(pat, name string) bool {
	if pat == "**" {
		return true
	}
	ok, err := path.Match(pat, name)
	return err == nil && ok
}

// matchSegments matches an anchored pattern against a whole path.
// "**" consumes zero or more segments.
func matchSegments(pat, segs []string) bool {
	memo := make(map[[2]int]bool)
	var f func(i, j int) bool
	f = func(i, j int) bool {
		key := [2]int{i, j}
		if v, ok := memo[key]; ok {
			return v
		}
		var res bool
		switch {
		case i == len(pat):
			res = j == len(segs)
		case pat[i] == "**":
			res = f(i+1, j) || (j < len(segs) && f(i, j+1))
		default:
			res = j < len(segs) && segmentMatch(pat[i], segs[j]) && f(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	return f(0, 0)
}

// CanMatchDescendant reports whether the rule could match some path
// strictly below the directory described by dirSegs. It is used for
// traversal pruning and is conservative: it may answer true when no real
// path matches, but never answers false when one could.
func (r *Rule) CanMatchDescendant(dirSegs []string) bool {
	if !r.anchored {
		// A base-name pattern can match at any depth below any directory.
		return true
	}
	pat := r.segments
	memo := make(map[[2]int]bool)
	var f func(i, j int) bool
	f = func(i, j int) bool {
		if j == len(dirSegs) {
			// The whole directory prefix is consumed; the remaining
			// pattern must be able to match at least one more segment.
			// Any non-empty remainder can ("**" takes one segment,
			// anything else needs exactly one).
			return i < len(pat)
		}
		if i == len(pat) {
			return false
		}
		key := [2]int{i, j}
		if v, ok := memo[key]; ok {
			return v
		}
		var res bool
		if pat[i] == "**" {
			res = f(i+1, j) || f(i, j+1)
		} else {
			res = segmentMatch(pat[i], dirSegs[j]) && f(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	return f(0, 0)
}

// LastMatch returns the index of the last rule matching the path itself,
// or -1 if no rule matches.
func LastMatch(rules []Rule, segs []string, isDir bool) int {
	idx := -1
	for i := range rules {
		if rules[i].Matches(segs, isDir) {
			idx = i
		}
	}
	return idx
}
