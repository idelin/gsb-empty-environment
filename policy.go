package netsec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Permission is a set of read/write capabilities. When several rules match,
// their requirements are joined, so the strictest combination applies.
type Permission uint8

const (
	PermissionNone      Permission = 0
	PermissionRead      Permission = 1 << 0
	PermissionWrite     Permission = 1 << 1
	PermissionReadWrite Permission = PermissionRead | PermissionWrite
)

// Join returns the union of two permission requirements.
func (p Permission) Join(other Permission) Permission {
	return p | other
}

// Covers reports whether held satisfies every capability in required.
func (p Permission) Covers(required Permission) bool {
	return p&required == required
}

// HigherThan reports whether p is a strict superset of other.
func (p Permission) HigherThan(other Permission) bool {
	return p != other && p.Covers(other)
}

func (p Permission) String() string {
	switch p {
	case PermissionNone:
		return "none"
	case PermissionRead:
		return "read"
	case PermissionWrite:
		return "write"
	case PermissionReadWrite:
		return "read-write"
	default:
		return fmt.Sprintf("permission(%d)", uint8(p))
	}
}

// Rule is a path-permission declaration. Path, PathPrefix, and PathPattern are
// optional match constraints; when more than one is present they must all
// match. A rule with none of them applies to every path in the provider.
//
// GrantURI marks the rule as a grant-uri-permission declaration. Such a rule
// may omit Permission and Deny.
type Rule struct {
	Name        string
	Path        string
	PathPrefix  string
	PathPattern string
	Permission  Permission
	Deny        bool
	GrantURI    bool
}

// PathPermission is the Android-manifest name for a Rule.
type PathPermission = Rule

// GrantRule is a grant-uri-permission prefix (or exact/pattern) declaration.
// Its path constraints are evaluated against the same canonical path as
// path-permission rules.
type GrantRule struct {
	Name        string
	Path        string
	PathPrefix  string
	PathPattern string
}

// GrantURIPermission is the Android-manifest name for a GrantRule.
type GrantURIPermission = GrantRule

// Policy is a provider root plus its path-permission and
// grant-uri-permission declarations. An empty Root means the URI root "/".
type Policy struct {
	Root   string
	Rules  []Rule
	Grants []GrantRule
}

// Evaluator is an alias for Policy, for callers that prefer an evaluator type.
type Evaluator = Policy

// Decision is the result of evaluating a request path.
type Decision struct {
	// Path is the decoded and dot-segment-normalized request path.
	Path string
	// Denied is true when any matching rule is deny, when the request escapes
	// the provider root, or when the path is malformed.
	Denied bool
	// Matched reports whether at least one path rule or grant rule matched.
	Matched bool
	// Required is the union of all matching path-permission requirements.
	Required Permission
	// GrantURI reports whether a matching grant-uri-permission covers Path.
	GrantURI bool
	// MatchedRules and MatchedGrants contain indexes into Policy.Rules and
	// Policy.Grants respectively.
	MatchedRules  []int
	MatchedGrants []int
}

// Permits reports whether held permissions (or a matching URI grant) allow
// access. An explicit deny always wins.
func (d Decision) Permits(held Permission) bool {
	if d.Denied {
		return false
	}
	if d.GrantURI {
		return true
	}
	return d.Matched && held.Covers(d.Required)
}

var (
	// ErrInvalidRule reports a rule path that cannot be canonicalized.
	ErrInvalidRule = errors.New("netsec: invalid rule")
	// ErrInvalidContentURI reports input that is not a content URI when a
	// content URI is required.
	ErrInvalidContentURI = errors.New("netsec: invalid content URI")
)

// ContentURI is the authority and path portion of content:// URI.
type ContentURI struct {
	Authority string
	Path      string
}

// ParseContentURI separates the authority from the raw, still-encoded path of
// a content:// URI. Query and fragment components are not part of the path.
func ParseContentURI(raw string) (ContentURI, error) {
	const scheme = "content://"
	if len(raw) < len(scheme) || !strings.EqualFold(raw[:len(scheme)], scheme) {
		return ContentURI{}, fmt.Errorf("%w: missing content:// scheme", ErrInvalidContentURI)
	}

	rest := raw[len(scheme):]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority := rest[:end]
	if authority == "" {
		return ContentURI{}, fmt.Errorf("%w: empty authority", ErrInvalidContentURI)
	}

	path := rest[end:]
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return ContentURI{Authority: authority, Path: path}, nil
}

// Evaluate canonicalizes the request path, rejects traversal outside the
// provider root, then applies every rule whose constraints match the canonical
// path. A single deny or the highest joined permission requirement wins.
//
// raw may be either a content:// URI or a bare path.
func (p Policy) Evaluate(raw string) (Decision, error) {
	requestPath, err := requestPath(raw)
	if err != nil {
		return Decision{Denied: true}, err
	}

	root := p.Root
	if root == "" {
		root = "/"
	}
	canonicalRoot, err := Canonicalize(root)
	if err != nil {
		return Decision{Denied: true}, fmt.Errorf("%w: root: %w", ErrInvalidRule, err)
	}

	canonicalPath, err := Canonicalize(requestPath)
	if err != nil {
		return Decision{Denied: true}, err
	}

	decision := Decision{Path: canonicalPath}
	if !PathWithinRoot(canonicalRoot, canonicalPath) {
		decision.Denied = true
		return decision, fmt.Errorf("%w: %q is outside %q", ErrEscapesRoot, canonicalPath, canonicalRoot)
	}

	for i, rule := range p.Rules {
		matched, err := matchConstraints(rule.Path, rule.PathPrefix, rule.PathPattern, canonicalPath)
		if err != nil {
			return decision, fmt.Errorf("%w: rule %d: %w", ErrInvalidRule, i, err)
		}
		if !matched {
			continue
		}

		decision.Matched = true
		decision.MatchedRules = append(decision.MatchedRules, i)
		decision.Required = decision.Required.Join(rule.Permission)
		if rule.Deny {
			decision.Denied = true
		}
		if rule.GrantURI {
			decision.GrantURI = true
		}
	}

	for i, grant := range p.Grants {
		matched, err := matchConstraints(grant.Path, grant.PathPrefix, grant.PathPattern, canonicalPath)
		if err != nil {
			return decision, fmt.Errorf("%w: grant %d: %w", ErrInvalidRule, i, err)
		}
		if !matched {
			continue
		}

		decision.Matched = true
		decision.MatchedGrants = append(decision.MatchedGrants, i)
		decision.GrantURI = true
	}

	return decision, nil
}

// EvaluatePath is a convenience wrapper for a bare path.
func (p Policy) EvaluatePath(path string) (Decision, error) {
	return p.Evaluate(path)
}

// Validate checks that the provider root and every rule can be canonicalized.
func (p Policy) Validate() error {
	root := p.Root
	if root == "" {
		root = "/"
	}
	if _, err := Canonicalize(root); err != nil {
		return fmt.Errorf("%w: root: %w", ErrInvalidRule, err)
	}
	for i, rule := range p.Rules {
		if err := validateConstraints(rule.Path, rule.PathPrefix, rule.PathPattern); err != nil {
			return fmt.Errorf("%w: rule %d: %w", ErrInvalidRule, i, err)
		}
	}
	for i, grant := range p.Grants {
		if err := validateConstraints(grant.Path, grant.PathPrefix, grant.PathPattern); err != nil {
			return fmt.Errorf("%w: grant %d: %w", ErrInvalidRule, i, err)
		}
	}
	return nil
}

// Evaluate is a package-level convenience for evaluating one request path
// against a policy.
func Evaluate(root, raw string, rules []Rule, grants []GrantRule) (Decision, error) {
	return Policy{Root: root, Rules: rules, Grants: grants}.Evaluate(raw)
}

func requestPath(raw string) (string, error) {
	if len(raw) >= len("content://") && strings.EqualFold(raw[:len("content://")], "content://") {
		uri, err := ParseContentURI(raw)
		if err != nil {
			return "", err
		}
		return uri.Path, nil
	}
	return raw, nil
}

func matchConstraints(exact, prefix, pattern, canonicalPath string) (bool, error) {
	if exact != "" {
		canonicalExact, err := Canonicalize(exact)
		if err != nil {
			return false, err
		}
		if canonicalPath != canonicalExact {
			return false, nil
		}
	}

	if prefix != "" {
		canonicalPrefix, err := Canonicalize(prefix)
		if err != nil {
			return false, err
		}
		if !prefixMatches(canonicalPrefix, canonicalPath) {
			return false, nil
		}
	}

	if pattern != "" {
		re, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			return false, err
		}
		if !re.MatchString(canonicalPath) {
			return false, nil
		}
	}

	// A declaration without any path constraint covers the whole provider.
	return true, nil
}

func validateConstraints(exact, prefix, pattern string) error {
	if exact != "" {
		if _, err := Canonicalize(exact); err != nil {
			return err
		}
	}
	if prefix != "" {
		if _, err := Canonicalize(prefix); err != nil {
			return err
		}
	}
	if pattern != "" {
		if _, err := regexp.Compile("^(?:" + pattern + ")$"); err != nil {
			return err
		}
	}
	return nil
}

func prefixMatches(prefix, path string) bool {
	if prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
