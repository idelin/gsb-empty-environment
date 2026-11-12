package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is an Ingress path match kind.
type Kind uint8

const (
	// Exact matches only the canonical rule path.
	Exact Kind = iota + 1
	// Prefix matches the rule path and paths below it on an element boundary.
	Prefix
)

// Rule maps an Ingress path to a backend target.
type Rule struct {
	Kind   Kind
	Path   string
	Target string
}

// Match is the result of resolving a request path. Path is the canonical
// request path and must also be the path sent to the selected backend.
// Rule.Path is the canonical rule path, not necessarily the configured text.
type Match struct {
	Path    string
	Rule    Rule
	Matched bool
}

// Router resolves canonical request paths against Exact and Prefix rules.
// Configure a Router before serving traffic; concurrent reads are safe once
// configuration is complete.
type Router struct {
	exact    map[string]Rule
	prefixes []Rule
}

// NewRouter validates and canonicalizes rules. It is safe to use the returned
// router when err is nil.
func NewRouter(rules ...Rule) (*Router, error) {
	r := &Router{exact: make(map[string]Rule)}
	for _, rule := range rules {
		if err := r.Add(rule); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// AddExact adds an Exact rule.
func (r *Router) AddExact(path, target string) error {
	return r.Add(Rule{Kind: Exact, Path: path, Target: target})
}

// AddPrefix adds a Prefix rule.
func (r *Router) AddPrefix(path, target string) error {
	return r.Add(Rule{Kind: Prefix, Path: path, Target: target})
}

// Add validates, canonicalizes, and registers one rule. A rule path with the
// same canonical path as an existing rule of the same kind is rejected.
func (r *Router) Add(rule Rule) error {
	if rule.Kind != Exact && rule.Kind != Prefix {
		return fmt.Errorf("%w: unknown match kind %d", ErrInvalidRule, rule.Kind)
	}

	canonical, err := CanonicalPath(rule.Path)
	if err != nil {
		return fmt.Errorf("%w: path %q: %v", ErrInvalidRule, rule.Path, err)
	}
	rule.Path = canonical

	switch rule.Kind {
	case Exact:
		if r.exact == nil {
			r.exact = make(map[string]Rule)
		}
		if _, exists := r.exact[canonical]; exists {
			return fmt.Errorf("%w: exact %q", ErrDuplicateRule, canonical)
		}
		r.exact[canonical] = rule

	case Prefix:
		for _, existing := range r.prefixes {
			if existing.Path == canonical {
				return fmt.Errorf("%w: prefix %q", ErrDuplicateRule, canonical)
			}
		}
		r.prefixes = append(r.prefixes, rule)
		sort.Slice(r.prefixes, func(i, j int) bool {
			if len(r.prefixes[i].Path) != len(r.prefixes[j].Path) {
				return len(r.prefixes[i].Path) > len(r.prefixes[j].Path)
			}
			return r.prefixes[i].Path < r.prefixes[j].Path
		})
	}

	return nil
}

// Match resolves rawPath with the same canonical path that callers must
// forward. An error means the request path is unsafe and must be rejected.
// Matched is false when no rule matches.
func (r *Router) Match(rawPath string) (Match, error) {
	canonical, err := CanonicalPath(rawPath)
	if err != nil {
		return Match{}, err
	}

	result := Match{Path: canonical}
	if r == nil {
		return result, nil
	}

	if rule, ok := r.exact[canonical]; ok {
		result.Rule = rule
		result.Matched = true
		return result, nil
	}

	for _, rule := range r.prefixes {
		if prefixMatches(canonical, rule.Path) {
			result.Rule = rule
			result.Matched = true
			return result, nil
		}
	}

	return result, nil
}

// prefixMatches implements element-wise Ingress Prefix matching. Repeated
// slashes have already been collapsed, so comparing the next byte with '/'
// is sufficient.
func prefixMatches(requestPath, prefix string) bool {
	if requestPath == prefix {
		return true
	}
	if !strings.HasPrefix(requestPath, prefix) {
		return false
	}
	if prefix == "/" {
		return true
	}
	return len(requestPath) > len(prefix) && requestPath[len(prefix)] == '/'
}
