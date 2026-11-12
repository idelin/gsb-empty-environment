package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// Sentinel errors returned by Grant. Callers can use errors.Is to distinguish
// a rejected request from a driver failure.
var (
	// ErrOutsidePermission means the requested or driver-returned listener
	// does not fall inside the granted Permission.
	ErrOutsidePermission = errors.New("netsec: listener outside granted permission")

	// ErrUnresolvedEmpty means an empty host was presented without an
	// EmptyPolicy that resolves it to a concrete listener.
	ErrUnresolvedEmpty = errors.New("netsec: empty listener host is not resolvable by policy")

	// ErrDriverRejected means the driver refused or failed the allocation.
	ErrDriverRejected = errors.New("netsec: driver rejected the listener")
)

// EmptyPolicy resolves an empty listener host. An empty host carries no
// address decision of its own, so the host must explicitly choose how it is
// widened; implementations must never silently turn it into a wildcard.
type EmptyPolicy interface {
	ResolveEmpty(port int) (ListenID, error)
}

// EmptyPolicyFunc adapts a function to the EmptyPolicy interface.
type EmptyPolicyFunc func(port int) (ListenID, error)

// ResolveEmpty calls the wrapped function.
func (f EmptyPolicyFunc) ResolveEmpty(port int) (ListenID, error) {
	return f(port)
}

// DenyEmpty is the default policy: empty hosts are a configuration error,
// never an implicit wildcard.
func DenyEmpty(port int) (ListenID, error) {
	return ListenID{}, ErrUnresolvedEmpty
}

// LoopbackEmptyPolicy resolves an empty host to the loopback address of the
// given family, keeping the service local-only.
func LoopbackEmptyPolicy(family Family) EmptyPolicy {
	return EmptyPolicyFunc(func(port int) (ListenID, error) {
		return ParseID(loopbackLiteral(family), port)
	})
}

// WildcardEmptyPolicy resolves an empty host to the unspecified address of
// the given family. Hosts should only install this after an explicit
// wildcard grant.
func WildcardEmptyPolicy(family Family) EmptyPolicy {
	return EmptyPolicyFunc(func(port int) (ListenID, error) {
		return ParseID(wildcardLiteral(family), port)
	})
}

func loopbackLiteral(family Family) string {
	if family == FamilyIPv4 {
		return "127.0.0.1"
	}
	return "::1"
}

func wildcardLiteral(family Family) string {
	if family == FamilyIPv4 {
		return "0.0.0.0"
	}
	return "::"
}

// Lease is a driver allocation that has not been bound to a ticket yet.
// Release returns the allocated port to the driver; implementations must be
// safe to call once, with later calls returning without error.
type Lease interface {
	// Network returns the socket family, one of "tcp", "tcp4" or "tcp6".
	Network() string

	// Addr returns the concrete listener address in host:port form. A
	// well-behaved driver never returns an empty host or a zero port.
	Addr() string

	// Release recycles the allocation. It is called when validation fails,
	// when the context is canceled, or when the ticket is released.
	Release() error
}

// Driver allocates and recycles concrete listeners on behalf of the host.
type Driver interface {
	Allocate(ctx context.Context, id ListenID) (Lease, error)
}

// Request is a remote-listening grant request.
type Request struct {
	// Network is the requested socket family: "tcp", "tcp4" or "tcp6".
	Network string

	// Host is the requested listener host. The empty string is distinct
	// from a wildcard literal and requires Policy to resolve it.
	Host string

	// Port is the requested port. Zero asks the driver to allocate one.
	Port int

	// Policy resolves an empty Host. nil behaves as DenyEmpty.
	Policy EmptyPolicy
}

// Ticket is a validated, permission-bound listening grant. Releasing it
// recycles the driver lease; Release is idempotent.
type Ticket struct {
	// ID is an opaque, unguessable identifier the host hands back to the
	// authorized client.
	ID string

	// Permission is the original grant scope; the ticket never widens it.
	Permission Permission

	// Granted is the concrete, post-allocation listener identifier.
	Granted ListenID

	lease    Lease
	released bool
}

// Release recycles the underlying driver lease. It is safe to call multiple
// times; only the first call reaches the driver.
func (t *Ticket) Release() error {
	if t == nil || t.released {
		return nil
	}
	t.released = true
	return t.lease.Release()
}

// Grant allocates a listener through driver, validates that both the request
// and the concrete result stay inside permission, and binds the result to a
// ticket.
//
// If the context is canceled while the driver is allocating, or the returned
// listener fails validation, the lease is released before Grant returns so
// no allocation leaks.
func Grant(ctx context.Context, driver Driver, permission Permission, req Request) (*Ticket, error) {
	if driver == nil {
		return nil, errors.New("netsec: nil listener driver")
	}

	requested, err := ParseID(req.Host, req.Port)
	if err != nil {
		return nil, err
	}

	if requested.Empty {
		policy := req.Policy
		if policy == nil {
			policy = EmptyPolicyFunc(DenyEmpty)
		}
		requested, err = policy.ResolveEmpty(req.Port)
		if err != nil {
			return nil, err
		}
		if requested.Empty {
			return nil, ErrUnresolvedEmpty
		}
	}

	if !permission.Contains(requested) {
		return nil, fmt.Errorf("%w: requested %s", ErrOutsidePermission, requested)
	}

	lease, err := driver.Allocate(ctx, requested)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDriverRejected, err)
	}

	actual, err := parseNetworkAddr(req.Network, lease.Addr())
	if err != nil {
		_ = lease.Release()
		return nil, fmt.Errorf("%w: %v", ErrDriverRejected, err)
	}

	if !permission.Contains(actual) {
		_ = lease.Release()
		return nil, fmt.Errorf("%w: driver returned %s", ErrOutsidePermission, actual)
	}

	id, err := newTicketID()
	if err != nil {
		_ = lease.Release()
		return nil, err
	}

	return &Ticket{
		ID:         id,
		Permission: permission,
		Granted:    actual,
		lease:      lease,
	}, nil
}

func newTicketID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netsec: cannot mint ticket id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
