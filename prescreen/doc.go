// Package prescreen is an importable read-admission (pre-screening) component
// for a Go cold-archive host whose storage layer reuses underlying objects by
// XOR-reshuffling 48-bit block numbers and whose projection views can alias
// many logical blocks onto one physical block.
//
// The gateway receives only a batch read description; it is not allowed to
// read every block first and filter afterwards. Every request slice, grant
// slice and revocation slice is instead an affine subspace written as a
// system of GF(2) linear equations (see SolveAffine, Affine.Equations). All
// decisions are computed algebraically from matrix ranks and
// inclusion-exclusion (UnionInclusionExclusion), so a 2^48 block space is
// handled in O(48^3 * 2^MaxSlices) work without enumerating a single block.
//
// # Trust model
//
// View manifests and capabilities carry real Ed25519 signatures that must
// verify under one of the Policy.Roots public keys. A capability binds a
// subject, tenant, exact object version, action and a set of physical affine
// slices. A view edge binds an affine map and the exact parent object version;
// a missing trusted root, a view cycle or any version mismatch fails closed.
//
// # Admission rule
//
// For every request slice, Evaluate composes the whole named view chain, maps
// the logical affine set to its physical image, and requires that image to be
// covered by the union of the same subject's valid grant slices while being
// disjoint from every valid revocation slice. Rank-deficient maps and
// disjoint logical slices that alias the same physical block are counted by
// their actual distinct blocks.
//
// # Read tickets
//
// The returned ReadTicket never expands block numbers. It carries only the
// physical affine sets actually needed, the exact distinct block count per
// object, and algebraic coverage certificates. VerifyTicket is an independent
// consumer-side check: it re-resolves the signed chain, re-verifies every
// signature, recomputes the rank and inclusion-exclusion algebra, and compares
// affine sets semantically. Two sets with identical cardinality but different
// blocks are therefore distinguished; comparing block counts alone is never
// sufficient.
package prescreen
