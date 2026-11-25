package archive

import "errors"

// Reason classifies a planning, execution or audit failure.  In particular,
// ReasonNoFeasiblePlan (a proven cost-ceiling failure) is distinct from
// ReasonInsufficientEvidence (facts needed to decide are missing or invalid).
type Reason uint8

const (
	ReasonNone Reason = iota
	ReasonUnauthorized
	ReasonNoFeasiblePlan
	ReasonInsufficientEvidence
	ReasonReturnQuota
	ReasonInvalidInput
	ReasonInvalidPlan
	ReasonIncompleteSearch
	ReasonLeak
)

var (
	ErrUnauthorized         = errors.New("archive: request exceeds tenant grant")
	ErrNoFeasiblePlan       = errors.New("archive: no plan within cost ceiling")
	ErrInsufficientEvidence = errors.New("archive: insufficient evidence")
	ErrReturnQuota          = errors.New("archive: returned-data quota exceeded")
	ErrInvalidInput         = errors.New("archive: invalid input")
	ErrInvalidPlan          = errors.New("archive: invalid plan")
	ErrIncompleteSearch     = errors.New("archive: search incomplete")
	ErrLeak                 = errors.New("archive: unauthorized or out-of-bounds byte exposure")
)

func sentinelFor(r Reason) error {
	switch r {
	case ReasonUnauthorized:
		return ErrUnauthorized
	case ReasonNoFeasiblePlan:
		return ErrNoFeasiblePlan
	case ReasonInsufficientEvidence:
		return ErrInsufficientEvidence
	case ReasonReturnQuota:
		return ErrReturnQuota
	case ReasonInvalidInput:
		return ErrInvalidInput
	case ReasonInvalidPlan:
		return ErrInvalidPlan
	case ReasonIncompleteSearch:
		return ErrIncompleteSearch
	case ReasonLeak:
		return ErrLeak
	}
	return nil
}

// Error is the package's structured error.  It supports errors.Is against the
// sentinel errors above.
type Error struct {
	Reason Reason
	Op     string
	Detail string
	Cause  error
}

func (e *Error) Error() string {
	msg := e.Op
	if e.Detail != "" {
		if msg != "" {
			msg += ": "
		}
		msg += e.Detail
	}
	if e.Cause != nil {
		if msg != "" {
			msg += ": "
		}
		msg += e.Cause.Error()
	}
	if msg == "" {
		if s := sentinelFor(e.Reason); s != nil {
			return s.Error()
		}
		return "archive: error"
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) Is(target error) bool {
	return target == sentinelFor(e.Reason)
}

func planErr(reason Reason, op, detail string, cause error) error {
	return &Error{Reason: reason, Op: op, Detail: detail, Cause: cause}
}

// ReasonOf extracts the Reason from a package error, if present.
func ReasonOf(err error) Reason {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ReasonNone
}
