package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Bound is one end of a version interval.  A nil *Bound means unbounded.
type Bound struct {
	Version   Version
	Inclusive bool
}

// Interval is a contiguous set of versions.
type Interval struct {
	Lower *Bound
	Upper *Bound
}

// Contains reports whether version is inside the interval.
func (iv Interval) Contains(version string) bool {
	v, err := ParseVersion(version)
	if err != nil {
		return false
	}
	return intervalContains(iv, v)
}

// String returns a canonical interval spelling.
func (iv Interval) String() string { return formatInterval(iv) }

// VersionRange is a union of version intervals with optional excluded
// versions.  It accepts the common range spellings used by npm, Cargo,
// NuGet, Maven and VERS, for example:
//
//	^1.2.3
//	~1.2
//	>=1.0.0 <2.0.0
//	1.2.x
//	1.2.3 - 2.0.0
//	[1.0.0,2.0.0)
//	vers:npm/1.2.3|>=2.0.0|<5.0.0
type VersionRange struct {
	Intervals []Interval
	Excluded  []Version
	Source    string
}

// Clone returns a deep copy of the range.
func (r *VersionRange) Clone() *VersionRange {
	if r == nil {
		return nil
	}
	out := &VersionRange{
		Intervals: make([]Interval, len(r.Intervals)),
		Excluded:  append([]Version(nil), r.Excluded...),
		Source:    r.Source,
	}
	for i, iv := range r.Intervals {
		out.Intervals[i] = Interval{Lower: cloneBound(iv.Lower), Upper: cloneBound(iv.Upper)}
	}
	return out
}

// NewInterval creates an interval.  Either bound may be nil.
func NewInterval(lower, upper *Bound) Interval {
	return Interval{Lower: lower, Upper: upper}
}

// ExactInterval creates an interval containing exactly one version.
func ExactInterval(version string) (Interval, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return Interval{}, err
	}
	return Interval{
		Lower: &Bound{Version: v, Inclusive: true},
		Upper: &Bound{Version: v, Inclusive: true},
	}, nil
}

// ParseRange parses a version range expression.
func ParseRange(expr string) (*VersionRange, error) {
	return ParseVersionRange(expr)
}

// ParseVersionRange parses a version range expression.  Both native range
// notation and a VERS URI ("vers:npm/...") are accepted.
func ParseVersionRange(expr string) (*VersionRange, error) {
	source := strings.TrimSpace(expr)
	if source == "" {
		return nil, fmt.Errorf("empty version range")
	}
	s := source
	if len(s) >= 5 && strings.EqualFold(s[:5], "vers:") {
		rest := s[5:]
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			return nil, fmt.Errorf("invalid VERS %q: missing type separator", source)
		}
		if rest[:slash] == "" {
			return nil, fmt.Errorf("invalid VERS %q: empty type", source)
		}
		s = rest[slash+1:]
	}
	if decoded, err := decodeComponent(s); err == nil {
		s = decoded
	}
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("empty version range")
	}

	var all []Interval
	var excluded []Version

	// "||" is the common union operator.  A single "|" is VERS's constraint
	// separator; adjacent one-sided bounds form one interval there.
	for _, unionPart := range splitOutsideBrackets(s, "||") {
		intervals, exclusions, err := parsePipeClause(unionPart)
		if err != nil {
			return nil, err
		}
		all = append(all, intervals...)
		excluded = append(excluded, exclusions...)
	}

	r := &VersionRange{Intervals: normalizeIntervals(all), Excluded: dedupeVersions(excluded), Source: source}
	r.simplifyExclusions()
	if len(r.Intervals) == 0 {
		return r, nil
	}
	return r, nil
}

func splitOutsideBrackets(s, sep string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[', '(':
			depth++
		case ']', ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && strings.HasPrefix(s[i:], sep) {
			out = append(out, s[start:i])
			i += len(sep) - 1
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func parsePipeClause(clause string) ([]Interval, []Version, error) {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return nil, nil, fmt.Errorf("empty range clause")
	}
	if !strings.Contains(clause, "|") {
		iv, ex, err := parseAndClause(clause)
		if err != nil {
			return nil, nil, err
		}
		return []Interval{iv}, ex, nil
	}

	segments := strings.Split(clause, "|")
	var intervals []Interval
	var exclusions []Version
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, nil, fmt.Errorf("empty VERS constraint in %q", clause)
		}
		iv, ex, err := parseAndClause(segment)
		if err != nil {
			return nil, nil, err
		}
		exclusions = append(exclusions, ex...)

		// An exclusion-only segment ("!=1.5.0") does not define a
		// new union member when other constraints already define
		// intervals; it removes a point from those intervals.
		if isUnboundedInterval(iv) && len(ex) > 0 && len(intervals) > 0 {
			continue
		}

		// VERS commonly writes an interval as the pair ">=2.0.0|<5.0.0".
		// Join a one-sided upper constraint to the immediately preceding
		// one-sided lower constraint.  Other constraints remain separate
		// union members.
		if isUpperOnly(iv) && len(intervals) > 0 && isLowerOnly(intervals[len(intervals)-1]) {
			joined, ok := intersectIntervals(intervals[len(intervals)-1], iv)
			if !ok {
				return nil, nil, fmt.Errorf("range %q matches no versions", clause)
			}
			intervals[len(intervals)-1] = joined
		} else {
			intervals = append(intervals, iv)
		}
	}
	return intervals, exclusions, nil
}

func isLowerOnly(iv Interval) bool {
	return iv.Lower != nil && iv.Upper == nil
}

func isUpperOnly(iv Interval) bool {
	return iv.Lower == nil && iv.Upper != nil
}

func isUnboundedInterval(iv Interval) bool {
	return iv.Lower == nil && iv.Upper == nil
}

func parseAndClause(clause string) (Interval, []Version, error) {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return Interval{}, nil, fmt.Errorf("empty range constraint")
	}
	if clause == "*" || clause == "x" || clause == "X" {
		return Interval{}, nil, nil
	}

	if iv, ok, err := parseBracketRange(clause); ok || err != nil {
		return iv, nil, err
	}
	if iv, ok, err := parseHyphenRange(clause); ok || err != nil {
		return iv, nil, err
	}

	tokens := tokenizeRange(clause)
	if len(tokens) == 0 {
		return Interval{}, nil, fmt.Errorf("invalid range %q", clause)
	}

	result := Interval{}
	var exclusions []Version
	first := true
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		op, rest := splitOperator(token)
		if rest == "" {
			if i+1 >= len(tokens) {
				return Interval{}, nil, fmt.Errorf("missing version after %q", op)
			}
			i++
			rest = tokens[i]
		}
		iv, ex, err := parseConstraint(op, rest)
		if err != nil {
			return Interval{}, nil, err
		}
		if first {
			result = iv
			first = false
		} else {
			joined, ok := intersectIntervals(result, iv)
			if !ok {
				return Interval{}, nil, fmt.Errorf("range %q matches no versions", clause)
			}
			result = joined
		}
		exclusions = append(exclusions, ex...)
	}
	return result, exclusions, nil
}

func tokenizeRange(s string) []string {
	s = strings.ReplaceAll(s, "&&", " ")
	s = strings.ReplaceAll(s, ",", " ")
	s = spaceOutOperators(s)
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "()")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func spaceOutOperators(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if isSpaceByte(s[i]) {
			b.WriteByte(' ')
			continue
		}
		matched := ""
		for _, op := range []string{">=", "<=", "!=", "~>", ">", "<", "=", "^", "~"} {
			if strings.HasPrefix(s[i:], op) {
				matched = op
				break
			}
		}
		if matched != "" {
			if b.Len() > 0 && !isSpaceByte(b.String()[b.Len()-1]) {
				b.WriteByte(' ')
			}
			b.WriteString(matched)
			b.WriteByte(' ')
			i += len(matched) - 1
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func splitOperator(token string) (string, string) {
	for _, op := range []string{">=", "<=", "!=", "~>", "==", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(token, op) {
			rest := strings.TrimSpace(token[len(op):])
			if op == "==" {
				op = "="
			}
			return op, rest
		}
	}
	return "", token
}

type partialVersion struct {
	version    Version
	precision  int
	wildcard   bool
	wildcardAt int
}

func parsePartial(raw string) (partialVersion, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return partialVersion{}, fmt.Errorf("empty version")
	}
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}

	// Wildcards are valid only in the numeric core.
	core := s
	if i := strings.IndexAny(core, "+-"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	wildcardAt := -1
	for i, part := range parts {
		if part == "x" || part == "X" || part == "*" {
			wildcardAt = i
			for _, tail := range parts[i:] {
				if tail != "x" && tail != "X" && tail != "*" {
					return partialVersion{}, fmt.Errorf("wildcard must be the last version component in %q", raw)
				}
			}
			break
		}
	}
	if wildcardAt >= 0 {
		if strings.ContainsAny(s, "+-") {
			return partialVersion{}, fmt.Errorf("wildcard version %q cannot have a suffix", raw)
		}
		nums := make([]int64, 0, wildcardAt)
		for _, part := range parts[:wildcardAt] {
			if !isDigits(part) {
				return partialVersion{}, fmt.Errorf("invalid version component %q", part)
			}
			n, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				return partialVersion{}, fmt.Errorf("invalid version component %q", part)
			}
			nums = append(nums, n)
		}
		v := versionFromSegments(nums, raw)
		return partialVersion{version: v, precision: wildcardAt, wildcard: true, wildcardAt: wildcardAt}, nil
	}

	v, err := parseVersion(raw)
	if err != nil {
		return partialVersion{}, err
	}
	return partialVersion{version: v, precision: len(v.Segments), wildcardAt: -1}, nil
}

func paddedSegments(v Version, n int) []int64 {
	out := make([]int64, n)
	copy(out, v.Segments)
	return out
}

func versionFromSegments(segments []int64, original string) Version {
	v := Version{Original: original, Segments: append([]int64(nil), segments...), Valid: true}
	if len(v.Segments) > 0 {
		v.Major = v.Segments[0]
	}
	if len(v.Segments) > 1 {
		v.Minor = v.Segments[1]
	}
	if len(v.Segments) > 2 {
		v.Patch = v.Segments[2]
	}
	return v
}

func lowerBound(v Version, inclusive bool) *Bound {
	return &Bound{Version: v, Inclusive: inclusive}
}

func upperBound(v Version, inclusive bool) *Bound {
	return &Bound{Version: v, Inclusive: inclusive}
}

func truncatedLower(p partialVersion) Version {
	n := p.precision
	if n < 1 {
		n = 1
	}
	segments := paddedSegments(p.version, n)
	return versionFromSegments(segments, p.version.Original)
}

func incrementedUpper(p partialVersion, index int) Version {
	n := index + 1
	if n < 3 {
		n = 3
	}
	segments := paddedSegments(p.version, n)
	if index < 0 {
		return Version{}
	}
	segments[index]++
	for i := index + 1; i < len(segments); i++ {
		segments[i] = 0
	}
	return versionFromSegments(segments, p.version.Original)
}

func parseConstraint(op, raw string) (Interval, []Version, error) {
	if raw == "*" || raw == "x" || raw == "X" {
		if op == "" || op == "=" {
			return Interval{}, nil, nil
		}
		return Interval{}, nil, fmt.Errorf("wildcard cannot be used with %q", op)
	}

	p, err := parsePartial(raw)
	if err != nil {
		return Interval{}, nil, err
	}
	lower := truncatedLower(p)

	switch op {
	case "":
		if p.wildcard || p.precision < 3 {
			return wildcardInterval(p)
		}
		return exactInterval(lower)
	case "=":
		if p.wildcard {
			return wildcardInterval(p)
		}
		return exactInterval(lower)
	case ">=":
		return Interval{Lower: lowerBound(lower, true)}, nil, nil
	case ">":
		return Interval{Lower: lowerBound(lower, false)}, nil, nil
	case "<=":
		return Interval{Upper: upperBound(lower, true)}, nil, nil
	case "<":
		return Interval{Upper: upperBound(lower, false)}, nil, nil
	case "!=":
		if p.wildcard {
			return Interval{}, nil, fmt.Errorf("wildcard cannot be excluded")
		}
		return Interval{}, []Version{lower}, nil
	case "^":
		upper := caretUpper(p)
		return Interval{Lower: lowerBound(lower, true), Upper: upperBound(upper, false)}, nil, nil
	case "~", "~>":
		upper := tildeUpper(p)
		return Interval{Lower: lowerBound(lower, true), Upper: upperBound(upper, false)}, nil, nil
	default:
		return Interval{}, nil, fmt.Errorf("unsupported version comparator %q", op)
	}
}

func exactInterval(v Version) (Interval, []Version, error) {
	return Interval{
		Lower: &Bound{Version: v, Inclusive: true},
		Upper: &Bound{Version: v, Inclusive: true},
	}, nil, nil
}

func wildcardInterval(p partialVersion) (Interval, []Version, error) {
	if p.precision == 0 {
		return Interval{}, nil, nil
	}
	lower := truncatedLower(p)
	upper := incrementedUpper(p, p.precision-1)
	return Interval{Lower: lowerBound(lower, true), Upper: upperBound(upper, false)}, nil, nil
}

func caretUpper(p partialVersion) Version {
	segments := paddedSegments(p.version, maxInt(p.precision, 3))
	index := -1
	for i := 0; i < p.precision && i < len(segments); i++ {
		if segments[i] != 0 {
			index = i
			break
		}
	}
	if index < 0 {
		index = p.precision - 1
	}
	if index < 0 {
		index = 0
	}
	segments[index]++
	for i := index + 1; i < len(segments); i++ {
		segments[i] = 0
	}
	return versionFromSegments(segments, p.version.Original)
}

func tildeUpper(p partialVersion) Version {
	index := 0
	if p.precision >= 2 {
		index = 1
	}
	return incrementedUpper(p, index)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseBracketRange(s string) (Interval, bool, error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 {
		return Interval{}, false, nil
	}
	first, last := s[0], s[len(s)-1]
	if (first != '[' && first != '(') || (last != ']' && last != ')') {
		return Interval{}, false, nil
	}
	inner := s[1 : len(s)-1]
	comma := strings.IndexByte(inner, ',')
	if comma < 0 {
		v, err := parsePartial(strings.TrimSpace(inner))
		if err != nil {
			return Interval{}, true, err
		}
		if v.wildcard || v.precision < 3 {
			return Interval{}, true, fmt.Errorf("incomplete version in bracket range %q", s)
		}
		iv, _, _ := exactInterval(truncatedLower(v))
		return iv, true, nil
	}
	lowerRaw := strings.TrimSpace(inner[:comma])
	upperRaw := strings.TrimSpace(inner[comma+1:])
	var lower, upper *Bound
	if lowerRaw != "" {
		lp, err := parsePartial(lowerRaw)
		if err != nil {
			return Interval{}, true, err
		}
		lv := truncatedLower(lp)
		lower = &Bound{Version: lv, Inclusive: first == '['}
	}
	if upperRaw != "" {
		up, err := parsePartial(upperRaw)
		if err != nil {
			return Interval{}, true, err
		}
		uv := truncatedLower(up)
		upper = &Bound{Version: uv, Inclusive: last == ']'}
	}
	iv := Interval{Lower: lower, Upper: upper}
	if intervalEmpty(iv) {
		return Interval{}, true, fmt.Errorf("range %q matches no versions", s)
	}
	return iv, true, nil
}

func parseHyphenRange(s string) (Interval, bool, error) {
	if !strings.Contains(s, " - ") {
		return Interval{}, false, nil
	}
	parts := strings.Split(s, " - ")
	if len(parts) != 2 {
		return Interval{}, true, fmt.Errorf("invalid hyphen range %q", s)
	}
	lp, err := parsePartial(strings.TrimSpace(parts[0]))
	if err != nil {
		return Interval{}, true, err
	}
	up, err := parsePartial(strings.TrimSpace(parts[1]))
	if err != nil {
		return Interval{}, true, err
	}
	lower := truncatedLower(lp)
	var upper *Bound
	if up.precision >= 3 {
		uv := truncatedLower(up)
		upper = &Bound{Version: uv, Inclusive: true}
	} else {
		n := up.precision
		if n < 1 {
			n = 1
		}
		uv := incrementedUpper(up, n-1)
		upper = &Bound{Version: uv, Inclusive: false}
	}
	iv := Interval{Lower: &Bound{Version: lower, Inclusive: true}, Upper: upper}
	if intervalEmpty(iv) {
		return Interval{}, true, fmt.Errorf("range %q matches no versions", s)
	}
	return iv, true, nil
}

func intervalEmpty(iv Interval) bool {
	if iv.Lower == nil || iv.Upper == nil {
		return false
	}
	c := iv.Lower.Version.Compare(iv.Upper.Version)
	if c > 0 {
		return true
	}
	return c == 0 && (!iv.Lower.Inclusive || !iv.Upper.Inclusive)
}

func intersectIntervals(a, b Interval) (Interval, bool) {
	out := Interval{}
	switch {
	case a.Lower == nil:
		out.Lower = cloneBound(b.Lower)
	case b.Lower == nil:
		out.Lower = cloneBound(a.Lower)
	default:
		c := a.Lower.Version.Compare(b.Lower.Version)
		switch {
		case c > 0:
			out.Lower = cloneBound(a.Lower)
		case c < 0:
			out.Lower = cloneBound(b.Lower)
		default:
			out.Lower = cloneBound(a.Lower)
			out.Lower.Inclusive = a.Lower.Inclusive && b.Lower.Inclusive
		}
	}

	switch {
	case a.Upper == nil:
		out.Upper = cloneBound(b.Upper)
	case b.Upper == nil:
		out.Upper = cloneBound(a.Upper)
	default:
		c := a.Upper.Version.Compare(b.Upper.Version)
		switch {
		case c < 0:
			out.Upper = cloneBound(a.Upper)
		case c > 0:
			out.Upper = cloneBound(b.Upper)
		default:
			out.Upper = cloneBound(a.Upper)
			out.Upper.Inclusive = a.Upper.Inclusive && b.Upper.Inclusive
		}
	}
	if intervalEmpty(out) {
		return Interval{}, false
	}
	return out, true
}

func cloneBound(b *Bound) *Bound {
	if b == nil {
		return nil
	}
	c := *b
	return &c
}

func normalizeIntervals(in []Interval) []Interval {
	filtered := in[:0]
	for _, iv := range in {
		if !intervalEmpty(iv) {
			filtered = append(filtered, iv)
		}
	}
	out := filtered
	if len(out) == 0 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Lower == nil {
			return b.Lower != nil
		}
		if b.Lower == nil {
			return false
		}
		c := a.Lower.Version.Compare(b.Lower.Version)
		if c != 0 {
			return c < 0
		}
		return a.Lower.Inclusive && !b.Lower.Inclusive
	})

	merged := make([]Interval, 0, len(out))
	for _, iv := range out {
		if len(merged) == 0 {
			merged = append(merged, iv)
			continue
		}
		last := &merged[len(merged)-1]
		if intervalsOverlapOrTouch(*last, iv) {
			*last = mergeIntervals(*last, iv)
			continue
		}
		merged = append(merged, iv)
	}
	return merged
}

func intervalsOverlapOrTouch(a, b Interval) bool {
	if a.Upper == nil || b.Lower == nil {
		return true
	}
	c := a.Upper.Version.Compare(b.Lower.Version)
	if c > 0 {
		return true
	}
	if c < 0 {
		return false
	}
	// Equal boundary: a gap exists only when both sides exclude it.
	return a.Upper.Inclusive || b.Lower.Inclusive
}

func mergeIntervals(a, b Interval) Interval {
	out := a
	if a.Upper == nil || b.Upper == nil {
		out.Upper = nil
	} else {
		c := a.Upper.Version.Compare(b.Upper.Version)
		switch {
		case c < 0:
			out.Upper = cloneBound(b.Upper)
		case c == 0:
			out.Upper = cloneBound(a.Upper)
			out.Upper.Inclusive = a.Upper.Inclusive || b.Upper.Inclusive
		}
	}
	return out
}

func dedupeVersions(in []Version) []Version {
	if len(in) == 0 {
		return nil
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].Compare(in[j]) < 0 })
	out := make([]Version, 0, len(in))
	for _, v := range in {
		if len(out) == 0 || out[len(out)-1].Compare(v) != 0 {
			out = append(out, v)
		}
	}
	return out
}

func (r *VersionRange) simplifyExclusions() {
	if r == nil {
		return
	}
	r.Excluded = dedupeVersions(r.Excluded)
	var remaining []Version
	for _, ex := range r.Excluded {
		handled := false
		for i := range r.Intervals {
			iv := &r.Intervals[i]
			if iv.Lower != nil && iv.Lower.Inclusive && iv.Lower.Version.Compare(ex) == 0 {
				iv.Lower.Inclusive = false
				handled = true
				continue
			}
			if iv.Upper != nil && iv.Upper.Inclusive && iv.Upper.Version.Compare(ex) == 0 {
				iv.Upper.Inclusive = false
				handled = true
				continue
			}
		}
		if !handled && r.containsIgnoringExclusions(ex) {
			remaining = append(remaining, ex)
		}
	}
	filtered := r.Intervals[:0]
	for _, iv := range r.Intervals {
		if !intervalEmpty(iv) {
			filtered = append(filtered, iv)
		}
	}
	r.Intervals = filtered
	r.Excluded = dedupeVersions(remaining)
}

func (r *VersionRange) containsIgnoringExclusions(v Version) bool {
	if r == nil {
		return false
	}
	for _, iv := range r.Intervals {
		if intervalContains(iv, v) {
			return true
		}
	}
	return false
}

func intervalContains(iv Interval, v Version) bool {
	if iv.Lower != nil {
		c := v.Compare(iv.Lower.Version)
		if c < 0 || c == 0 && !iv.Lower.Inclusive {
			return false
		}
	}
	if iv.Upper != nil {
		c := v.Compare(iv.Upper.Version)
		if c > 0 || c == 0 && !iv.Upper.Inclusive {
			return false
		}
	}
	return true
}

// Contains reports whether version is in the range.
func (r *VersionRange) Contains(version string) bool {
	if r == nil {
		return false
	}
	v, err := ParseVersion(version)
	if err != nil {
		// Fall back to loose comparison for non-SemVer ecosystems.
		return r.containsLoose(version)
	}
	return r.containsParsed(v)
}

// ContainsVersion is an alias for Contains.
func (r *VersionRange) ContainsVersion(version string) bool { return r.Contains(version) }

// Matches is an alias for Contains.
func (r *VersionRange) Matches(version string) bool { return r.Contains(version) }

// Satisfies is an alias for Contains.
func (r *VersionRange) Satisfies(version string) bool { return r.Contains(version) }

func (r *VersionRange) containsParsed(v Version) bool {
	if !r.containsIgnoringExclusions(v) {
		return false
	}
	for _, ex := range r.Excluded {
		if ex.Compare(v) == 0 {
			return false
		}
	}
	return true
}

func (r *VersionRange) containsLoose(version string) bool {
	if r == nil {
		return false
	}
	for _, iv := range r.Intervals {
		if iv.Lower != nil {
			c := compareLoose(version, iv.Lower.Version.String())
			if c < 0 || c == 0 && !iv.Lower.Inclusive {
				continue
			}
		}
		if iv.Upper != nil {
			c := compareLoose(version, iv.Upper.Version.String())
			if c > 0 || c == 0 && !iv.Upper.Inclusive {
				continue
			}
		}
		excluded := false
		for _, ex := range r.Excluded {
			if compareLoose(version, ex.String()) == 0 {
				excluded = true
				break
			}
		}
		if !excluded {
			return true
		}
	}
	return false
}

// Empty reports whether the range contains no versions.
func (r *VersionRange) Empty() bool {
	return r == nil || len(r.Intervals) == 0
}

// IsUnbounded reports whether the range contains every version.
func (r *VersionRange) IsUnbounded() bool {
	if r == nil || len(r.Intervals) != 1 || len(r.Excluded) != 0 {
		return false
	}
	iv := r.Intervals[0]
	return iv.Lower == nil && iv.Upper == nil
}

// ExactVersion returns the only version in the range, if it contains exactly
// one version.
func (r *VersionRange) ExactVersion() (string, bool) {
	if r == nil || len(r.Intervals) != 1 || len(r.Excluded) != 0 {
		return "", false
	}
	iv := r.Intervals[0]
	if iv.Lower == nil || iv.Upper == nil {
		return "", false
	}
	if !iv.Lower.Inclusive || !iv.Upper.Inclusive {
		return "", false
	}
	if iv.Lower.Version.Compare(iv.Upper.Version) != 0 {
		return "", false
	}
	return iv.Lower.Version.String(), true
}

// Intersects reports whether two ranges share at least one version.
func (r *VersionRange) Intersects(other *VersionRange) bool {
	if r == nil || other == nil {
		return false
	}
	for _, a := range r.Intervals {
		for _, b := range other.Intervals {
			iv, ok := intersectIntervals(a, b)
			if !ok {
				continue
			}
			if iv.Lower != nil && iv.Upper != nil && iv.Lower.Version.Compare(iv.Upper.Version) == 0 {
				v := iv.Lower.Version
				if r.containsParsed(v) && other.containsParsed(v) {
					return true
				}
				continue
			}
			return true
		}
	}
	return false
}

// Union returns the union of two ranges.
func (r *VersionRange) Union(other *VersionRange) *VersionRange {
	if r == nil {
		return other
	}
	if other == nil {
		return r
	}
	out := &VersionRange{
		Intervals: normalizeIntervals(append(append([]Interval(nil), r.Intervals...), other.Intervals...)),
		Excluded:  dedupeVersions(append(append([]Version(nil), r.Excluded...), other.Excluded...)),
	}
	out.simplifyExclusions()
	return out
}

// Intersect returns the intersection of two ranges.
func (r *VersionRange) Intersect(other *VersionRange) *VersionRange {
	if r == nil || other == nil {
		return &VersionRange{}
	}
	var intervals []Interval
	for _, a := range r.Intervals {
		for _, b := range other.Intervals {
			if iv, ok := intersectIntervals(a, b); ok {
				intervals = append(intervals, iv)
			}
		}
	}
	out := &VersionRange{
		Intervals: normalizeIntervals(intervals),
		Excluded:  dedupeVersions(append(append([]Version(nil), r.Excluded...), other.Excluded...)),
	}
	out.simplifyExclusions()
	return out
}

// Exclude removes a single version from the range.
func (r *VersionRange) Exclude(version string) error {
	v, err := ParseVersion(version)
	if err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	if r.containsIgnoringExclusions(v) {
		r.Excluded = append(r.Excluded, v)
		r.simplifyExclusions()
	}
	return nil
}

// String returns a deterministic interval spelling of the range.
func (r *VersionRange) String() string {
	if r == nil || len(r.Intervals) == 0 {
		return "<empty>"
	}
	parts := make([]string, 0, len(r.Intervals))
	for _, iv := range r.Intervals {
		text := formatInterval(iv)
		for _, ex := range r.Excluded {
			if intervalContains(iv, ex) {
				text += " !=" + ex.String()
			}
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " || ")
}

func formatInterval(iv Interval) string {
	if iv.Lower == nil && iv.Upper == nil {
		return "*"
	}
	if iv.Lower != nil && iv.Upper != nil {
		c := iv.Lower.Version.Compare(iv.Upper.Version)
		if c == 0 && iv.Lower.Inclusive && iv.Upper.Inclusive {
			return iv.Lower.Version.String()
		}
	}
	var parts []string
	if iv.Lower != nil {
		op := ">="
		if !iv.Lower.Inclusive {
			op = ">"
		}
		parts = append(parts, op+iv.Lower.Version.String())
	}
	if iv.Upper != nil {
		op := "<="
		if !iv.Upper.Inclusive {
			op = "<"
		}
		parts = append(parts, op+iv.Upper.Version.String())
	}
	return strings.Join(parts, " ")
}

// VersionInRange reports whether version is contained in rangeExpr.
func VersionInRange(version, rangeExpr string) (bool, error) {
	r, err := ParseVersionRange(rangeExpr)
	if err != nil {
		return false, err
	}
	return r.Contains(version), nil
}

// InRange is an alias for VersionInRange.
func InRange(version, rangeExpr string) (bool, error) {
	return VersionInRange(version, rangeExpr)
}

// Satisfies reports whether version satisfies rangeExpr.
func Satisfies(version, rangeExpr string) (bool, error) {
	return VersionInRange(version, rangeExpr)
}

// MatchesVersion reports whether version satisfies rangeExpr.  The arguments
// may be supplied in either order; the range expression is identified by its
// syntax.
func MatchesVersion(a, b string) (bool, error) {
	if detectVersionRange(b) {
		return VersionInRange(a, b)
	}
	if detectVersionRange(a) {
		return VersionInRange(b, a)
	}
	return VersionInRange(a, b)
}

// MatchVersion is an alias for MatchesVersion.
func MatchVersion(a, b string) (bool, error) { return MatchesVersion(a, b) }
