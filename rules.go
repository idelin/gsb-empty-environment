package netsec

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var regexCache sync.Map

type compiledRegex struct {
	expr *regexp.Regexp
	err  error
}

// compileFullPattern anchors the expression so matching is always performed
// against the complete string rather than an unanchored substring.
func compileFullPattern(pattern string) (*regexp.Regexp, error) {
	if cached, ok := regexCache.Load(pattern); ok {
		entry := cached.(compiledRegex)
		return entry.expr, entry.err
	}
	expr, err := regexp.Compile(`^(?:` + pattern + `)$`)
	entry := compiledRegex{expr: expr, err: err}
	regexCache.Store(pattern, entry)
	return expr, err
}

// Matches reports whether the rule matches the given coordinate. Missing rule
// attributes match every value for that field. Invalid regular expressions
// return an error and never match.
func (r TrustRule) Matches(group, name, version, file string) (bool, error) {
	fields := []struct {
		pattern *string
		value   string
	}{
		{r.Group, group},
		{r.Name, name},
		{r.Version, version},
		{r.File, file},
	}
	for _, field := range fields {
		if field.pattern == nil {
			continue
		}
		matched, err := r.matchValue(*field.pattern, field.value)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func (r TrustRule) matchValue(pattern, value string) (bool, error) {
	if !r.Regex {
		// Literal rules support Gradle dynamic selectors in version fields;
		// every other field is an exact, whole-string comparison.
		return literalMatch(pattern, value), nil
	}
	expr, err := compileFullPattern(pattern)
	if err != nil {
		return false, fmt.Errorf("invalid trusted-artifacts regex %q: %w", pattern, err)
	}
	return expr.MatchString(value), nil
}

func literalMatch(pattern, value string) bool {
	if IsDynamicVersion(pattern) {
		return DynamicVersionMatches(pattern, value)
	}
	return pattern == value
}

// IsMatchAllRegex reports whether a regex matches every possible string.
// Common wrappers such as "^.*$", "(.*)" and ".*|.*" are recognised; only
// regex-enabled rules are treated as over-broad.
func IsMatchAllRegex(pattern string) bool {
	core := strings.TrimSpace(pattern)
	for {
		stripped := strings.TrimPrefix(strings.TrimSuffix(core, "$"), "^")
		stripped = strings.TrimSpace(stripped)
		if stripped == core {
			break
		}
		core = stripped
	}
	core = strings.TrimSpace(core)
	for {
		if len(core) >= 2 && core[0] == '(' && core[len(core)-1] == ')' {
			inner := core[1 : len(core)-1]
			if !strings.ContainsAny(inner, "|()") {
				core = strings.TrimSpace(inner)
				continue
			}
		}
		break
	}
	if core == ".*" || core == ".+" {
		return true
	}
	// Alternations where every branch is itself match-all (".*|.*", "(.*)|.*").
	if strings.Contains(core, "|") {
		allBranchesMatchAll := true
		for _, branch := range strings.Split(core, "|") {
			if !IsMatchAllRegex(branch) {
				allBranchesMatchAll = false
				break
			}
		}
		if allBranchesMatchAll {
			return true
		}
	}
	return false
}

// HasUnescapedDot reports whether the regex contains a "." metacharacter that
// is not escaped and not inside a character class. Such a rule also matches
// strings where the dot position holds another character.
func HasUnescapedDot(pattern string) bool {
	escaped := false
	inClass := false
	for _, char := range pattern {
		switch {
		case escaped:
			escaped = false
		case char == '\\':
			escaped = true
		case char == '[':
			inClass = true
		case char == ']':
			inClass = false
		case char == '.' && !inClass:
			return true
		}
	}
	return false
}

// IsDynamicVersion reports whether selector is a Gradle dynamic version:
// "+", "prefix.+", "latest.release", "latest.integration", or a version range.
func IsDynamicVersion(selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "+" {
		return true
	}
	if strings.HasSuffix(selector, ".+") {
		return true
	}
	lower := strings.ToLower(selector)
	if lower == "latest.release" || lower == "latest.integration" {
		return true
	}
	if isVersionRange(selector) {
		return true
	}
	return false
}

// DynamicVersionMatches evaluates a Gradle dynamic version selector against
// a concrete version. Non-dynamic selectors fall back to exact equality.
func DynamicVersionMatches(selector, version string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "+" {
		return version != ""
	}
	if strings.HasSuffix(selector, ".+") {
		return segmentPrefixMatch(strings.TrimSuffix(selector, ".+"), version)
	}
	lower := strings.ToLower(selector)
	if lower == "latest.release" || lower == "latest.integration" {
		return version != ""
	}
	if isVersionRange(selector) {
		return rangeMatches(selector, version)
	}
	return selector == version
}

// segmentPrefixMatch reports whether version shares the same numeric segment
// prefix as fixedPrefix. "1" matches "1", "1.0" and "1.2.3", but not "10".
func segmentPrefixMatch(fixedPrefix, version string) bool {
	prefixParts := strings.Split(fixedPrefix, ".")
	versionParts := strings.Split(version, ".")
	if len(versionParts) < len(prefixParts) {
		return false
	}
	for index, part := range prefixParts {
		if part != versionParts[index] {
			return false
		}
	}
	return true
}

func isVersionRange(selector string) bool {
	if selector == "" {
		return false
	}
	first := selector[0]
	last := selector[len(selector)-1]
	return (first == '[' || first == '(') && (last == ']' || last == ')')
}

// rangeMatches supports the Gradle forms "[1.0,2.0)", "[1.0,)", "(,1.0]",
// "[1.0]" (exact) and strict ranges such as "[1.0,2.0)!![1.5,2.0)".
func rangeMatches(rangeSpec, version string) bool {
	for _, part := range strings.Split(rangeSpec, "!!") {
		if singleRangeMatches(part, version) {
			return true
		}
	}
	return false
}

func singleRangeMatches(rangeSpec, version string) bool {
	rangeSpec = strings.TrimSpace(rangeSpec)
	if !isVersionRange(rangeSpec) {
		return false
	}
	lowerInclusive := rangeSpec[0] == '['
	upperInclusive := rangeSpec[len(rangeSpec)-1] == ']'
	inner := rangeSpec[1 : len(rangeSpec)-1]

	// Exact form "[1.0]".
	if !strings.ContainsAny(inner, ",") {
		return compareVersions(version, inner) == 0
	}

	bounds := strings.SplitN(inner, ",", 2)
	lower := strings.TrimSpace(bounds[0])
	upper := strings.TrimSpace(bounds[1])
	if lower != "" {
		cmp := compareVersions(version, lower)
		if cmp < 0 || (cmp == 0 && !lowerInclusive) {
			return false
		}
	}
	if upper != "" {
		cmp := compareVersions(version, upper)
		if cmp > 0 || (cmp == 0 && !upperInclusive) {
			return false
		}
	}
	return true
}

// compareVersions compares dot-separated numeric versions with a lexicographic
// fallback for non-numeric segments.
func compareVersions(a, b string) int {
	left := strings.Split(a, ".")
	right := strings.Split(b, ".")
	for index := 0; index < len(left) || index < len(right); index++ {
		var leftPart, rightPart string
		if index < len(left) {
			leftPart = left[index]
		}
		if index < len(right) {
			rightPart = right[index]
		}
		if cmp := compareVersionPart(leftPart, rightPart); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func compareVersionPart(a, b string) int {
	aNumeric := isAllDigits(a)
	bNumeric := isAllDigits(b)
	switch {
	case aNumeric && bNumeric:
		switch {
		case len(a) != len(b):
			if len(a) > len(b) {
				return 1
			}
			return -1
		case a > b:
			return 1
		case a < b:
			return -1
		default:
			return 0
		}
	case aNumeric:
		return 1
	case bNumeric:
		return -1
	default:
		return strings.Compare(a, b)
	}
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
