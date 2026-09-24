package negcache

import (
	"context"
	"errors"
	"net"
)

// ErrorClass classifies a source failure. Each class has its own TTL and
// cacheable policy in the cache.
type ErrorClass int

const (
	// ClassNone means the call succeeded; no error.
	ClassNone ErrorClass = iota
	// ClassNotFound means the key does not exist at the source (negative result).
	ClassNotFound
	// ClassTemporary means a transient failure (e.g. network timeout); retry later.
	ClassTemporary
	// ClassPermanent means a non-retryable failure other than not-found.
	ClassPermanent
	// ClassCanceled means the caller/context was canceled. Never shared-cached.
	ClassCanceled
)

func (c ErrorClass) String() string {
	switch c {
	case ClassNone:
		return "none"
	case ClassNotFound:
		return "not-found"
	case ClassTemporary:
		return "temporary"
	case ClassPermanent:
		return "permanent"
	case ClassCanceled:
		return "canceled"
	}
	return "unknown"
}

// ErrNotFound is the canonical sentinel a Source returns for a missing key.
var ErrNotFound = errors.New("negcache: not found")

// TempError simulates a temporary, network-style failure. It implements
// net.Error so the default classifier treats it as temporary, never as
// not-found.
type TempError struct{ Msg string }

func (e *TempError) Error() string   { return e.Msg }
func (e *TempError) Timeout() bool   { return true }
func (e *TempError) Temporary() bool { return true }

// Classifier maps a source error to an ErrorClass. Injectable via Config.
type Classifier func(err error) ErrorClass

// DefaultClassifier classifies:
//   - context.Canceled                         -> ClassCanceled
//   - ErrNotFound                              -> ClassNotFound
//   - net.Error (timeout/temporary net errors) -> ClassTemporary
//   - anything else                            -> ClassPermanent
//
// A temporary network-style error is therefore never mistaken for not-found.
func DefaultClassifier(err error) ErrorClass {
	if err == nil {
		return ClassNone
	}
	if errors.Is(err, context.Canceled) {
		return ClassCanceled
	}
	if errors.Is(err, ErrNotFound) {
		return ClassNotFound
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return ClassTemporary
	}
	return ClassPermanent
}
