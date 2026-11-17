package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidRequest indicates that object metadata is malformed.
	ErrInvalidRequest = errors.New("netsec: invalid request")
	// ErrInvalidLabel indicates a malformed or oversized label.
	ErrInvalidLabel = errors.New("netsec: invalid label")
	// ErrImmutableLabel indicates an attempt to override a mandatory label.
	ErrImmutableLabel = errors.New("netsec: immutable label conflict")
	// ErrObjectNotFound indicates that no committed object is visible.
	ErrObjectNotFound = errors.New("netsec: object not found")
	// ErrObjectExists indicates that a committed or staged object already uses
	// the requested identifier.
	ErrObjectExists = errors.New("netsec: object already exists")
	// ErrDigestMismatch indicates that labels and their digest disagree.
	ErrDigestMismatch = errors.New("netsec: label digest mismatch")
	// ErrIncompleteRecord indicates that an object was about to become visible
	// without its complete authorization state.
	ErrIncompleteRecord = errors.New("netsec: incomplete authorization state")
	// ErrTransactionMismatch indicates a stale or foreign transaction token.
	ErrTransactionMismatch = errors.New("netsec: transaction mismatch")
	// ErrAccessDenied indicates that the authorizer rejected a read.
	ErrAccessDenied = errors.New("netsec: access denied")
	// ErrNoAuthorizer indicates a protected read was attempted without an
	// authorizer configured.
	ErrNoAuthorizer = errors.New("netsec: no authorizer configured")
	// ErrNilStore indicates that a host or transaction was configured without
	// a store.
	ErrNilStore = errors.New("netsec: nil store")
)

// InvalidLabelError describes a malformed label.
type InvalidLabelError struct {
	Key    string
	Reason string
}

func (e *InvalidLabelError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s: %s", ErrInvalidLabel, e.Reason)
	}
	return fmt.Sprintf("%s %q: %s", ErrInvalidLabel, e.Key, e.Reason)
}

func (e *InvalidLabelError) Unwrap() error { return ErrInvalidLabel }

// ImmutableLabelError describes an attempted override of a mandatory label.
type ImmutableLabelError struct {
	Key      string
	Source   string
	Value    string
	Required string
}

func (e *ImmutableLabelError) Error() string {
	return fmt.Sprintf(
		"%s: %s label %q=%q conflicts with required value %q",
		ErrImmutableLabel,
		e.Source,
		e.Key,
		e.Value,
		e.Required,
	)
}

func (e *ImmutableLabelError) Unwrap() error { return ErrImmutableLabel }
