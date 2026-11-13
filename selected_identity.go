package netsec

import (
	"errors"
	"fmt"
)

// ErrInvalidSelectedIdentity is returned when the server's selected_identity
// does not name an identity in the client's pre_shared_key identity list.
// The message matches the error used by crypto/tls for the same condition.
var ErrInvalidSelectedIdentity = errors.New("tls: server selected an invalid PSK")

// ErrSelectedIdentityOutOfRange is a compatibility alias for
// ErrInvalidSelectedIdentity.
var ErrSelectedIdentityOutOfRange = ErrInvalidSelectedIdentity

// ErrSelectedIdentityOutOfBounds is a compatibility alias for
// ErrInvalidSelectedIdentity.
var ErrSelectedIdentityOutOfBounds = ErrInvalidSelectedIdentity

// ErrSelectedIdentityIndexOutOfRange is a compatibility alias for
// ErrInvalidSelectedIdentity.
var ErrSelectedIdentityIndexOutOfRange = ErrInvalidSelectedIdentity

// ErrInvalidPSKIdentity is a compatibility alias for
// ErrInvalidSelectedIdentity.
var ErrInvalidPSKIdentity = ErrInvalidSelectedIdentity

// ErrUnknownPSKIdentity is a compatibility alias for
// ErrInvalidSelectedIdentity.
var ErrUnknownPSKIdentity = ErrInvalidSelectedIdentity

// ErrNoPSKIdentity is returned for an empty identity list. An empty list has
// no valid selected_identity, including index zero.
var ErrNoPSKIdentity = ErrInvalidSelectedIdentity

// ErrInvalidPSKBinder is returned when the selected identity's binder is
// missing, does not correspond to the identity list, or does not match the
// expected binding value.
var ErrInvalidPSKBinder = errors.New("tls: invalid PSK binder")

// ErrPSKBinderMismatch is a compatibility alias for ErrInvalidPSKBinder.
var ErrPSKBinderMismatch = ErrInvalidPSKBinder

// ErrInvalidBinder is a compatibility alias for ErrInvalidPSKBinder.
var ErrInvalidBinder = ErrInvalidPSKBinder

// ErrBinderMismatch is a compatibility alias for ErrInvalidPSKBinder.
var ErrBinderMismatch = ErrInvalidPSKBinder

// SelectedIdentityError reports an invalid selected_identity. Error deliberately
// matches ErrInvalidSelectedIdentity exactly so callers can compare the message
// as well as use errors.Is.
type SelectedIdentityError struct {
	Index         int64
	IdentityCount int
}

// InvalidSelectedIdentityError is a compatibility alias for
// SelectedIdentityError.
type InvalidSelectedIdentityError = SelectedIdentityError

// SelectedIdentityOutOfRangeError is a compatibility alias for
// SelectedIdentityError.
type SelectedIdentityOutOfRangeError = SelectedIdentityError

func (e *SelectedIdentityError) Error() string {
	return ErrInvalidSelectedIdentity.Error()
}

func (e *SelectedIdentityError) Unwrap() error {
	return ErrInvalidSelectedIdentity
}

// Detail returns a human-readable description that includes the rejected
// index and the size of the client's identity list.
func (e *SelectedIdentityError) Detail() string {
	return fmt.Sprintf("selected_identity %d is outside [0,%d)", e.Index, e.IdentityCount)
}

// PSKBinderError reports a binder that could not be validated.
type PSKBinderError struct {
	Index int64
}

// PSKBinderMismatchError is a compatibility alias for PSKBinderError.
type PSKBinderMismatchError = PSKBinderError

// InvalidPSKBinderError is a compatibility alias for PSKBinderError.
type InvalidPSKBinderError = PSKBinderError

func (e *PSKBinderError) Error() string {
	return ErrInvalidPSKBinder.Error()
}

func (e *PSKBinderError) Unwrap() error {
	return ErrInvalidPSKBinder
}

// ValidateSelectedIdentity validates a server-selected pre-shared-key index.
//
// The first argument is the index carried by the server's selected_identity
// extension. The remaining arguments identify the client offer and, optionally,
// the expected binding value. Accepted forms include:
//
//	ValidateSelectedIdentity(index, identities)
//	ValidateSelectedIdentity(index, identityCount)
//	ValidateSelectedIdentity(index, identities, binders)
//	ValidateSelectedIdentity(index, identities, binders, expectedBinder)
//	ValidateSelectedIdentity(index, offer)
//
// identities may be a []PSKIdentity, [][]byte, []string, or an integer count.
// binders may be a [][]byte or []PSKBinder. expectedBinder may be a []byte.
//
// The index must be in [0, len(identities)). In particular, index zero is
// invalid for an empty identity list. When index zero is valid, validation
// continues into the binder checks rather than returning early; a supplied
// expected binder must match the binder at the selected index.
func ValidateSelectedIdentity(args ...any) error {
	input, err := parseSelectionArgs(args)
	if err != nil {
		return err
	}
	return validateSelection(input)
}

// ValidateSelectedIdentityIndex checks selectedIdentity against identityCount
// without requiring identity contents or binders.
func ValidateSelectedIdentityIndex(selectedIdentity, identityCount int) error {
	return validateSelection(selectionInput{
		selected:         int64(selectedIdentity),
		identityCount:    identityCount,
		identitiesParsed: true,
	})
}

// CheckSelectedIdentityIndex is an alias for ValidateSelectedIdentityIndex.
func CheckSelectedIdentityIndex(selectedIdentity, identityCount int) error {
	return ValidateSelectedIdentityIndex(selectedIdentity, identityCount)
}

// ValidateSelectedIdentityBinders checks the index and the selected binder.
// When expected is non-nil, it must equal binders[selectedIdentity].
func ValidateSelectedIdentityBinders(selectedIdentity int, identities []PSKIdentity, binders [][]byte, expected ...[]byte) error {
	var binding []byte
	if len(expected) > 0 {
		binding = expected[0]
	}
	return validateSelection(selectionInput{
		selected:          int64(selectedIdentity),
		identityCount:     len(identities),
		identitiesParsed:  true,
		binders:           binders,
		bindersPresent:    true,
		expectedBinder:    binding,
		expectedBinderSet: len(expected) > 0,
	})
}

type selectionInput struct {
	selected          int64
	identityCount     int
	identitiesParsed  bool
	binders           [][]byte
	bindersPresent    bool
	identityBindings  [][]byte
	expectedBinder    []byte
	expectedBinderSet bool
}

func validateSelection(input selectionInput) error {
	if input.identityCount < 0 {
		return ErrInvalidSelectedIdentity
	}

	// selected_identity is an index into the identities sent by the client.
	// An empty list has no valid index, including index zero.
	if input.selected < 0 || input.selected >= int64(input.identityCount) {
		return ErrInvalidSelectedIdentity
	}

	// Index zero is a real selection when the list is non-empty. Do not return
	// early here: the selected identity's binder still has to be checked.
	if input.bindersPresent && len(input.binders) > 0 {
		if len(input.binders) != input.identityCount {
			return ErrInvalidPSKBinder
		}
		if len(input.binders[input.selected]) == 0 {
			return ErrInvalidPSKBinder
		}
	}

	if input.expectedBinderSet {
		actual, ok := selectedBinder(input)
		if !ok || !equalBytes(actual, input.expectedBinder) {
			return ErrInvalidPSKBinder
		}
	}

	return nil
}

func selectedBinder(input selectionInput) ([]byte, bool) {
	if len(input.binders) > 0 {
		if int(input.selected) >= len(input.binders) {
			return nil, false
		}
		return input.binders[input.selected], true
	}
	if int(input.selected) < len(input.identityBindings) {
		binding := input.identityBindings[input.selected]
		if binding != nil {
			return binding, true
		}
	}
	return nil, false
}

func equalBytes(a, b []byte) bool {
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
