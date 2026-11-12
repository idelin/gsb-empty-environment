package netsec

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

// Effect is the outcome of a sandbox rule.
type Effect string

const (
	// Allow permits an operation on a path.
	Allow Effect = "allow"
	// Deny rejects an operation on a path.
	Deny Effect = "deny"
)

// Action and Decision are aliases for Effect. They make the package convenient
// to use in code that talks about sandbox actions or decisions.
type (
	Action   = Effect
	Decision = Effect
)

// FilterType is an alias for FilterKind.
type FilterType = FilterKind

// FilterKind identifies how a filter value is interpreted.
type FilterKind string

const (
	// FilterLiteral matches exactly one path.
	FilterLiteral FilterKind = "literal"
	// FilterSubpath matches a path and all of its descendants.
	FilterSubpath FilterKind = "subpath"
	// FilterRegex matches a path when the regular expression matches the whole
	// path.
	FilterRegex FilterKind = "regex"
)

// Short aliases for FilterKind values.
const (
	KindLiteral = FilterLiteral
	KindSubpath = FilterSubpath
	KindRegex   = FilterRegex
)

// Filter is a path selection expression attached to a rule.
//
// A literal filter compares the complete path. A subpath filter matches the
// named path and every path below it, with a component boundary between the
// prefix and the remainder. A regex filter is matched against the complete
// path, as though the expression were surrounded by \A and \z.
type Filter struct {
	Kind  FilterKind
	Value string
}

// Literal constructs an exact-path filter.
func Literal(path string) Filter {
	return Filter{Kind: FilterLiteral, Value: path}
}

// Subpath constructs a directory-subtree filter. The named path itself is
// included in the subtree.
func Subpath(path string) Filter {
	return Filter{Kind: FilterSubpath, Value: path}
}

// Regex constructs a whole-path regular expression filter.
func Regex(expression string) Filter {
	return Filter{Kind: FilterRegex, Value: expression}
}

// NewFilter constructs a filter from an explicit kind and value.
func NewFilter(kind FilterKind, value string) Filter {
	return Filter{Kind: kind, Value: value}
}

// LiteralFilter is an alias for Literal.
func LiteralFilter(path string) Filter { return Literal(path) }

// SubpathFilter is an alias for Subpath.
func SubpathFilter(path string) Filter { return Subpath(path) }

// RegexFilter is an alias for Regex.
func RegexFilter(expression string) Filter { return Regex(expression) }

// NewLiteralFilter is an explicit helper equivalent to Literal.
func NewLiteralFilter(path string) Filter { return Literal(path) }

// NewSubpathFilter is an explicit helper equivalent to Subpath.
func NewSubpathFilter(path string) Filter { return Subpath(path) }

// NewRegexFilter is an explicit helper equivalent to Regex.
func NewRegexFilter(expression string) Filter { return Regex(expression) }

// Rule is one entry in an ordered sandbox policy. Rules are evaluated in slice
// order and the last matching rule wins.
//
// An operation that ends in "*" is a prefix wildcard. For example,
// "file-read*" matches "file-read", "file-read-data", and
// "file-read-metadata". An operation without a trailing "*" is exact.
type Rule struct {
	Action    Effect
	Operation string
	Filter    Filter
}

// NewRule constructs a rule from its action, operation, and filter.
func NewRule(action Effect, operation string, filter Filter) Rule {
	return Rule{Action: action, Operation: operation, Filter: filter}
}

// Matches reports whether the rule selects operation and path.
func (r Rule) Matches(operation, path string) bool {
	return ruleMatches(r, operation, path)
}

// Policy is an ordered collection of sandbox rules. A policy starts from
// "(deny default)": when no rule matches, the result is Deny.
type Policy struct {
	Rules []Rule
}

// NewPolicy returns a policy containing rules.
func NewPolicy(rules []Rule) *Policy {
	return &Policy{Rules: append([]Rule(nil), rules...)}
}

// New returns a policy from individual rules.
func New(rules ...Rule) *Policy {
	return &Policy{Rules: append([]Rule(nil), rules...)}
}

// Evaluate applies an operation and path to the policy. Rules are considered
// in order; the last matching rule determines the result. If nothing matches,
// Deny is returned, implementing "(deny default)".
func (p *Policy) Evaluate(operation, path string) Effect {
	if p == nil {
		return Deny
	}
	return Evaluate(p.Rules, operation, path)
}

// Decide is an alias for Evaluate.
func (p *Policy) Decide(operation, path string) Effect {
	return p.Evaluate(operation, path)
}

// Evaluate applies an operation and path to an ordered rule list.
func Evaluate(rules []Rule, operation, path string) Effect {
	result := Deny
	for _, rule := range rules {
		if ruleMatches(rule, operation, path) {
			result = rule.Action
		}
	}
	return result
}

// Decide is an alias for Evaluate.
func Decide(rules []Rule, operation, path string) Effect {
	return Evaluate(rules, operation, path)
}

// Allows reports whether evaluating an operation and path produces Allow.
func Allows(rules []Rule, operation, path string) bool {
	return Evaluate(rules, operation, path) == Allow
}

// Check evaluates a policy and also reports whether at least one rule matched.
func (p *Policy) Check(operation, path string) (Effect, bool) {
	if p == nil {
		return Deny, false
	}
	return Check(p.Rules, operation, path)
}

// Check evaluates rules and reports whether at least one rule matched.
func Check(rules []Rule, operation, path string) (Effect, bool) {
	result := Deny
	matched := false
	for _, rule := range rules {
		if ruleMatches(rule, operation, path) {
			result = rule.Action
			matched = true
		}
	}
	return result, matched
}

// Match reports whether the filter selects path.
func (f Filter) Match(path string) bool {
	switch f.Kind {
	case FilterLiteral:
		return f.Value == path
	case FilterSubpath:
		return subpathContains(f.Value, path)
	case FilterRegex:
		re, err := compileWholePathRegexp(f.Value)
		return err == nil && re.MatchString(path)
	default:
		return false
	}
}

// Matches is an alias for Match.
func (f Filter) Matches(path string) bool {
	return f.Match(path)
}

// Validate reports whether the filter is well formed.
func (f Filter) Validate() error {
	switch f.Kind {
	case FilterLiteral, FilterSubpath:
		return nil
	case FilterRegex:
		_, err := compileWholePathRegexp(f.Value)
		return err
	default:
		return fmt.Errorf("netsec: unknown filter kind %q", f.Kind)
	}
}

// Validate reports whether the rule is well formed.
func (r Rule) Validate() error {
	if r.Action != Allow && r.Action != Deny {
		return fmt.Errorf("netsec: unknown rule action %q", r.Action)
	}
	if r.Operation == "" {
		return errors.New("netsec: empty rule operation")
	}
	return r.Filter.Validate()
}

// Validate reports whether every rule in the policy is well formed.
func (p *Policy) Validate() error {
	if p == nil {
		return nil
	}
	for i, rule := range p.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("netsec: rule %d: %w", i, err)
		}
	}
	return nil
}

func ruleMatches(rule Rule, operation, path string) bool {
	if rule.Action != Allow && rule.Action != Deny {
		return false
	}
	return operationMatches(rule.Operation, operation) && rule.Filter.Match(path)
}

func operationMatches(pattern, operation string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(operation, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == operation
}

// OperationMatches reports whether a rule operation pattern matches a concrete
// operation. A trailing "*" makes the pattern a prefix wildcard.
func OperationMatches(pattern, operation string) bool {
	return operationMatches(pattern, operation)
}

// subpathContains reports whether candidate is root itself or a descendant of
// root. The separator is required between the root and a descendant, so
// "/a/b" does not contain "/a/bc".
func subpathContains(root, candidate string) bool {
	root = cleanSubpathRoot(root)
	if root == "/" {
		return strings.HasPrefix(candidate, "/")
	}
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

func cleanSubpathRoot(root string) string {
	for len(root) > 1 && strings.HasSuffix(root, "/") {
		root = strings.TrimSuffix(root, "/")
	}
	return root
}

func compileWholePathRegexp(expression string) (*regexp.Regexp, error) {
	// Putting the expression inside absolute anchors is necessary: Go's
	// MatchString reports a substring match by default, while sandbox regex
	// filters match the entire path.
	return regexp.Compile(`\A(?:` + expression + `)\z`)
}

type regexShape struct {
	literal      string
	isLiteral    bool
	anySuffix    bool
	requiresMore bool
	universal    bool
	valid        bool
}

// describeRegex extracts useful, conservative facts about an expression. It
// only reports properties that are certainly true.
func describeRegex(expression string) regexShape {
	re, err := syntax.Parse(expression, syntax.Perl)
	if err != nil {
		return regexShape{}
	}
	re = re.Simplify()

	if literal, ok := syntaxLiteral(re); ok {
		return regexShape{literal: literal, isLiteral: true, valid: true}
	}

	prefix, anySuffix, requiresMore := literalPrefixWithAnySuffix(re)
	if prefix != "" || anySuffix {
		if anySuffix && prefix == "" {
			return regexShape{anySuffix: true, requiresMore: requiresMore, universal: !requiresMore, valid: true}
		}
		return regexShape{literal: prefix, anySuffix: anySuffix, requiresMore: requiresMore, valid: true}
	}

	if regexMatchesEverything(re) {
		return regexShape{universal: true, valid: true}
	}
	return regexShape{valid: true}
}

func syntaxLiteral(re *syntax.Regexp) (string, bool) {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpBeginLine, syntax.OpEndLine, syntax.OpWordBoundary,
		syntax.OpNoWordBoundary:
		return "", true
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			return "", false
		}
		var b strings.Builder
		for _, r := range re.Rune {
			b.WriteRune(r)
		}
		return b.String(), true
	case syntax.OpCapture, syntax.OpConcat:
		var b strings.Builder
		for _, sub := range re.Sub {
			s, ok := syntaxLiteral(sub)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		}
		return b.String(), true
	case syntax.OpAlternate:
		if len(re.Sub) == 0 {
			return "", false
		}
		var first string
		for i, sub := range re.Sub {
			s, ok := syntaxLiteral(sub)
			if !ok {
				return "", false
			}
			if i == 0 {
				first = s
			} else if s != first {
				return "", false
			}
		}
		return first, true
	default:
		return "", false
	}

}

func literalPrefixWithAnySuffix(re *syntax.Regexp) (string, bool, bool) {
	parts := flattenConcat(re)
	if len(parts) == 0 {
		return "", false, false
	}
	i := 0
	var b strings.Builder
	for i < len(parts) {
		if isIgnorableRegexp(parts[i]) {
			i++
			continue
		}
		if s, ok := syntaxLiteral(parts[i]); ok {
			b.WriteString(s)
			i++
			continue
		}
		break
	}
	if i >= len(parts) {
		return "", false, false
	}
	rest := parts[i:]
	if rest[0].Op != syntax.OpStar && rest[0].Op != syntax.OpPlus {
		return "", false, false
	}
	if len(rest) > 2 {
		return "", false, false
	}
	if len(rest) == 2 && !isIgnorableRegexp(rest[1]) {
		return "", false, false
	}
	if !anyChar(reStarOperand(rest[0])) {
		return "", false, false
	}
	return b.String(), true, rest[0].Op == syntax.OpPlus
}

func flattenConcat(re *syntax.Regexp) []*syntax.Regexp {
	if re.Op != syntax.OpConcat {
		return []*syntax.Regexp{re}
	}
	return re.Sub
}

func isIgnorableRegexp(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpBeginLine, syntax.OpEndLine, syntax.OpWordBoundary,
		syntax.OpNoWordBoundary:
		return true
	default:
		return false
	}
}

func reStarOperand(re *syntax.Regexp) *syntax.Regexp {
	if len(re.Sub) == 1 {
		return re.Sub[0]
	}
	return nil
}

func anyChar(re *syntax.Regexp) bool {
	if re == nil {
		return false
	}
	if re.Op == syntax.OpAnyChar || re.Op == syntax.OpAnyCharNotNL {
		return true
	}
	if re.Op == syntax.OpCharClass && re.Flags&syntax.FoldCase == 0 {
		// A character class that covers every legal rune is equivalent to
		// "any character" for this purpose.
		if len(re.Rune) >= 2 {
			return re.Rune[0] <= 0 && re.Rune[len(re.Rune)-1] >= 0x10FFFF
		}
	}
	return false
}

func regexMatchesEverything(re *syntax.Regexp) bool {
	parts := flattenConcat(re)
	seenAny := false
	for _, part := range parts {
		if isIgnorableRegexp(part) {
			continue
		}
		if part.Op != syntax.OpStar && part.Op != syntax.OpPlus {
			return false
		}
		if !anyChar(reStarOperand(part)) {
			return false
		}
		seenAny = true
	}
	return seenAny
}
