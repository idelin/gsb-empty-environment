// Package netsec selects a cloud job credential from multiple trust-ranked
// sources.
//
// The sources form a directed strict partial order: an instance credential,
// a task credential and an environment-injected credential can declare that
// one may never be overridden by a lower-trusted one. Each candidate is bound
// to a tenant, an audience and a validity window. When two or more eligible
// candidates are mutually incomparable the selector returns a ConflictError
// instead of choosing one based on arrival order.
//
// A Refresh reads every source once into an immutable snapshot and evaluates
// that snapshot, so the selection evidence is fixed for the duration of the
// refresh even when sources change or revoke concurrently. The published
// Selection is swapped atomically; failed or conflicting refreshes retain the
// previously published identity.
package netsec
