// Package diagnostics merges diagnostic contributions from multiple
// analysis stages and keeps their ranges valid across document edits.
//
// A diagnostic's identity is the triple (file version, original range,
// rule ID): two contributions merge only when all three agree. Identical
// message text at different ranges, or the same range produced by
// analyses of different file versions, never merge.
package diagnostics

// Severity ranks how serious a diagnostic is. Higher values win when
// duplicate contributions merge.
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
	}
	return "unknown"
}

// Range is a half-open interval [Start, End) of byte offsets.
type Range struct {
	Start, End int
}

// Len returns the length of the range in bytes.
func (r Range) Len() int { return r.End - r.Start }

// Diagnostic is a single finding reported by one analysis stage. Range is
// expressed in the coordinates of the analysis version the stage ran
// against, which is supplied separately to File.Submit.
type Diagnostic struct {
	RuleID   string
	Severity Severity
	Message  string
	Range    Range
}

// Identity uniquely identifies a logical diagnostic: the file version the
// analysis ran against, the range in that version's coordinates, and the
// rule that produced it. Message text is deliberately not part of the
// identity.
type Identity struct {
	Version uint64
	Range   Range
	RuleID  string
}
