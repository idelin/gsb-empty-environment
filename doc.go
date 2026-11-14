// Package netsec implements patient- and tenant-scoped search for FHIR-like
// health resources.
//
// The package parses FHIR search expressions, including chained parameters,
// _include and _revinclude, and evaluates every reference hop against the same
// access scope as the base search. A reference is never followed when its
// target is outside the caller's tenant, patient set, or resource-type
// allowlist. Pagination and counting are derived from the same authorized
// result set.
package netsec
