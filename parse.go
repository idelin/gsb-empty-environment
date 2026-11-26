package temporal

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseRange parses the mixed-bracket interval notation used by the package.
//
// Supported forms include "[a,b)", "(a,b]", "(a,b)", "[a,b]", unbounded
// "(-inf,b)", "(a,+inf)" / "[a,)", "[,b)" and the empty words "empty" /
// "∅". When kind is omitted the domain is inferred: date/time-looking
// endpoints or a trailing unit marker select Continuous, plain integers
// select Discrete.
func ParseRange(s string, kind ...Kind) (Range, error) {
	k := inferKind(s, kind)
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Range{}, fmt.Errorf("temporal: empty range expression")
	}
	lowered := strings.ToLower(strings.TrimSpace(raw))
	if lowered == "empty" || lowered == "∅" || lowered == "null" {
		return Empty(k), nil
	}

	r, err := parseBracketRange(raw, k)
	if err != nil {
		return Range{}, err
	}
	return r.Normalize(), nil
}

// MustParseRange is ParseRange that panics on error.
func MustParseRange(s string, kind ...Kind) Range {
	r, err := ParseRange(s, kind...)
	if err != nil {
		panic(err)
	}
	return r
}

// ParseIntRange parses a discrete integer range and normalizes it to [lo,hi).
func ParseIntRange(s string) (Range, error) { return ParseRange(s, Discrete) }

// MustParseIntRange is ParseIntRange that panics on error.
func MustParseIntRange(s string) Range { return MustParseRange(s, Discrete) }

// ParseTimeRange parses a continuous microsecond-timestamp range. Numeric
// endpoints are microseconds; RFC3339 / "2006-01-02 15:04:05" / date-only
// endpoints are converted to Unix microseconds (UTC).
func ParseTimeRange(s string) (Range, error) { return ParseRange(s, Continuous) }

// MustParseTimeRange is ParseTimeRange that panics on error.
func MustParseTimeRange(s string) Range { return MustParseRange(s, Continuous) }

// Parse is an alias for ParseRange with inferred kind.
func Parse(s string) (Range, error) { return ParseRange(s) }

// MustParse is MustParseRange with inferred kind.
func MustParse(s string) Range { return MustParseRange(s) }

func inferKind(s string, explicit []Kind) Kind {
	if len(explicit) > 0 {
		return explicit[0]
	}
	t := strings.TrimSpace(s)
	// Strip brackets.
	t = strings.TrimLeft(t, "[(<")
	t = strings.TrimRight(t, "])>")
	parts := splitEndpoints(t)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || isInfToken(p) {
			continue
		}
		if looksLikeTime(p) {
			return Continuous
		}
	}
	return Discrete
}

func parseBracketRange(s string, k Kind) (Range, error) {
	first := s[0]
	last := s[len(s)-1]

	var lowerInclusive bool
	switch first {
	case '[':
		lowerInclusive = true
	case '(', ']':
		lowerInclusive = false
	default:
		return Range{}, fmt.Errorf("temporal: invalid opening bracket %q in %q", first, s)
	}

	var upperInclusive bool
	switch last {
	case ']':
		upperInclusive = true
	case ')', '[':
		upperInclusive = false
	default:
		return Range{}, fmt.Errorf("temporal: invalid closing bracket %q in %q", last, s)
	}

	inner := strings.TrimSpace(s[1 : len(s)-1])
	parts := splitEndpoints(inner)
	if len(parts) != 2 {
		return Range{}, fmt.Errorf("temporal: expected two endpoints in %q", s)
	}

	r := Range{Kind: k, LowerInclusive: lowerInclusive, UpperInclusive: upperInclusive}

	lo, loUnb, err := parseEndpoint(parts[0], k, -1)
	if err != nil {
		return Range{}, fmt.Errorf("temporal: lower endpoint: %w", err)
	}
	if loUnb {
		r.LowerUnbounded = true
		r.LowerInclusive = false
	} else {
		r.Lower = lo
	}

	hi, hiUnb, err := parseEndpoint(parts[1], k, +1)
	if err != nil {
		return Range{}, fmt.Errorf("temporal: upper endpoint: %w", err)
	}
	if hiUnb {
		r.UpperUnbounded = true
		r.UpperInclusive = false
	} else {
		r.Upper = hi
	}

	return r, nil
}

func splitEndpoints(inner string) []string {
	// Split on the first comma outside of any date/time punctuation. Dates
	// contain dashes/colons/spaces but never commas, so a plain comma split
	// is sufficient; also accept the Chinese comma.
	inner = strings.ReplaceAll(inner, "，", ",")
	return strings.SplitN(inner, ",", 2)
}

func isInfToken(p string) bool {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", "-inf", "+inf", "inf", "-infinity", "+infinity", "infinity", "-∞", "+∞", "∞", "null", "nil", "~":
		return true
	}
	return false
}

func parseEndpoint(p string, k Kind, side int) (int64, bool, error) {
	tok := strings.TrimSpace(strings.Trim(p, `"'`))
	lower := strings.ToLower(tok)
	switch lower {
	case "-inf", "-infinity", "-∞":
		return 0, true, nil
	case "+inf", "inf", "+infinity", "infinity", "+∞", "∞":
		return 0, true, nil
	case "", "null", "nil", "~":
		// Empty endpoint position means unbounded, e.g. "[,5)".
		return 0, true, nil
	}

	if k == Continuous && looksLikeTime(tok) {
		us, err := parseTimeToMicro(tok)
		return us, false, err
	}

	n, err := strconv.ParseInt(tok, 10, 64)
	if err != nil {
		// Maybe a float seconds timestamp.
		if f, ferr := strconv.ParseFloat(tok, 64); ferr == nil && k == Continuous {
			return int64(f * 1e6), false, nil
		}
		return 0, false, fmt.Errorf("invalid endpoint %q: %w", tok, err)
	}
	return n, false, nil
}

func looksLikeTime(tok string) bool {
	if strings.ContainsAny(tok, "Tt :") && strings.ContainsAny(tok, ":-") {
		// Avoid treating negative integers or "a-b" as times.
		if _, err := strconv.ParseInt(tok, 10, 64); err != nil {
			if strings.Count(tok, "-") >= 2 || strings.Contains(tok, ":") {
				return true
			}
		}
	}
	if len(tok) >= 8 {
		if _, err := time.Parse("20060102", tok); err == nil {
			return true
		}
	}
	return false
}

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05.999999",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"20060102",
}

func parseTimeToMicro(tok string) (int64, error) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, tok); err == nil {
			return t.UTC().UnixMicro(), nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", tok, time.UTC); err == nil {
		return t.UnixMicro(), nil
	}
	return 0, fmt.Errorf("unrecognized timestamp %q", tok)
}
