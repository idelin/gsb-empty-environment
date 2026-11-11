package delegation

import "errors"

// Sentinel errors returned by the Service. All of them are safe to compare
// against directly with errors.Is.
var (
	// ErrMalformedScope is returned when a Scope is empty or contains
	// blank entries.
	ErrMalformedScope = errors.New("delegation: malformed scope")
	// ErrInvalidToken is returned when a token cannot be parsed or its
	// signature does not verify.
	ErrInvalidToken = errors.New("delegation: invalid token")
	// ErrTicketNotFound is returned when a ticket ID is unknown to the
	// service.
	ErrTicketNotFound = errors.New("delegation: ticket not found")
	// ErrTicketExpired is returned when the ticket deadline has passed.
	ErrTicketExpired = errors.New("delegation: ticket expired")
	// ErrTicketRevoked is returned when the ticket or one of its ancestors
	// has been revoked.
	ErrTicketRevoked = errors.New("delegation: ticket revoked")
	// ErrTicketConsumed is returned when the ticket has already been claimed
	// by a job.
	ErrTicketConsumed = errors.New("delegation: ticket already consumed")
	// ErrScopeWidened is returned by Delegate when the requested scope is not
	// a strict subset of the parent scope.
	ErrScopeWidened = errors.New("delegation: delegated scope must narrow the parent scope")
	// ErrScopeDenied is returned by Claim when the requested tenant, object
	// or action is not granted by the ticket.
	ErrScopeDenied = errors.New("delegation: requested action is not within the ticket scope")
	// ErrWeakKey is returned by NewService when the HMAC key is too short.
	ErrWeakKey = errors.New("delegation: HMAC key must be at least 32 bytes")
)

// Scope describes the authorization carried by a ticket: the tenant it
// belongs to, the set of object identifiers it can touch, the actions that
// may be performed and an absolute deadline.
type Scope struct {
	// TenantID identifies the tenant. Delegation can never cross tenants.
	TenantID string
	// Objects is the set of object identifiers the ticket grants access to.
	// Every element must be non-empty.
	Objects []string
	// Actions is the set of action identifiers the ticket grants. Every
	// element must be non-empty.
	Actions []string
	// ExpiresAt is the absolute deadline. A zero value is invalid.
	ExpiresAt int64
}

// validate canonicalizes s in place (deduplicates and sorts the sets) and
// returns ErrMalformedScope if the scope is missing a tenant, a deadline or
// any blank object/action entry.
func (s *Scope) validate() error {
	if s.TenantID == "" || s.ExpiresAt == 0 {
		return ErrMalformedScope
	}
	objects, err := normalizeSet(s.Objects)
	if err != nil {
		return err
	}
	actions, err := normalizeSet(s.Actions)
	if err != nil {
		return err
	}
	s.Objects = objects
	s.Actions = actions
	return nil
}

// narrows reports whether s is a subset of parent: the same tenant, a
// deadline that does not exceed the parent deadline, and object/action sets
// that are non-empty subsets of the parent sets.
func (s Scope) narrows(parent Scope) bool {
	if s.TenantID != parent.TenantID {
		return false
	}
	if s.ExpiresAt > parent.ExpiresAt {
		return false
	}
	if !isSubset(s.Objects, parent.Objects) {
		return false
	}
	return isSubset(s.Actions, parent.Actions)
}

// Ticket is the decoded authorization carried by a job. Tokens produced by
// Service are the serialized, HMAC-signed form of a Ticket.
type Ticket struct {
	// ID is the unique, randomly generated ticket identifier.
	ID string
	// ParentID is the ID of the ticket this one was delegated from. It is
	// empty for root tickets.
	ParentID string
	Scope
}

// permits reports whether the ticket grants the given action on the given
// object for the given tenant at time now.
func (t Ticket) permits(tenantID, object, action string, now int64) bool {
	if t.TenantID != tenantID || t.ExpiresAt <= now {
		return false
	}
	return contains(t.Objects, object) && contains(t.Actions, action)
}
