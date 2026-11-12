// Package netsec evaluates cloud-account authorization against a finite
// action set and organization-tree boundaries.
//
// Resource conditions are closed integer interval sets. Identity and resource
// policy allows are unioned first, policy denies are applied next, and the
// result is then narrowed layer by layer along the organization path: allow
// boundaries intersect the running grant while deny boundaries subtract from
// it. Every Decision exposes the per-layer contribution and the earliest
// deny evidence, and is reproducible from a stored Snapshot.
package netsec
