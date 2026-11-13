// Package netsec implements a tenant-bound delegation authorization broker for
// credential hosts that assume cloud roles on behalf of multiple customers.
//
// The central rule is that authorization is bound to the tenant, not merely to
// the target role. A platform principal may be delegated by several tenants, but
// a request for one tenant can only use that tenant's registered role, external
// ID, and source resources. The identity of the requester must come from the
// host's authentication context; it is deliberately absent from AssumeRequest.
//
// Assume calls are idempotent per (authenticated principal, request ID). Tenant
// configuration generations fence in-flight STS results so a result produced
// under a retired configuration is never returned to a caller.
package netsec
