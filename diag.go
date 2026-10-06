// Package diag aggregates diagnostics produced by multiple analysis
// phases over a document that keeps evolving under edits.
//
// A diagnostic's merge identity is the triple
// (file version, original range, rule id): two diagnostics with the
// same message text but different ranges never merge, and two
// diagnostics whose ranges only coincide after mapping to the current
// text never merge either. A phase refresh replaces exactly that
// phase's contributions; the merged severity of an identity is the
// maximum severity contributed by any phase.
package diag

// Severity of a diagnostic. Higher values are more severe; the merged
// severity of an identity is the maximum across contributing phases.
type Severity int

const (
	SeverityHint Severity = iota
	SeverityInfo
	SeverityWarning
	SeverityError
)

func (s Severity) String() string {
	switch s {
	case SeverityHint:
		return "hint"
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	default:
		return "unknown"
	}
}

// Range is a half-open byte-offset interval [Start, End) in the text
// of a specific file version.
type Range struct {
	Start, End int
}

// Diagnostic is a single finding contributed by one analysis phase.
// Its Range is expressed in the coordinates of the file version the
// phase analyzed.
type Diagnostic struct {
	RuleID   string
	Range    Range
	Severity Severity
	Message  string
}

// Key is the merge identity of a diagnostic: the file version it was
// analyzed against, its range in that version's coordinates, and the
// rule that produced it. Message text is deliberately not part of the
// identity, and neither is the range mapped to the current version.
type Key struct {
	Version FileVersion
	RuleID  string
	Range   Range
}

// Merged is the aggregated view of all phase contributions that share
// one identity.
type Merged struct {
	RuleID string
	// Range is the identity range mapped to the snapshot's document
	// version. Distinct identities may share this range.
	Range Range
	// Severity is the maximum severity across contributing phases.
	Severity Severity
	// Message comes from a highest-severity contribution (ties break
	// deterministically by phase name).
	Message string
	// Origin is the identity this diagnostic merged under: the
	// original analysis version and range.
	Origin Key
	// Phases lists the contributing phases, sorted by name.
	Phases []string
}
