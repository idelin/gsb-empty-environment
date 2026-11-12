// Package netsec provides a connection-lease layer for directory clients
// (e.g. LDAP) that multiplex several tenants over a pooled connection.
//
// Every query and every result is bound both to a connection generation and
// to a tenant lease. A bind (rebind) takes exclusive ownership of the
// connection, drains all previously issued queries, and issues a fresh
// generation/lease pair. Results that arrive late, or on a previous
// generation, are never delivered to another tenant.
package netsec

import (
	"context"
	"errors"
)

// Sentinel errors reported by the lease manager.
var (
	// ErrEmptyPassword is returned when a non-empty username is presented
	// together with an empty password. RFC 4513 treats such a bind as an
	// anonymous bind; the manager refuses it regardless of whether the
	// directory server reports success.
	ErrEmptyPassword = errors.New("netsec: non-empty username with empty password is an anonymous bind and is not authorized")

	// ErrBindFailed is returned when the directory server rejects a bind.
	ErrBindFailed = errors.New("netsec: directory bind failed")

	// ErrIdentityMismatch is returned when the post-bind identity
	// confirmation callback reports a principal that differs from the one
	// the gateway intended to bind as.
	ErrIdentityMismatch = errors.New("netsec: confirmed identity does not match requested identity")

	// ErrNoIdentity is returned when the post-bind confirmation reports an
	// empty principal even though an authenticated bind was requested.
	ErrNoIdentity = errors.New("netsec: directory did not confirm an authenticated principal")

	// ErrLeaseInvalid is returned when a search is attempted with a lease
	// that has been revoked by a subsequent bind or connection failure.
	ErrLeaseInvalid = errors.New("netsec: lease is no longer valid for this connection")

	// ErrManagerClosed is returned when the manager has been shut down.
	ErrManagerClosed = errors.New("netsec: lease manager is closed")

	// ErrDrainTimeout is returned when in-flight queries do not reach their
	// terminal state before the drain deadline. The connection is poisoned
	// rather than being reused.
	ErrDrainTimeout = errors.New("netsec: timed out draining in-flight queries before bind")

	// ErrConnUnavailable is returned when the connection is marked poisoned
	// and a replacement cannot be established.
	ErrConnUnavailable = errors.New("netsec: directory connection unavailable")
)

// Tenant identifies the directory partition/identity namespace on whose
// behalf a bind and subsequent searches are performed.
type Tenant struct {
	// ID is the stable identifier of the tenant (e.g. realm or org id).
	ID string
	// BaseDN scopes searches for this tenant. It is informational for the
	// lease layer but useful for routing and auditing.
	BaseDN string
}

// BindRequest carries the credentials used to (re)bind the pooled connection.
type BindRequest struct {
	Tenant   Tenant
	Username string
	Password string
}

// SearchRequest is a directory search issued under an active lease.
type SearchRequest struct {
	Tenant Tenant
	BaseDN string
	Filter string
	// Limit bounds the number of entries requested from the directory.
	Limit int
}

// Entry is a single directory result record.
type Entry struct {
	DN         string
	Attributes map[string][]string
}

// Envelope wraps a directory protocol message with the lease tags under
// which it was produced. The manager inspects these tags on every message
// read from the wire so that late or cross-generation traffic can be
// isolated before it reaches application code.
type Envelope interface {
	// OpID is the per-connection request id echoed by the directory.
	OpID() int
	// IsTerminal reports whether the message terminates an operation
	// (search result done, bind response, or an error).
	IsTerminal() bool
	// Entry returns the directory entry carried by the message, or nil for
	// a non-entry message.
	Entry() *Entry
	// Err returns a protocol-level error carried by the message, if any.
	Err() error
}

// Conn is the minimal directory connection contract the lease manager
// needs. A concrete LDAP client (or the scripted test double) implements it.
type Conn interface {
	// Bind performs an LDAP bind on the underlying connection.
	Bind(ctx context.Context, username, password string) error
	// Send starts a search and returns the protocol request id assigned.
	Send(ctx context.Context, req SearchRequest) (opID int, err error)
	// Recv blocks until the next protocol message is available.
	Recv(ctx context.Context) (Envelope, error)
	// Close tears down the underlying transport.
	Close() error
}

// Dialer establishes a fresh directory connection. It is invoked both for
// the initial connection and whenever a poisoned connection must be
// replaced.
type Dialer func(ctx context.Context) (Conn, error)

// IdentityConfirmer is invoked after every successful bind to ask the
// directory (e.g. via the LDAP WhoAmI extended operation) which principal
// the connection is actually bound as. The returned principal must match
// the username the gateway requested; otherwise the bind is rejected even
// though the server reported success.
type IdentityConfirmer interface {
	Confirm(ctx context.Context, c Conn) (principal string, err error)
}

// IdentityConfirmerFunc adapts a plain function to IdentityConfirmer.
type IdentityConfirmerFunc func(ctx context.Context, c Conn) (string, error)

// Confirm implements IdentityConfirmer.
func (f IdentityConfirmerFunc) Confirm(ctx context.Context, c Conn) (string, error) {
	return f(ctx, c)
}

// Cursor streams the results of a leased search. It is safe to call Next
// from a single goroutine; Close may be invoked concurrently.
type Cursor interface {
	// OpID returns the directory protocol request id of this search.
	OpID() int
	// Next returns the next entry. It returns false when the terminal
	// result has been consumed, the context was cancelled, or the lease was
	// revoked.
	Next(ctx context.Context) (*Entry, bool)
	// Err returns the terminal error, if any, after Next returns false.
	Err() error
	// Close cancels the operation. If the terminal response has not yet
	// been received and discarded by the reader goroutine, the connection
	// is poisoned and replaced rather than reused.
	Close() error
}

// Lease is a tenant-scoped, generation-tagged authorization to run
// searches on the pooled connection. A lease is invalidated by the next
// bind, a drain failure, or a connection fault.
type Lease interface {
	// Tenant returns the tenant the lease was issued to.
	Tenant() Tenant
	// Generation returns the connection generation at bind time.
	Generation() uint64
	// LeaseID returns the unique id of this tenant lease.
	LeaseID() uint64
}
