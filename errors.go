package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidReference is returned when an alias reference is missing its
	// account, region, or name component.
	ErrInvalidReference = errors.New("netsec: invalid alias reference")
	// ErrInvalidBinding is returned when a stored binding is malformed.
	ErrInvalidBinding = errors.New("netsec: invalid alias binding")
	// ErrInvalidKey is returned when stored key material is malformed.
	ErrInvalidKey = errors.New("netsec: invalid public key")
	// ErrAliasNotFound is returned when an alias has no binding in the
	// transaction snapshot.
	ErrAliasNotFound = errors.New("netsec: alias not found")
	// ErrKeyNotFound is returned when a chain terminates at a key that is
	// absent from the transaction snapshot.
	ErrKeyNotFound = errors.New("netsec: public key not found")
	// ErrCycle is returned when alias chain traversal revisits a reference.
	ErrCycle = errors.New("netsec: alias cycle detected")
	// ErrMaxHops is returned when a chain exceeds the configured hop limit.
	ErrMaxHops = errors.New("netsec: alias chain too long")
	// ErrAccountNotAuthorized is returned when a chain would enter an
	// account the caller is not authorized for.
	ErrAccountNotAuthorized = errors.New("netsec: account not authorized")
	// ErrFingerprintMismatch is returned when stored key material does not
	// match its recorded fingerprint.
	ErrFingerprintMismatch = errors.New("netsec: public key fingerprint mismatch")
	// ErrInvalidSignRequest is returned when a signature request fails
	// structural or binding verification.
	ErrInvalidSignRequest = errors.New("netsec: invalid signature request")
)

// CycleError wraps ErrCycle and carries the reference path that closed the
// cycle, including the repeated reference as its final element.
type CycleError struct {
	Path []AliasRef
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("%v: %s", ErrCycle, formatPath(e.Path))
}

// Is makes errors.Is(err, ErrCycle) succeed for CycleError.
func (e *CycleError) Is(target error) bool { return target == ErrCycle }

// AccountAuthError wraps ErrAccountNotAuthorized with the offending account
// and the reference that attempted to enter it.
type AccountAuthError struct {
	Account AccountID
	Ref     AliasRef
}

func (e *AccountAuthError) Error() string {
	return fmt.Sprintf("%v: account %q referenced by %s/%s/%s",
		ErrAccountNotAuthorized, e.Account, e.Ref.Account, e.Ref.Region, e.Ref.Name)
}

// Is makes errors.Is(err, ErrAccountNotAuthorized) succeed.
func (e *AccountAuthError) Is(target error) bool {
	return target == ErrAccountNotAuthorized
}

func formatPath(path []AliasRef) string {
	out := ""
	for i, ref := range path {
		if i > 0 {
			out += " -> "
		}
		out += string(ref.Account) + "/" + string(ref.Region) + "/" + string(ref.Name)
	}
	return out
}
