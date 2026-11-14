package netsec

import "errors"

// Sentinel errors returned by the authorization component. Callers can use
// errors.Is to branch on them without parsing error strings.
var (
	// ErrEmptyName is returned when a resource name or pattern value is empty.
	ErrEmptyName = errors.New("netsec: empty resource name or pattern value")
	// ErrUnknownKind is returned when a ResourceKind value is not recognized.
	ErrUnknownKind = errors.New("netsec: unknown resource kind")
	// ErrUnknownPatternType is returned when a PatternType is not recognized.
	ErrUnknownPatternType = errors.New("netsec: unknown pattern type")
	// ErrUnknownEffect is returned when an ACL entry has no valid effect.
	ErrUnknownEffect = errors.New("netsec: unknown ACL effect")
	// ErrUnknownAction is returned when an action mask contains no known bit.
	ErrUnknownAction = errors.New("netsec: unknown action")
	// ErrEmptyPrincipal is returned when an entry or request names no identity.
	ErrEmptyPrincipal = errors.New("netsec: empty principal")
	// ErrKindMismatch is returned when a pattern kind and a resource kind do
	// not match. Patterns can never be applied across resource types.
	ErrKindMismatch = errors.New("netsec: pattern/resource kind mismatch")
	// ErrInvalidEntry is returned when an ACL entry fails validation.
	ErrInvalidEntry = errors.New("netsec: invalid ACL entry")
	// ErrNotAuthorized is the reason wrapped when a request is denied.
	ErrNotAuthorized = errors.New("netsec: request not authorized")
	// ErrGroupNotFound is returned for an operation on an unregistered group.
	ErrGroupNotFound = errors.New("netsec: consumer group not found")
	// ErrGroupOwnerConflict is returned when a group name is already bound to
	// another principal. The name cannot be "recycled" through a rename.
	ErrGroupOwnerConflict = errors.New("netsec: consumer group already owned by another principal")
	// ErrSubscriptionStale is returned when a subscription was authorized
	// against an older ACL generation and fails re-verification.
	ErrSubscriptionStale = errors.New("netsec: subscription no longer authorized under current ACL generation")
)
