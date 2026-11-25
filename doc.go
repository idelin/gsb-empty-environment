// Package evidence implements evidence aggregation and independent
// re-validation for IP-address certificate issuance.
//
// The package deliberately keeps the policy checks separate from the
// aggregation search.  Aggregate produces a bundle that contains the raw,
// signed receipts it selected.  Accept (and Audit) recompute every policy
// property from those receipts: the IP is checked again, management domains
// are counted again, route origins are checked again, and observation time is
// intersected again.  Summary fields in Bundle are diagnostic only and are
// never trusted by the verifier.
//
// Time is represented with half-open intervals [Start, End).  A witness may
// observe during a union of intervals, which is why TimeSpan exists instead of
// a single start/end pair.  The intersection of several TimeSpans is computed
// globally; pairwise non-empty intersections are not sufficient.
package evidence
