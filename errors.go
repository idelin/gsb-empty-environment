package negcache

import (
	"context"
	"errors"
	"fmt"
)

// Class is the error classification attached to every negative result.
type Class int

const (
	ClassNone Class = iota
	ClassNotFound
	ClassTransient
	ClassPermanent
	ClassCanceled
)

func (c Class) String() string {
	switch c {
	case ClassNotFound:
		return "not-found"
	case ClassTransient:
		return "transient"
	case ClassPermanent:
		return "permanent"
	case ClassCanceled:
		return "canceled"
	default:
		return "none"
	}
}

// ErrNotFound is a sentinel a source may wrap to signal a genuine miss.
var ErrNotFound = errors.New("not found")

// SourceError is a classified error coming from the data source.
type SourceError struct {
	Class Class
	Err   error
}

func (e *SourceError) Error() string { return fmt.Sprintf("%s: %v", e.Class, e.Err) }
func (e *SourceError) Unwrap() error { return e.Err }

func NotFound(err error) *SourceError  { return &SourceError{Class: ClassNotFound, Err: err} }
func Transient(err error) *SourceError { return &SourceError{Class: ClassTransient, Err: err} }
func Permanent(err error) *SourceError { return &SourceError{Class: ClassPermanent, Err: err} }
func Canceled(err error) *SourceError  { return &SourceError{Class: ClassCanceled, Err: err} }

// Classify maps a plain error to a Class. Unknown and network-like errors
// are Transient, never NotFound: a shaky connection must not be cached as
// "the record does not exist".
func Classify(err error) Class {
	if err == nil {
		return ClassNone
	}
	var se *SourceError
	if errors.As(err, &se) {
		return se.Class
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ClassCanceled
	}
	if errors.Is(err, ErrNotFound) {
		return ClassNotFound
	}
	return ClassTransient
}
