// Package netsec plans the assignment of signing requests onto hardware
// isolation domains.
//
// Solve accepts up to MaxRequests requests and up to MaxDomains candidate
// domains with per-key-purpose capacities, explicit request incompatibility
// pairs and the automatic approval-role separation rule for sensitive
// tenants. It returns a plan that first minimizes the number of enabled
// domains and then minimizes the summed latency cost. When the problem is
// infeasible it returns an independently reviewable ConflictSet built around
// an inclusion-minimal infeasible request subset.
//
// Executor turns a plan into capacity reservations through a host-provided
// Reserver. Reservation is all-or-nothing: a failure rolls back every
// reservation already issued so no capacity is left partially occupied.
package netsec
