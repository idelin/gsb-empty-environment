package packer

import (
	"fmt"
	"io/ioutil"
	"strings"
)

// Action is the effect of a matching rule.
type Action int

const (
	Include Action = iota
	Exclude
)

// Rule is a single ordered include/exclude rule.
//
// Pattern syntax:
//   - '*', '?', '[...]' match within one path segment; '\' escapes.
//   - A whole segment '**' matches zero or more complete segments.
//   - A leading '/' or any internal '/' anchors the pattern to the root;
//     otherwise a single-segment pattern matches the basename at any depth.
//   - A trailing '/' restricts the rule to directories.
//   - Patterns are normalized lexically: empty segments, '.' segments and
//     resolvable '..' segments are collapsed, so './a//b' and 'a/b' are the
//     same rule.
type Rule struct {
	Action   Action
	Raw      string
	Segs     []string
	Anchored bool
	DirOnly  bool
}

func (r Rule) String() string {
	sign := "+ "
	if r.Action == Exclude {
		sign = "- "
	}
	return sign + r.Raw
}

// ParseRule compiles one pattern into a Rule.
func ParseRule(action Action, pattern string) (Rule, error) {
	r := Rule{Action: action, Raw: pattern}
	p := pattern
	anchored := false
	if strings.HasPrefix(p, "/") {
		anchored = true
		p = strings.TrimLeft(p, "/")
	}
	if strings.HasSuffix(p, "/") {
		r.DirOnly = true
		p = strings.TrimRight(p, "/")
	}
	if strings.Contains(p, "/") {
		anchored = true
	}
	if p == "" {
		return r, fmt.Errorf("empty pattern %q", pattern)
	}
	var segs []string
	for _, s := range strings.Split(p, "/") {
		switch s {
		case "", ".":
			continue
		case "..":
			n := len(segs)
			if n > 0 && segs[n-1] != ".." && !hasWildcard(segs[n-1]) {
				segs = segs[:n-1]
			} else {
				segs = append(segs, "..")
			}
		default:
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return r, fmt.Errorf("pattern %q matches nothing", pattern)
	}
	r.Segs = segs
	r.Anchored = anchored
	return r, nil
}

func hasWildcard(s string) bool {
	return strings.ContainsAny(s, "*?[\\")
}

// ParseRules parses a rule file body: one rule per line, each line a '+'
// (include) or '-' (exclude) sign followed by a pattern. Blank lines and
// lines starting with '#' are ignored.
func ParseRules(text string) ([]Rule, error) {
	var rules []Rule
	for ln, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var action Action
		switch line[0] {
		case '+':
			action = Include
		case '-':
			action = Exclude
		default:
			return nil, fmt.Errorf("line %d: rule must start with '+' or '-': %q", ln+1, line)
		}
		pat := strings.TrimSpace(line[1:])
		r, err := ParseRule(action, pat)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", ln+1, err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// LoadRulesFile reads and parses a rule file.
func LoadRulesFile(path string) ([]Rule, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseRules(string(data))
}

// Decision is the outcome of evaluating the rule set for one path.
type Decision struct {
	Included bool
	Rule     int // index of the winning rule, -1 when the default decided
}

// RuleSet is an ordered list of rules. Later rules override earlier ones.
// A rule that matches a directory also applies to everything beneath it,
// unless a later rule matches a deeper path and overrides it; this is what
// makes it possible to re-include paths inside an excluded directory.
type RuleSet struct {
	Rules          []Rule
	DefaultInclude bool

	includeIdx []int
}

// Build precomputes derived data. Call after setting Rules.
func (rs *RuleSet) Build() {
	rs.includeIdx = rs.includeIdx[:0]
	for i := range rs.Rules {
		if rs.Rules[i].Action == Include {
			rs.includeIdx = append(rs.includeIdx, i)
		}
	}
}

// Decide resolves the winning rule for a path given as clean segments.
// The winner is the highest-indexed rule matching the path itself or any
// of its ancestor directories; with no match the default applies.
func (rs *RuleSet) Decide(segs []string, isDir bool) Decision {
	best := -1
	for i := range rs.Rules {
		if ruleMatchesChain(&rs.Rules[i], segs, isDir) {
			best = i
		}
	}
	if best == -1 {
		return Decision{Included: rs.DefaultInclude, Rule: -1}
	}
	return Decision{Included: rs.Rules[best].Action == Include, Rule: best}
}

func ruleMatchesChain(r *Rule, segs []string, isDir bool) bool {
	for k := len(segs); k >= 1; k-- {
		if k == len(segs) && r.DirOnly && !isDir {
			continue
		}
		if r.matches(segs[:k]) {
			return true
		}
	}
	return false
}

func (r *Rule) matches(segs []string) bool {
	if r.Anchored {
		return matchSegments(r.Segs, segs)
	}
	return segMatch(r.Segs[0], segs[len(segs)-1])
}

// canReinclude reports whether any include rule with index greater than
// after could match some path at or below the directory dirSegs. When it
// returns false, nothing under that directory can ever be included and the
// subtree may be pruned.
func (rs *RuleSet) canReinclude(after int, dirSegs []string) bool {
	for _, i := range rs.includeIdx {
		if i <= after {
			continue
		}
		if couldMatchUnder(&rs.Rules[i], dirSegs) {
			return true
		}
	}
	return false
}

// couldMatchUnder conservatively reports whether the rule's pattern can
// match some path having dirSegs as a prefix. It never returns false when
// a match is possible.
func couldMatchUnder(r *Rule, dirSegs []string) bool {
	if !r.Anchored {
		return true
	}
	return feasible(r.Segs, dirSegs)
}

// feasible reports whether pattern pat can match a path that extends the
// concrete directory prefix dir.
func feasible(pat, dir []string) bool {
	if len(dir) == 0 {
		return true
	}
	if len(pat) == 0 {
		return false
	}
	if pat[0] == "**" {
		return feasible(pat[1:], dir) || feasible(pat, dir[1:])
	}
	if !segMatch(pat[0], dir[0]) {
		return false
	}
	return feasible(pat[1:], dir[1:])
}

// Describe renders the winning rule for output.
func (rs *RuleSet) Describe(ruleIdx int) string {
	if ruleIdx < 0 {
		return "default"
	}
	return fmt.Sprintf("rule %d: %s", ruleIdx, rs.Rules[ruleIdx].String())
}
