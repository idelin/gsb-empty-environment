package pathsec

import (
	"errors"
	"fmt"
)

// Status is the shared validation result taxonomy.
type Status string

const (
	StatusValid   Status = "Valid"
	StatusInvalid Status = "Invalid"
	StatusUnknown Status = "Unknown"

	// StatusNotFound is an alias used by ROA validation.
	StatusNotFound Status = "NotFound"

	// StatusSkipped indicates that a subsystem was not configured or
	// not evaluated. It is never returned by the strict combined check
	// when that subsystem is required.
	StatusSkipped Status = "Skipped"
)

// Validation aliases keep call sites readable.
const (
	Valid    = StatusValid
	Invalid  = StatusInvalid
	Unknown  = StatusUnknown
	NotFound = StatusNotFound
)

// Common sentinel errors. Detailed results also carry human-readable reasons.
var (
	ErrEmptyPath      = errors.New("pathsec: empty AS_PATH")
	ErrASSet          = errors.New("pathsec: AS_SET is not supported")
	ErrInvalidPrefix  = errors.New("pathsec: invalid IP prefix")
	ErrInvalidASN     = errors.New("pathsec: invalid AS number")
	ErrInvalidPCount  = errors.New("pathsec: invalid BGPsec pCount")
	ErrCountMismatch  = errors.New("pathsec: BGPsec path/signature count mismatch")
	ErrNoTrustedKey   = errors.New("pathsec: no trusted BGPsec key")
	ErrBadSignature   = errors.New("pathsec: BGPsec signature verification failed")
	ErrTargetAS       = errors.New("pathsec: BGPsec target AS mismatch")
	ErrPrefixBinding  = errors.New("pathsec: BGPsec prefix binding mismatch")
	ErrTruncatedChain = errors.New("pathsec: truncated BGPsec predecessor chain")
	ErrGraphLimit     = errors.New("pathsec: adversarial graph supports at most 12 AS nodes")
)

// Reason is a structured validation finding.
type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	ASN     uint32 `json:"asn,omitempty"`
	Index   int    `json:"index,omitempty"`
}

func (r Reason) String() string {
	if r.Code == "" {
		return r.Message
	}
	if r.Message == "" {
		return r.Code
	}
	return fmt.Sprintf("%s: %s", r.Code, r.Message)
}

func statusWorse(a, b Status) Status {
	rank := map[Status]int{
		StatusSkipped:  0,
		StatusValid:    1,
		StatusNotFound: 2,
		StatusUnknown:  2,
		StatusInvalid:  3,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
