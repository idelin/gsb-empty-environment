// Package crlstate aggregates partitioned full CRLs and multi-generation delta
// CRLs for a certificate path-validation host.
//
// # Inputs
//
// Each Manifest describes one partition generation: issuer identity, signing
// root, revocation epoch, serial half-open interval, reason mask, validity
// window, optional indirectCRL delegation chain, CRL number and base-delta
// dependency. Full manifests are partitioned baselines. A delta uses BaseDelta
// to point either at its full base (cumulative delta generation) or at the
// preceding delta CRL number (chained delta).
//
// A split or merge must be accompanied by a Migration. Migrations are signed
// by both the old and new roots, and their old and new serial×reason regions
// must be exactly equal with no gaps or overlaps.
//
// # Decision order
//
// Check returns, in order:
//
//  1. StatusLocked if an issuer has two valid, content-conflicting manifests
//     under the same (IssuerID, RevocationEpoch). The pair is retained as
//     ForkEvidence and further ingestion for that issuer is refused.
//  2. StatusRevoked if any valid manifest proves a blocking revocation whose
//     effective time precedes the certificate lifetime end. A certificateHold
//     is non-blocking only when a removeFromCRL entry with the same issuer,
//     serial and HoldID occurs at or after the hold.
//  3. StatusGood only if at least one root-consistent path has a complete,
//     contiguous full-base/matching-delta sequence covering every required
//     reason bit for every instant of [Start, End).
//  4. StatusUnknown otherwise. Missing coverage is reported as a Gap.
//
// Coverage is intentionally conservative:
//
//   - A delta is never applied to a missing, stale or different-epoch base.
//   - A hole in a delta-validity sequence permanently ends usable coverage for
//     that run; later deltas cannot fill it.
//   - Reason bits and time slices cannot be assembled from different roots.
//   - At an instant, the highest revocationEpoch among manifests that cover
//     the queried serial and intersect the required reasons is authoritative.
//     Lower epochs cannot fill a newer epoch's gap. Once a higher epoch has
//     started, its expiry does not resurrect an older epoch's still-open
//     validity window.
//   - Crossing epochs is allowed only across an exact time boundary, either
//     with unchanged partition layout and root, or through a valid Migration.
//
// # Authentication
//
// A nil Verifier trusts the caller to authenticate inputs before ingestion.
// Production hosts should supply a Verifier. Verify receives both RootID and
// IssuerID so a host can enforce its root-to-issuer binding policy in addition
// to signature verification.
//
// # Cache
//
// Cache stores Check results keyed by issuer, serial interval, required reason
// mask and certificate lifetime. A newly ingested manifest invalidates only
// entries whose serial and reason domains intersect, and whose lifetime lies
// in the affected time domain: the manifest window, its contiguous delta
// closure, or the unbounded interval opened by a newly started higher epoch.
// An interval treap keeps the work proportional to the affected entries.
// Locks and migrations invalidate the affected issuer because they can alter
// path connectivity outside a single manifest domain.
package crlstate
