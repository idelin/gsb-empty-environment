package netsec

import (
	"path"
	"sort"
	"strings"
)

// Policy is a single index's or alias's field-level policy.
//
// AllowedFields is an allow-list. A parent field does not grant its children:
// allowing "user" does not allow "user.ssn". Use a pattern such as "user.*" or
// "user.**" to grant descendants explicitly. "*" alone grants every field.
//
// DeniedFields always wins. Denying a parent field also denies all of its
// descendants.
//
// The zero value denies every field. AllowAll grants every field that is not
// denied; it is intended for administrative policies only.
type Policy struct {
	AllowedFields []string `json:"allowed_fields,omitempty"`
	DeniedFields  []string `json:"denied_fields,omitempty"`
	AllowAll      bool     `json:"allow_all,omitempty"`
}

// FieldPolicy is a descriptive alias for Policy.
type FieldPolicy = Policy

// Clone returns a deep copy of p.
func (p Policy) Clone() Policy {
	return Policy{
		AllowedFields: append([]string(nil), p.AllowedFields...),
		DeniedFields:  append([]string(nil), p.DeniedFields...),
		AllowAll:      p.AllowAll,
	}
}

func (p Policy) normalized() Policy {
	out := Policy{
		AllowedFields: normalizePatterns(p.AllowedFields),
		DeniedFields:  normalizePatterns(p.DeniedFields),
		AllowAll:      p.AllowAll,
	}
	return out
}

func normalizePatterns(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// fieldRules is the immutable, normalized form of one policy.
type fieldRules struct {
	source   string
	allow    []string
	deny     []string
	allowAll bool
}

func newFieldRules(source string, p Policy) *fieldRules {
	p = p.normalized()
	return &fieldRules{
		source:   source,
		allow:    p.AllowedFields,
		deny:     p.DeniedFields,
		allowAll: p.AllowAll,
	}
}

func (r *fieldRules) allows(field string) bool {
	if field == "" {
		return false
	}
	if r.denies(field) {
		return false
	}
	if r.allowAll {
		return true
	}
	for _, pattern := range r.allow {
		if fieldPatternMatches(pattern, field) {
			return true
		}
	}
	return false
}

func (r *fieldRules) denies(field string) bool {
	for _, pattern := range r.deny {
		if fieldPatternMatches(pattern, field) {
			return true
		}
		if patternMatchesAncestor(pattern, field) {
			return true
		}
	}
	return false
}

// couldAllowDescendant reports whether this policy could possibly grant a
// field below parent. It is deliberately conservative and is only used to
// decide whether an object must be traversed while filtering.
func (r *fieldRules) couldAllowDescendant(parent string) bool {
	if parent == "" {
		return true
	}
	if r.allowAll {
		return true
	}
	probe := parent + ".__fieldauth_probe__"
	for _, pattern := range r.allow {
		if fieldPatternMatches(pattern, probe) {
			return true
		}
		if !hasPatternMeta(pattern) && strings.HasPrefix(pattern, parent+".") {
			return true
		}
	}
	return false
}

// allowsAllDescendants is the stronger form used when a script may read a
// container object. Exact child grants are not enough: the entire subtree
// must be covered, so a previously unknown child cannot be exposed.
func (r *fieldRules) allowsAllDescendants(parent string) bool {
	if parent == "" {
		return r.allowAll
	}
	for _, deny := range r.deny {
		if deny == parent || fieldPatternMatches(deny, parent+".__fieldauth_probe__") ||
			strings.HasPrefix(deny, parent+".") {
			return false
		}
	}
	if r.allowAll {
		return true
	}
	deepProbe := parent + ".__fieldauth_a__.__fieldauth_b__"
	for _, allow := range r.allow {
		if allow == "*" || allow == "**" {
			return true
		}
		if strings.Contains(allow, "**") && fieldPatternMatches(allow, deepProbe) {
			return true
		}
	}
	return false
}

func hasPatternMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// fieldPatternMatches matches a dotted field path. "*" and "**" match every
// path. Other patterns are segment-aware: a "*" never crosses a dot.
func fieldPatternMatches(pattern, field string) bool {
	pattern = strings.TrimSpace(pattern)
	field = strings.TrimSpace(field)
	if pattern == "" || field == "" {
		return false
	}
	if pattern == "*" || pattern == "**" {
		return true
	}
	if !hasPatternMeta(pattern) {
		return pattern == field
	}
	return matchSegments(strings.Split(pattern, "."), strings.Split(field, "."))
}

func matchSegments(pattern, field []string) bool {
	if len(pattern) == 0 {
		return len(field) == 0
	}
	if pattern[0] == "**" {
		if len(pattern) == 1 {
			return true
		}
		for i := 0; i <= len(field); i++ {
			if matchSegments(pattern[1:], field[i:]) {
				return true
			}
		}
		return false
	}
	if len(field) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], field[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pattern[1:], field[1:])
}

func patternMatchesAncestor(pattern, field string) bool {
	if !hasPatternMeta(pattern) {
		return strings.HasPrefix(field, pattern+".")
	}
	parts := strings.Split(field, ".")
	for i := 1; i < len(parts); i++ {
		if fieldPatternMatches(pattern, strings.Join(parts[:i], ".")) {
			return true
		}
	}
	return false
}

// requestFieldMatches implements field-selection semantics. Unlike a policy
// pattern, a concrete request for an object ("user") also selects its allowed
// descendants. A trailing ".*" selects the whole subtree. The selection is
// always intersected with the effective allow-list, so request wildcards can
// only narrow, never broaden, the result.
func requestFieldMatches(pattern, field string) bool {
	pattern = strings.TrimSpace(pattern)
	field = strings.TrimSpace(field)
	if pattern == "" || field == "" {
		return false
	}
	if pattern == "*" || pattern == "**" {
		return true
	}
	if !hasPatternMeta(pattern) {
		return field == pattern || strings.HasPrefix(field, pattern+".")
	}
	if strings.HasSuffix(pattern, ".*") {
		base := strings.TrimSuffix(pattern, ".*")
		if !hasPatternMeta(base) {
			return field == base || strings.HasPrefix(field, base+".")
		}
	}
	return fieldPatternMatches(pattern, field)
}

func isConcreteField(field string) bool {
	return field != "" && !hasPatternMeta(field)
}

func fieldIsAncestor(ancestor, field string) bool {
	return ancestor != "" && field != ancestor && strings.HasPrefix(field, ancestor+".")
}
