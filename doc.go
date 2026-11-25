// Package credentialhost implements tenant-scoped delegated credential
// authorization for a multi-tenant backup platform.
//
// A platform principal may act for several tenants, but an assume request is
// authorized against exactly one tenant record. The record pins the exact role
// ID, a randomly generated 128-bit external ID, an allowed source-resource set,
// a maximum session duration, and a policy digest. The source resource must
// also still be owned by that tenant.
//
// Request identity comes from the host-authenticated context, never from the
// request body. Authorization decisions are canonicalized and bound to the
// principal plus tenantID, roleID, externalID, sourceResource, duration, and
// invocation-parameter digest. Audit-only session names are validated but do
// not affect authorization; they are limited to 1..64 bytes from the safe
// alphanumeric/+=,.@_- set.
//
// STS responses are accepted only when audience, role ID, source identity,
// session ID, and expiry match the decision and its requested time limit.
// Mismatched, stale, or late credentials are destroyed. Request IDs are
// idempotent per parameter set; revocation, tenant rotation, and resource
// ownership changes invalidate both cached and in-flight results.
package credentialhost
