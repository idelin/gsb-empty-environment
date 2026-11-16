// Package netsec rewrites simplified PromQL query trees for a multi-tenant
// gateway so that every vector selector is forced to query exactly one
// tenant.
//
// Rewrite removes any pre-existing matchers on the tenant label and appends
// an exact tenant="<id>" matcher to every vector selector, including
// selectors nested in function arguments, range vectors and subqueries.
// Queries without vector selectors pass through unchanged. Queries that can
// overwrite or strip the tenant label (label_replace/label_join writing
// tenant, or tenant in by/without/on/ignoring/group labels) are rejected
// before any mutation occurs.
package netsec
