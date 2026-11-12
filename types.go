// Package netsec provides a result cache for a file detection service.
//
// The cache stores per-file detection conclusions produced by different
// analysis tiers (for example a quick screen and a full analysis). A cached
// conclusion is only reusable when every check requested by a caller has
// actually been executed: a clean quick screen must never be presented as a
// clean full analysis. When several conclusions for the same content and rule
// version are merged, positive evidence always survives any conflicting
// negative result.
package netsec

import (
	"errors"
	"time"
)

// CheckID identifies a single detection check (signature scan, heuristic,
// archive extraction, and so on).
type CheckID string

// Verdict is the outcome of one executed check.
type Verdict int

const (
	// VerdictNegative means the check executed and found no threat.
	VerdictNegative Verdict = iota
	// VerdictPositive means the check executed and found a threat.
	VerdictPositive
)

// String returns the textual representation of a verdict.
func (v Verdict) String() string {
	switch v {
	case VerdictNegative:
		return "negative"
	case VerdictPositive:
		return "positive"
	default:
		return "unknown"
	}
}

// Key addresses a cache entry. It binds the content digest of the scanned
// object together with the exact detection rule version used, so a rule
// upgrade always misses old entries and can never silently reuse them.
type Key struct {
	// ContentDigest is an opaque content hash, e.g. "sha256:...".
	ContentDigest string
	// RulesVersion identifies the detection rule set that produced the
	// conclusion.
	RulesVersion string
}

// CheckResult is the outcome of one check that actually ran.
type CheckResult struct {
	Check   CheckID
	Verdict Verdict
	// Evidence describes why a positive verdict was raised. It is retained
	// verbatim when conclusions are merged and must never be dropped.
	Evidence []string
	// RanAt records when the check executed.
	RanAt time.Time
}

// Status is the three-state interpretation of a conclusion.
type Status int

const (
	// StatusIncomplete means at least one check did not finish.
	StatusIncomplete Status = iota
	// StatusClean means every recorded check finished negative with no
	// outstanding work.
	StatusClean
	// StatusThreat means at least one recorded check returned positive.
	StatusThreat
)

// String returns the textual representation of a status.
func (s Status) String() string {
	switch s {
	case StatusIncomplete:
		return "incomplete"
	case StatusClean:
		return "clean"
	case StatusThreat:
		return "threat"
	default:
		return "unknown"
	}
}

// Conclusion is a cached detection outcome for one Key.
//
// Results only contains checks that actually executed; Pending lists checks
// that were requested but did not finish (for example because the operation
// was canceled). A quick screen therefore looks like a partial conclusion with
// the full-analysis checks still pending rather than a clean one.
type Conclusion struct {
	Key     Key
	Results []CheckResult
	Pending []CheckID
	// UpdatedAt is the time of the most recent incorporated result.
	UpdatedAt time.Time
}

var (
	// ErrKeyMismatch is returned when Merge receives conclusions that do not
	// share the same Key, preventing cross-content or cross-rule-version
	// contamination.
	ErrKeyMismatch = errors.New("netsec: conclusion key mismatch")
	// ErrInvalidVerdict is returned when a check carries an unknown verdict.
	ErrInvalidVerdict = errors.New("netsec: invalid check verdict")
	// ErrInvalidCheck is returned when an empty check identifier is used.
	ErrInvalidCheck = errors.New("netsec: empty check id")
	// ErrInvalidKey is returned when a cache key is missing its digest or rule
	// version.
	ErrInvalidKey = errors.New("netsec: cache key requires content digest and rules version")
)

// NewNegativeResult builds a clean CheckResult for an executed check.
func NewNegativeResult(check CheckID, ranAt time.Time) CheckResult {
	return CheckResult{Check: check, Verdict: VerdictNegative, RanAt: ranAt}
}

// NewPositiveResult builds a positive CheckResult carrying the evidence that
// explains the finding.
func NewPositiveResult(check CheckID, evidence []string, ranAt time.Time) CheckResult {
	return CheckResult{
		Check:    check,
		Verdict:  VerdictPositive,
		Evidence: append([]string(nil), evidence...),
		RanAt:    ranAt,
	}
}
