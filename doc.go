// Package netsec provides tenant and session isolation for a Go HSM proxy
// that multiplexes several tenants onto one PKCS#11 slot.
//
// The underlying slot may expose one application-wide login state. Isolation
// replaces that shared state with per-tenant state and gives each tenant a
// private, randomly generated object-handle space. Session objects are visible
// only to the Session that created them. Token objects are visible to the
// owning tenant's sessions only while that tenant is logged in. Logging out a
// tenant destroys all of its session objects and never changes another
// tenant's login state or objects.
//
// Unknown handles and handles owned by another tenant return the same
// ErrObjectNotFound value, so a caller cannot use error behavior as an oracle
// to discover objects outside its namespace.
package netsec
