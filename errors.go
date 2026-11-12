package netsec

import (
	"errors"
	"fmt"
)

// ErrorKind classifies a chain verification failure.
type ErrorKind string

const (
	KindInvalid           ErrorKind = "invalid_input"
	KindFork              ErrorKind = "fork"
	KindWindowGap         ErrorKind = "window_gap"
	KindSequenceGap       ErrorKind = "sequence_gap"
	KindCountMismatch     ErrorKind = "count_mismatch"
	KindPrevHashMismatch  ErrorKind = "previous_hash_mismatch"
	KindEntryHashMismatch ErrorKind = "entry_hash_mismatch"
	KindAnchorMismatch    ErrorKind = "anchor_mismatch"
	KindAnchorMissing     ErrorKind = "anchor_missing"
	KindAnchorConflict    ErrorKind = "anchor_conflict"
)

var (
	// ErrInvalid reports malformed verification input.
	ErrInvalid = errors.New("audit chain: invalid input")
	// ErrFork reports one window number carrying two different window heads.
	ErrFork = errors.New("audit chain: window fork")
	// ErrWindowGap reports non-adjacent window numbers.
	ErrWindowGap = errors.New("audit chain: window number gap")
	// ErrSequenceGap reports a missing, overlapping, or out-of-order sequence.
	ErrSequenceGap = errors.New("audit chain: sequence gap")
	// ErrCountMismatch reports a header count that differs from actual entries.
	ErrCountMismatch = errors.New("audit chain: window count mismatch")
	// ErrPrevHashMismatch reports a broken link to the previous window head.
	ErrPrevHashMismatch = errors.New("audit chain: previous window hash mismatch")
	// ErrEntryHashMismatch reports a stored entry hash that does not recompute.
	ErrEntryHashMismatch = errors.New("audit chain: entry hash mismatch")
	// ErrAnchorMismatch reports a recomputed chain head that differs from an
	// externally published anchor.
	ErrAnchorMismatch = errors.New("audit chain: anchor mismatch")
	// ErrAnchorMissing reports an anchor whose sequence is absent from the
	// supplied archive, including an anchor beyond a truncated tail.
	ErrAnchorMissing = errors.New("audit chain: anchor sequence missing")
	// ErrAnchorConflict reports two immutable anchors with different hashes at
	// the same sequence number.
	ErrAnchorConflict = errors.New("audit chain: conflicting anchors")
)

// Compatibility aliases for callers using shorter sentinel names.
var (
	ErrSeqGap        = ErrSequenceGap
	ErrBadCount      = ErrCountMismatch
	ErrHashMismatch  = ErrEntryHashMismatch
	ErrPrevMismatch  = ErrPrevHashMismatch
	ErrMissingAnchor = ErrAnchorMissing
)

// ChainError is a structured verification failure.
type ChainError struct {
	Kind   ErrorKind
	Window uint64
	Seq    uint64
	Detail string
	Want   []byte
	Got    []byte
}

func (e *ChainError) Error() string {
	where := ""
	if e.Window != 0 {
		where = fmt.Sprintf(" window %d", e.Window)
	}
	if e.Seq != 0 {
		where += fmt.Sprintf(" seq %d", e.Seq)
	}
	if e.Detail != "" {
		if where == "" {
			return fmt.Sprintf("audit chain: %s: %s", e.Kind, e.Detail)
		}
		return fmt.Sprintf("audit chain: %s:%s: %s", e.Kind, where, e.Detail)
	}
	if where == "" {
		return fmt.Sprintf("audit chain: %s", e.Kind)
	}
	return fmt.Sprintf("audit chain: %s:%s", e.Kind, where)
}

// Unwrap maps a structured failure to its sentinel so callers can use
// errors.Is(err, ErrFork), errors.Is(err, ErrAnchorMismatch), etc.
func (e *ChainError) Unwrap() error {
	switch e.Kind {
	case KindFork:
		return ErrFork
	case KindWindowGap:
		return ErrWindowGap
	case KindSequenceGap:
		return ErrSequenceGap
	case KindCountMismatch:
		return ErrCountMismatch
	case KindPrevHashMismatch:
		return ErrPrevHashMismatch
	case KindEntryHashMismatch:
		return ErrEntryHashMismatch
	case KindAnchorMismatch:
		return ErrAnchorMismatch
	case KindAnchorMissing:
		return ErrAnchorMissing
	case KindAnchorConflict:
		return ErrAnchorConflict
	default:
		return ErrInvalid
	}
}

// ForkError reports a fork: a single window number was observed with two
// different window heads (different headers, or identical headers with
// different contents).
type ForkError struct {
	Window          uint64
	Existing        WindowHeader
	Conflicting     WindowHeader
	ExistingHead    []byte
	ConflictingHead []byte
}

func (e *ForkError) Error() string {
	if headersEqual(e.Existing, e.Conflicting) {
		return fmt.Sprintf(
			"audit chain: fork at window %d: identical headers but different content (heads %x / %x)",
			e.Window, e.ExistingHead, e.ConflictingHead,
		)
	}
	return fmt.Sprintf(
		"audit chain: fork at window %d: conflicting headers (start %d/%d, count %d/%d, prev %x/%x)",
		e.Window,
		e.Existing.StartSeq, e.Conflicting.StartSeq,
		e.Existing.Count, e.Conflicting.Count,
		e.Existing.PrevHash, e.Conflicting.PrevHash,
	)
}

// Unwrap maps a fork to ErrFork.
func (e *ForkError) Unwrap() error { return ErrFork }

// Report is the result of a full archive check. It retains every detected
// issue rather than stopping at the first one.
type Report struct {
	Windows int
	Entries uint64
	Anchors int
	Issues  []error
}

// OK reports whether no issue was found.
func (r *Report) OK() bool { return r != nil && len(r.Issues) == 0 }

// Err returns the first issue, or nil when the chain is valid.
func (r *Report) Err() error {
	if r == nil || len(r.Issues) == 0 {
		return nil
	}
	return r.Issues[0]
}

// Forks returns every reported fork.
func (r *Report) Forks() []*ForkError {
	if r == nil {
		return nil
	}
	var forks []*ForkError
	for _, issue := range r.Issues {
		var fork *ForkError
		if errors.As(issue, &fork) {
			forks = append(forks, fork)
		}
	}
	return forks
}

func (r *Report) add(err error) {
	if err != nil {
		r.Issues = append(r.Issues, err)
	}
}

func newChainError(kind ErrorKind, window, seq uint64, detail string) *ChainError {
	return &ChainError{Kind: kind, Window: window, Seq: seq, Detail: detail}
}

func headersEqual(a, b WindowHeader) bool {
	return a.Number == b.Number &&
		a.StartSeq == b.StartSeq &&
		a.Count == b.Count &&
		bytesEqual(a.PrevHash, b.PrevHash)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
