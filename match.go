package netsec

import "strings"

// MatchMode selects how a StringMatch is compared against an input value.
type MatchMode int

const (
	// MatchExact matches when the input equals Value.
	MatchExact MatchMode = iota
	// MatchPrefix matches when the input starts with Value.
	MatchPrefix
	// MatchSuffix matches when the input ends with Value.
	MatchSuffix
	// MatchContains matches when Value is a substring of the input.
	MatchContains
	// MatchPresence only checks that the input is non-empty; Value is ignored.
	// It models an Istio presence match such as requestPrincipals: ["*"].
	MatchPresence
)

// StringMatch is a deliberately small subset of Istio's StringMatch:
// exact/prefix/suffix/contains plus a presence check.
type StringMatch struct {
	Mode  MatchMode
	Value string
}

// Exact, Prefix, Suffix, Contains and Presence are constructors that make the
// supported match modes read clearly at call sites.
func Exact(v string) StringMatch    { return StringMatch{Mode: MatchExact, Value: v} }
func Prefix(v string) StringMatch   { return StringMatch{Mode: MatchPrefix, Value: v} }
func Suffix(v string) StringMatch   { return StringMatch{Mode: MatchSuffix, Value: v} }
func Contains(v string) StringMatch { return StringMatch{Mode: MatchContains, Value: v} }
func Presence() StringMatch         { return StringMatch{Mode: MatchPresence} }

// Matches reports whether value satisfies the matcher.
func (m StringMatch) Matches(value string) bool {
	switch m.Mode {
	case MatchPrefix:
		return strings.HasPrefix(value, m.Value)
	case MatchSuffix:
		return strings.HasSuffix(value, m.Value)
	case MatchContains:
		return strings.Contains(value, m.Value)
	case MatchPresence:
		return value != ""
	default: // MatchExact, including the zero-value StringMatch
		return value == m.Value
	}
}
